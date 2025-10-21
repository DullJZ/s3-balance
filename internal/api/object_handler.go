package api

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/gorilla/mux"
)

// handleObjectOperations 处理对象相关操作
func (h *S3Handler) handleObjectOperations(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]

	// 记录操作指标
	start := time.Now()
	method := r.Method
	var status = "success"
	defer func() {
		if h.metrics != nil {
			duration := time.Since(start).Seconds()
			h.metrics.RecordS3Operation(method, bucketName, status)
			h.metrics.RecordS3OperationDuration(method, bucketName, duration)
		}
	}()

	switch r.Method {
	case "GET":
		h.handleGetObject(w, r, bucketName, key)
	case "HEAD":
		h.handleHeadObject(w, r, bucketName, key)
	case "PUT":
		h.handlePutObject(w, r, bucketName, key)
	case "DELETE":
		h.handleDeleteObject(w, r, bucketName, key)
	}
}

// handleGetObject 获取对象（默认使用预签名URL重定向）
func (h *S3Handler) handleGetObject(w http.ResponseWriter, r *http.Request, bucketName string, key string) {
	// 检查请求的存储桶是否为虚拟存储桶
	requestedBucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	// 如果是虚拟存储桶，需要通过映射查找真实存储桶
	var err error
	var bucket1 *bucket.BucketInfo

	if requestedBucket.IsVirtual() {
		// 获取虚拟存储桶映射
		mapping, err := h.storage.GetVirtualBucketMapping(bucketName, key)
		if err != nil {
			h.sendS3Error(w, "NoSuchKey", "The specified key does not exist", key)
			return
		}

		// 获取映射到的真实存储桶
		bucket1, ok = h.bucketManager.GetBucket(mapping.RealBucketName)
		if !ok {
			h.sendS3Error(w, "InternalError", "Mapped real bucket not found", key)
			return
		}

		h.recordBackendOperation(bucket1, bucket.OperationTypeB)
	}

	// 生成预签名下载URL
	downloadInfo, err := h.presigner.GenerateDownloadURL(
		context.Background(),
		bucket1,
		key,
	)
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to generate download URL", key)
		return
	}

	// 根据配置决定使用代理模式还是重定向模式
	if h.proxyModeEnabled() {
		// 代理模式：流式传输内容给客户端
		resp, err := http.Get(downloadInfo.URL)
		if err != nil {
			h.sendS3Error(w, "InternalError", "Failed to fetch object", key)
			return
		}
		defer resp.Body.Close()

		// 检查响应状态
		if resp.StatusCode != http.StatusOK {
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
			return
		}

		// 复制重要的响应头
		if contentType := resp.Header.Get("Content-Type"); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
			w.Header().Set("Content-Length", contentLength)
		} else if resp.ContentLength >= 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
		} else if obj, err := h.storage.GetObjectInfo(key); err == nil {
			w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
		}
		if lastModified := resp.Header.Get("Last-Modified"); lastModified != "" {
			w.Header().Set("Last-Modified", lastModified)
		}
		if etag := resp.Header.Get("ETag"); etag != "" {
			w.Header().Set("ETag", etag)
		}
		if contentEncoding := resp.Header.Get("Content-Encoding"); contentEncoding != "" {
			w.Header().Set("Content-Encoding", contentEncoding)
		}
		if cacheControl := resp.Header.Get("Cache-Control"); cacheControl != "" {
			w.Header().Set("Cache-Control", cacheControl)
		}

		// 流式复制响应体
		_, err = io.Copy(w, resp.Body)
		if err != nil {
			log.Printf("Error streaming response body for key %s: %v", key, err)
		}
	} else {
		// 重定向模式：返回302重定向到预签名URL（默认）
		http.Redirect(w, r, downloadInfo.URL, http.StatusFound)
	}
}

// handleHeadObject 获取对象元数据
func (h *S3Handler) handleHeadObject(w http.ResponseWriter, r *http.Request, bucketName string, key string) {
	// 检查请求的存储桶是否为虚拟存储桶
	requestedBucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// 如果是虚拟存储桶，需要通过映射查找真实存储桶
	if requestedBucket.IsVirtual() {
		// 获取虚拟存储桶映射
		mapping, err := h.storage.GetVirtualBucketMapping(bucketName, key)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		_ = mapping // 使用mapping变量，避免编译错误

		// 查找对象信息（在映射的真实存储桶中）
		obj, err := h.storage.GetObjectInfo(key)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		h.setObjectHeaders(w, obj)
		return
	}

	// 真实存储桶的直接处理
	// 从存储中获取对象信息
	obj, err := h.storage.GetObjectInfo(key)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	h.setObjectHeaders(w, obj)
	w.WriteHeader(http.StatusOK)
}

// handlePutObject 上传对象
func (h *S3Handler) handlePutObject(w http.ResponseWriter, r *http.Request, bucketName string, key string) {
	// 检查请求的存储桶是否为虚拟存储桶
	requestedBucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	// 获取内容长度
	contentLength := r.ContentLength
	if contentLength < 0 {
		h.sendS3Error(w, "MissingContentLength", "Content-Length header is required", key)
		return
	}

	var targetBucket *bucket.BucketInfo
	var err error

	// 如果是虚拟存储桶，需要选择真实存储桶并创建映射
	if requestedBucket.IsVirtual() {
		// 获取虚拟存储桶文件映射，如果不存在则创建
		mapping, mappingErr := h.storage.GetVirtualBucketMapping(bucketName, key)
		if mappingErr != nil {
			// 映射不存在，使用负载均衡器选择真实存储桶并创建映射
			targetBucket, err = h.balancer.SelectBucket(key, contentLength)
			if err != nil {
				h.sendS3Error(w, "InsufficientStorage", "No bucket has enough space", key)
				return
			}

			// 创建虚拟存储桶文件级映射
			if err := h.storage.CreateVirtualBucketMapping(bucketName, key, targetBucket.Config.Name); err != nil {
				h.sendS3Error(w, "InternalError", "Failed to create virtual bucket file mapping", key)
				return
			}
		} else {
			// 映射已存在，获取对应的真实存储桶
			targetBucket, ok = h.bucketManager.GetBucket(mapping.RealBucketName)
			if !ok {
				h.sendS3Error(w, "InternalError", "Mapped real bucket not found", key)
				return
			}
		}
	} else {
		// 如果不是虚拟存储桶，拒绝客户端对真实存储桶的直接PUT操作
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	h.recordBackendOperation(targetBucket, bucket.OperationTypeA)

	// 生成预签名上传URL
	uploadInfo, err := h.presigner.GenerateUploadURL(
		context.Background(),
		targetBucket,
		key,
		r.Header.Get("Content-Type"),
		nil, // metadata
	)
	if err != nil {
		log.Printf("Failed to generate upload URL for key %s in bucket %s: %v", key, targetBucket.Config.Name, err)
		h.sendS3Error(w, "InternalError", "Failed to generate upload URL", key)
		return
	}

	// 只使用反向代理上传到真实预签名URL，不再返回307重定向
	// 创建新的请求
	req, err := http.NewRequest(uploadInfo.Method, uploadInfo.URL, r.Body)
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to create upload request", key)
		return
	}

	// 设置必要的头
	req.ContentLength = contentLength
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	// 添加预签名URL所需的额外头
	for k, v := range uploadInfo.Headers {
		req.Header.Set(k, v)
	}

	// 执行上传
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to upload object", key)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// 记录对象元数据
		h.storage.RecordObject(key, targetBucket.Config.Name, contentLength, nil)
		targetBucket.UpdateUsedSize(contentLength)

		// 返回成功响应
		w.Header().Set("ETag", fmt.Sprintf("\"%x\"", time.Now().UnixNano()))
		w.WriteHeader(http.StatusOK)
	} else {
		// 读取错误响应体以获取详细信息
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Upload failed with status %d: %s", resp.StatusCode, string(body))
		h.sendS3Error(w, "InternalError", fmt.Sprintf("Upload failed with status %d", resp.StatusCode), key)
	}
}

// handleDeleteObject 删除对象
func (h *S3Handler) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucketName string, key string) {
	// 检查请求的存储桶是否为虚拟存储桶
	requestedBucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	var targetBucket *bucket.BucketInfo
	var err error

	if requestedBucket.IsVirtual() {
		// 获取虚拟存储桶文件映射
		mapping, err := h.storage.GetVirtualBucketMapping(bucketName, key)
		if err != nil {
			// 对象不存在，S3规范要求返回204
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// 获取映射到的真实存储桶
		targetBucket, ok = h.bucketManager.GetBucket(mapping.RealBucketName)
		if !ok {
			h.sendS3Error(w, "InternalError", "Mapped real bucket not found", key)
			return
		}
	} else {
		// 如果不是虚拟存储桶，拒绝客户端对真实存储桶的直接DELETE操作
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.recordBackendOperation(targetBucket, bucket.OperationTypeA)

	// 生成预签名删除URL
	deleteInfo, err := h.presigner.GenerateDeleteURL(
		context.Background(),
		targetBucket,
		key,
	)
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to generate delete URL", key)
		return
	}

	// 执行删除
	req, _ := http.NewRequest("DELETE", deleteInfo.URL, nil)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to delete object", key)
		return
	}
	defer resp.Body.Close()

	// 从数据库中删除对象记录
	h.storage.DeleteObject(key)

	// 如果是虚拟存储桶，还需要删除文件级别映射
	if requestedBucket.IsVirtual() {
		h.storage.DeleteVirtualBucketFileMapping(bucketName, key)
	}

	// S3规范要求删除操作总是返回204
	w.WriteHeader(http.StatusNoContent)
}
