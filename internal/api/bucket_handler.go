package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// handleListBuckets 处理列出所有存储桶请求
func (h *S3Handler) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets := h.bucketManager.GetAllBuckets()

	result := ListBucketsResult{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
		Owner: Owner{
			ID:          "s3-balance",
			DisplayName: "S3 Balance Service",
		},
		Buckets: Buckets{
			Bucket: make([]BucketInfo, 0, len(buckets)),
		},
	}

	for _, b := range buckets {
		// 只显示启用的虚拟存储桶，对客户端隐藏底层真实存储桶
		if b.IsAvailable() && b.Config.Enabled && b.Config.Virtual {
			result.Buckets.Bucket = append(result.Buckets.Bucket, BucketInfo{
				Name:         b.Config.Name,
				CreationDate: time.Now().Add(-24 * time.Hour), // 模拟创建时间
			})
		}
	}

	h.sendXMLResponse(w, http.StatusOK, result)
}

// handleBucketOperations 处理存储桶相关操作
func (h *S3Handler) handleBucketOperations(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]

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
		h.handleListObjects(w, r, bucketName)
	case "HEAD":
		h.handleHeadBucket(w, r, bucketName)
	case "PUT":
		h.handleCreateBucket(w, r, bucketName)
	case "DELETE":
		h.handleDeleteBucket(w, r, bucketName)
	}
}

// handleListObjects 列出存储桶中的对象
func (h *S3Handler) handleListObjects(w http.ResponseWriter, r *http.Request, bucketName string) {
	// 检查bucket是否存在
	bucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	// 如果是虚拟存储桶，列出虚拟存储桶中的对象
	if bucket.IsVirtual() {
		h.handleListObjectsForVirtualBucket(w, r, bucketName)
		return
	}

	// 如果不是虚拟存储桶，拒绝客户端访问真实存储桶
	h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
}

// handleListObjectsForVirtualBucket 列出虚拟存储桶中的对象
func (h *S3Handler) handleListObjectsForVirtualBucket(w http.ResponseWriter, r *http.Request, bucketName string) {
	// 解析查询参数
	prefix := r.URL.Query().Get("prefix")
	marker := r.URL.Query().Get("marker")
	maxKeysStr := r.URL.Query().Get("max-keys")
	// delimiter := r.URL.Query().Get("delimiter") // 暂时不支持delimiter

	maxKeys := 1000
	if maxKeysStr != "" {
		if mk, err := strconv.Atoi(maxKeysStr); err == nil {
			maxKeys = mk
		}
	}

	// 从存储服务获取虚拟存储桶中的对象
	objects, err := h.storage.GetVirtualBucketObjects(bucketName)
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to list virtual bucket objects", bucketName)
		return
	}

	result := ListBucketResult{
		Xmlns:       "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:        bucketName,
		Prefix:      prefix,
		Marker:      marker,
		MaxKeys:     maxKeys,
		IsTruncated: false,
		Contents:    make([]ObjectInfo, 0, len(objects)),
	}

	// 过滤对象并转换为S3格式
	for _, obj := range objects {
		// 前缀过滤
		if prefix != "" && !strings.HasPrefix(obj.Key, prefix) {
			continue
		}

		// Marker过滤
		if marker != "" && obj.Key <= marker {
			continue
		}

		result.Contents = append(result.Contents, ObjectInfo{
			Key:          obj.Key,
			LastModified: obj.UpdatedAt,
			ETag:         fmt.Sprintf("\"%x\"", obj.ID),
			Size:         obj.Size,
		})
	}

	// 如果超过了最大数量，设置截断标志
	if len(result.Contents) > maxKeys {
		result.Contents = result.Contents[:maxKeys]
		result.IsTruncated = true
	}

	h.sendXMLResponse(w, http.StatusOK, result)
}

// handleHeadBucket 检查存储桶是否存在
func (h *S3Handler) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucketName string) {
	bucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// 虚拟存储桶也应该返回成功状态
	if bucket.IsVirtual() {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 如果不是虚拟存储桶，拒绝客户端访问真实存储桶
	w.WriteHeader(http.StatusNotFound)
}

// handleCreateBucket 创建存储桶（虚拟实现）
func (h *S3Handler) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucketName string) {
	// 检查是否已经存在同名存储桶
	if bucket, exists := h.bucketManager.GetBucket(bucketName); exists {
		// 如果是虚拟存储桶，检查是否已经有映射
		if bucket.IsVirtual() {
			// 虚拟存储桶不需要检查映射，文件级映射只在有文件时才创建
			h.sendS3Error(w, "BucketAlreadyExists", "The requested bucket name is not available", bucketName)
			return
		} else {
			// 如果是真实存储桶，返回已存在错误
			h.sendS3Error(w, "BucketAlreadyExists", "The requested bucket name is not available", bucketName)
			return
		}
	}

	// 检查是否为虚拟存储桶
	if requestedBucket, exists := h.bucketManager.GetBucket(bucketName); exists && requestedBucket.IsVirtual() {
		// 虚拟存储桶需要选择一个真实存储桶进行映射
		realBuckets := h.bucketManager.GetRealBuckets()
		if len(realBuckets) == 0 {
			h.sendS3Error(w, "InternalError", "No real buckets available for virtual bucket mapping", bucketName)
			return
		}

		// 简化：选择第一个可用的真实存储桶
		// 实际应用中可能需要更复杂的策略
		targetBucket := realBuckets[0]

		// 创建虚拟存储桶到真实存储桶的映射
		if err := h.storage.CreateVirtualBucketMapping(bucketName, "", targetBucket.Config.Name); err != nil {
			h.sendS3Error(w, "InternalError", "Failed to create virtual bucket mapping", bucketName)
			return
		}
	}

	// 在负载均衡场景下，不真正创建bucket，只返回成功
	// 实际的bucket应该在配置中预先定义
	w.Header().Set("Location", "/"+bucketName)
	w.WriteHeader(http.StatusOK)
}

// handleDeleteBucket 删除存储桶（虚拟实现）
func (h *S3Handler) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucketName string) {
	// 检查存储桶是否存在
	bucket, exists := h.bucketManager.GetBucket(bucketName)
	if !exists {
		// 不存在的桶，返回成功（S3标准）
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 虚拟存储桶需要删除映射关系
	if bucket.IsVirtual() {
		// 删除虚拟存储桶映射
		if err := h.storage.DeleteVirtualBucketMapping(bucketName); err != nil {
			h.sendS3Error(w, "InternalError", "Failed to delete virtual bucket mapping", bucketName)
			return
		}
	}

	// 在负载均衡场景下，不真正删除真实bucket
	w.WriteHeader(http.StatusNoContent)
}
