package api

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/gorilla/mux"
)

// handleUploadPart 处理分片上传的单个分片
func (h *S3Handler) handleUploadPart(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]
	partNumber := vars["partNumber"]
	uploadID := vars["uploadId"]

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

	// 如果是虚拟存储桶，需要通过映射查找真实存储桶
	if requestedBucket.IsVirtual() {
		// 获取虚拟存储桶映射
		mapping, err := h.storage.GetVirtualBucketMapping(bucketName, key)
		if err != nil {
			// 对于分片上传，映射应该在初始化分片上传时已经创建
			// 如果没有找到映射，使用负载均衡器选择一个新的存储桶
			targetBucket, err = h.balancer.SelectBucket(key, contentLength)
			if err != nil {
				h.sendS3Error(w, "InternalError", "Failed to select bucket for multipart upload", key)
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
		// 如果不是虚拟存储桶，拒绝客户端对真实存储桶的直接操作
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	// 检查当前已上传大小 + 本次分片大小是否超过bucket剩余空间
	currentSize, err := h.storage.GetUploadSessionSize(uploadID)
	if err != nil {
		log.Printf("Warning: failed to get upload session size for uploadID %s: %v", uploadID, err)
		// 继续处理，不阻止上传
		currentSize = 0
	}

	projectedSize := currentSize + contentLength
	availableSpace := targetBucket.GetAvailableSpace()
	if projectedSize > availableSpace {
		// 空间不足，自动中止后端分片上传
		log.Printf("Upload would exceed bucket capacity for key %s, aborting multipart upload. Current: %d bytes, Part: %d bytes, Available: %d bytes",
			key, currentSize, contentLength, availableSpace)
		h.abortMultipartUploadInternal(targetBucket, key, uploadID)

		h.sendS3Error(w, "EntityTooLarge",
			fmt.Sprintf("Upload would exceed bucket capacity. Current: %d bytes, Part: %d bytes, Available: %d bytes",
				currentSize, contentLength, availableSpace), key)
		return
	}

	// 转换partNumber为整数
	partNum, err := strconv.Atoi(partNumber)
	if err != nil {
		h.sendS3Error(w, "InvalidArgument", "Invalid part number", key)
		return
	}

	// 生成预签名上传分片URL
	presignClient := s3.NewPresignClient(targetBucket.Client)
	uploadPartInput := &s3.UploadPartInput{
		Bucket:     aws.String(targetBucket.Config.Name),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(int32(partNum)),
	}

	presignRequest, err := presignClient.PresignUploadPart(context.Background(), uploadPartInput, func(opts *s3.PresignOptions) {
		opts.Expires = 15 * time.Minute
	})
	if err != nil {
		log.Printf("Failed to generate upload part URL for key %s, part %s: %v", key, partNumber, err)
		h.sendS3Error(w, "InternalError", "Failed to generate upload part URL", key)
		return
	}

	// 使用反向代理上传分片到真实预签名URL
	req, err := http.NewRequest("PUT", presignRequest.URL, r.Body)
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to create upload part request", key)
		return
	}

	// 设置必要的头
	req.ContentLength = contentLength
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}

	// 执行上传
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to upload part %s for key %s: %v", partNumber, key, err)
		h.sendS3Error(w, "InternalError", "Failed to upload part", key)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// 从响应中获取ETag并返回给客户端
		etag := resp.Header.Get("ETag")
		if etag != "" {
			w.Header().Set("ETag", etag)
		}

		// 更新上传会话的分片数和累积大小
		session, err := h.storage.GetUploadSession(uploadID)
		if err != nil {
			log.Printf("Failed to get upload session for uploadID %s: %v", uploadID, err)
		} else {
			// 更新已完成的分片数
			if err := h.storage.UpdateUploadSession(uploadID, session.CompletedParts+1, "pending"); err != nil {
				log.Printf("Failed to update upload session for uploadID %s: %v", uploadID, err)
			}
			// 累加分片大小
			if err := h.storage.IncrementUploadSessionSize(uploadID, contentLength); err != nil {
				log.Printf("Failed to increment upload session size for uploadID %s: %v", uploadID, err)
			}
		}

		w.WriteHeader(http.StatusOK)
	} else {
		// 读取错误响应体以获取详细信息
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Upload part failed with status %d: %s", resp.StatusCode, string(body))
		h.sendS3Error(w, "InternalError", fmt.Sprintf("Upload part failed with status %d", resp.StatusCode), key)
	}
}

// handleMultipartUpload 初始化分片上传
func (h *S3Handler) handleMultipartUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]

	// 检查请求的存储桶是否为虚拟存储桶
	requestedBucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	var targetBucket *bucket.BucketInfo
	var err error

	// 如果是虚拟存储桶，需要选择真实存储桶并创建映射
	if requestedBucket.IsVirtual() {
		// 选择目标存储桶
		targetBucket, err = h.balancer.SelectBucket(key, 0) // 分片上传时不检查空间
		if err != nil {
			h.sendS3Error(w, "InternalError", "Failed to select bucket for upload", key)
			return
		}

		// 创建虚拟存储桶文件级映射
		if err := h.storage.CreateVirtualBucketMapping(bucketName, key, targetBucket.Config.Name); err != nil {
			h.sendS3Error(w, "InternalError", "Failed to create virtual bucket file mapping", key)
			return
		}
	} else {
		// 如果不是虚拟存储桶，拒绝客户端对真实存储桶的直接操作
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	// 初始化分片上传
	ctx := context.Background()
	createResp, err := targetBucket.Client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(targetBucket.Config.Name),
		Key:    aws.String(key),
	})
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to initiate multipart upload", key)
		return
	}

	uploadID := *createResp.UploadId

	// 记录上传会话到数据库
	if err := h.storage.RecordUploadSession(uploadID, key, targetBucket.Config.Name, 0); err != nil {
		log.Printf("Failed to record upload session for uploadID %s: %v", uploadID, err)
		// 不影响主流程，继续处理
	}

	result := InitiateMultipartUploadResult{
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:   bucketName, // 返回虚拟存储桶名称给客户端
		Key:      key,
		UploadID: uploadID,
	}

	h.sendXMLResponse(w, http.StatusOK, result)
}

// handleListMultipartUploads 列出分片上传
func (h *S3Handler) handleListMultipartUploads(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]

	// 解析查询参数
	queryParams := r.URL.Query()
	keyMarker := queryParams.Get("key-marker")
	uploadIdMarker := queryParams.Get("upload-id-marker")
	prefix := queryParams.Get("prefix")
	delimiter := queryParams.Get("delimiter")
	maxUploadsStr := queryParams.Get("max-uploads")
	maxUploads := 1000
	if maxUploadsStr != "" {
		if m, err := strconv.Atoi(maxUploadsStr); err == nil && m > 0 {
			maxUploads = m
		}
	}

	// 检查请求的存储桶
	requestedBucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	var allUploads []Upload
	var isTruncated bool

	// 如果是虚拟存储桶，从数据库查询上传会话
	if requestedBucket.IsVirtual() {
		// 从数据库获取待处理的上传会话
		sessions, err := h.storage.GetPendingUploadSessions(prefix, keyMarker, uploadIdMarker, maxUploads)
		if err != nil {
			log.Printf("Failed to get pending upload sessions: %v", err)
			// 降级到遍历所有存储桶的方式
			ctx := context.Background()
			allBuckets := h.bucketManager.GetAllBuckets()
			for _, bucket := range allBuckets {
				if bucket.IsVirtual() {
					continue
				}

				// 列出每个真实存储桶的分片上传
				listResp, err := bucket.Client.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
					Bucket:         aws.String(bucket.Config.Name),
					KeyMarker:      aws.String(keyMarker),
					UploadIdMarker: aws.String(uploadIdMarker),
					Prefix:         aws.String(prefix),
					Delimiter:      aws.String(delimiter),
					MaxUploads:     aws.Int32(int32(maxUploads)),
				})
				if err != nil {
					log.Printf("Failed to list multipart uploads for bucket %s: %v", bucket.Config.Name, err)
					continue
				}

				// 将结果添加到列表中
				for _, upload := range listResp.Uploads {
					allUploads = append(allUploads, Upload{
						Key:      aws.ToString(upload.Key),
						UploadID: aws.ToString(upload.UploadId),
						Initiator: Owner{
							ID:          aws.ToString(upload.Initiator.ID),
							DisplayName: aws.ToString(upload.Initiator.DisplayName),
						},
						Owner: Owner{
							ID:          aws.ToString(upload.Owner.ID),
							DisplayName: aws.ToString(upload.Owner.DisplayName),
						},
						StorageClass: string(upload.StorageClass),
						Initiated:    aws.ToTime(upload.Initiated),
					})
				}
			}
		} else {
			// 成功从数据库获取会话
			if len(sessions) > maxUploads {
				sessions = sessions[:maxUploads]
				isTruncated = true
			}

			// 转换会话为Upload格式
			for _, session := range sessions {
				allUploads = append(allUploads, Upload{
					Key:      session.Key,
					UploadID: session.UploadID,
					Initiator: Owner{
						ID:          "s3-balance",
						DisplayName: "S3 Balance User",
					},
					Owner: Owner{
						ID:          "s3-balance",
						DisplayName: "S3 Balance User",
					},
					StorageClass: "STANDARD",
					Initiated:    session.CreatedAt,
				})
			}
		}
	} else {
		// 如果不是虚拟存储桶，拒绝客户端对真实存储桶的直接操作
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	// 构建响应
	result := ListMultipartUploadsResult{
		Xmlns:          "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:         bucketName,
		KeyMarker:      keyMarker,
		UploadIdMarker: uploadIdMarker,
		MaxUploads:     maxUploads,
		IsTruncated:    isTruncated,
		Uploads:        allUploads,
	}

	// 如果有更多结果，设置下一个标记
	if isTruncated && len(allUploads) > 0 {
		lastUpload := allUploads[len(allUploads)-1]
		result.NextKeyMarker = lastUpload.Key
		result.NextUploadIdMarker = lastUpload.UploadID
	}

	h.sendXMLResponse(w, http.StatusOK, result)
}

// handleListMultipartParts 列出分片上传的分片
func (h *S3Handler) handleListMultipartParts(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]
	uploadID := r.URL.Query().Get("uploadId")

	// 解析查询参数
	queryParams := r.URL.Query()
	partNumberMarkerStr := queryParams.Get("part-number-marker")
	partNumberMarker := 0
	if partNumberMarkerStr != "" {
		if m, err := strconv.Atoi(partNumberMarkerStr); err == nil && m > 0 {
			partNumberMarker = m
		}
	}
	maxPartsStr := queryParams.Get("max-parts")
	maxParts := 1000
	if maxPartsStr != "" {
		if m, err := strconv.Atoi(maxPartsStr); err == nil && m > 0 {
			maxParts = m
		}
	}

	// 检查请求的存储桶
	requestedBucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	var targetBucket *bucket.BucketInfo

	// 如果是虚拟存储桶，需要通过映射查找真实存储桶
	if requestedBucket.IsVirtual() {
		// 获取虚拟存储桶映射
		mapping, err := h.storage.GetVirtualBucketMapping(bucketName, key)
		if err != nil {
			// 如果没有找到映射，尝试查询所有真实存储桶
			allBuckets := h.bucketManager.GetAllBuckets()
			for _, bucket := range allBuckets {
				if bucket.IsVirtual() {
					continue
				}
				// 尝试列出分片，如果成功则说明上传在这个桶中
				ctx := context.Background()
				_, err := bucket.Client.ListParts(ctx, &s3.ListPartsInput{
					Bucket:           aws.String(bucket.Config.Name),
					Key:              aws.String(key),
					UploadId:         aws.String(uploadID),
					PartNumberMarker: aws.String(strconv.Itoa(partNumberMarker)),
					MaxParts:         aws.Int32(1), // 只检查是否存在
				})
				if err == nil {
					targetBucket = bucket
					break
				}
			}
			if targetBucket == nil {
				h.sendS3Error(w, "NoSuchUpload", "The specified multipart upload does not exist", uploadID)
				return
			}
		} else {
			// 获取映射到的真实存储桶
			targetBucket, ok = h.bucketManager.GetBucket(mapping.RealBucketName)
			if !ok {
				h.sendS3Error(w, "InternalError", "Mapped real bucket not found", key)
				return
			}
		}
	} else {
		// 如果不是虚拟存储桶，拒绝客户端对真实存储桶的直接操作
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	// 列出分片
	ctx := context.Background()
	listResp, err := targetBucket.Client.ListParts(ctx, &s3.ListPartsInput{
		Bucket:           aws.String(targetBucket.Config.Name),
		Key:              aws.String(key),
		UploadId:         aws.String(uploadID),
		PartNumberMarker: aws.String(strconv.Itoa(partNumberMarker)),
		MaxParts:         aws.Int32(int32(maxParts)),
	})
	if err != nil {
		h.sendS3Error(w, "NoSuchUpload", "The specified multipart upload does not exist", uploadID)
		return
	}

	// 转换分片列表
	var parts []Part
	for _, part := range listResp.Parts {
		parts = append(parts, Part{
			PartNumber:   int(aws.ToInt32(part.PartNumber)),
			LastModified: aws.ToTime(part.LastModified),
			ETag:         aws.ToString(part.ETag),
			Size:         aws.ToInt64(part.Size),
		})
	}

	// 构建响应
	result := ListPartsResult{
		Xmlns:            "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:           bucketName, // 返回虚拟存储桶名称给客户端
		Key:              key,
		UploadID:         uploadID,
		PartNumberMarker: partNumberMarker,
		MaxParts:         maxParts,
		IsTruncated:      aws.ToBool(listResp.IsTruncated),
		Parts:            parts,
	}

	// 设置下一个分片标记
	if listResp.NextPartNumberMarker != nil {
		if nextMarker, err := strconv.Atoi(aws.ToString(listResp.NextPartNumberMarker)); err == nil {
			result.NextPartNumberMarker = nextMarker
		}
	}

	h.sendXMLResponse(w, http.StatusOK, result)
}

// handleCompleteMultipartUpload 完成分片上传
func (h *S3Handler) handleCompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]
	uploadID := r.URL.Query().Get("uploadId")

	// 检查请求的存储桶是否为虚拟存储桶
	requestedBucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	var targetBucket *bucket.BucketInfo

	// 如果是虚拟存储桶，需要通过映射查找真实存储桶
	if requestedBucket.IsVirtual() {
		// 获取虚拟存储桶映射
		mapping, err := h.storage.GetVirtualBucketMapping(bucketName, key)
		if err != nil {
			h.sendS3Error(w, "NoSuchKey", "The specified key does not exist", key)
			return
		}

		// 获取映射到的真实存储桶
		targetBucket, ok = h.bucketManager.GetBucket(mapping.RealBucketName)
		if !ok {
			h.sendS3Error(w, "InternalError", "Mapped real bucket not found", key)
			return
		}
	} else {
		// 如果不是虚拟存储桶，拒绝客户端对真实存储桶的直接操作
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	// 解析请求体以获取分片列表
	var completeReq CompleteMultipartUpload
	body, _ := io.ReadAll(r.Body)
	err := xml.Unmarshal(body, &completeReq)
	if err != nil {
		log.Printf("Failed to parse CompleteMultipartUpload request body: %v, body: %s", err, string(body))
		h.sendS3Error(w, "MalformedXML", "The XML you provided was not well-formed", key)
		return
	}

	log.Printf("CompleteMultipartUpload request - Bucket: %s, Key: %s, UploadID: %s, Parts: %d",
		bucketName, key, uploadID, len(completeReq.Parts))
	for i, part := range completeReq.Parts {
		log.Printf("  Part %d: PartNumber=%d, ETag=%s", i+1, part.PartNumber, part.ETag)
	}

	// 最终检查：验证累积大小是否超过bucket可用空间
	totalSize, err := h.storage.GetUploadSessionSize(uploadID)
	if err != nil {
		log.Printf("Warning: failed to get upload session size for uploadID %s: %v", uploadID, err)
		// 继续处理，不阻止完成操作
		totalSize = 0
	}

	if totalSize > 0 {
		availableSpace := targetBucket.GetAvailableSpace()
		if totalSize > availableSpace {
			// 空间不足，自动中止后端分片上传
			log.Printf("Upload size exceeds bucket capacity for key %s, aborting multipart upload. Total: %d bytes, Available: %d bytes",
				key, totalSize, availableSpace)
			h.abortMultipartUploadInternal(targetBucket, key, uploadID)

			h.sendS3Error(w, "EntityTooLarge",
				fmt.Sprintf("Upload size exceeds bucket capacity. Total: %d bytes, Available: %d bytes",
					totalSize, availableSpace), key)
			return
		}
	}

	// 完成分片上传
	ctx := context.Background()
	sort.SliceStable(completeReq.Parts, func(i, j int) bool {
		return completeReq.Parts[i].PartNumber < completeReq.Parts[j].PartNumber
	})

	var parts []types.CompletedPart
	for _, part := range completeReq.Parts {
		parts = append(parts, types.CompletedPart{
			ETag:       aws.String(part.ETag),
			PartNumber: aws.Int32(int32(part.PartNumber)),
		})
	}

	log.Printf("Calling CompleteMultipartUpload on real bucket %s with uploadID %s", targetBucket.Config.Name, uploadID)
	completeResp, err := targetBucket.Client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(targetBucket.Config.Name),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	if err != nil {
		log.Printf("CompleteMultipartUpload failed: %v", err)
		if apiErr, ok := getAPIError(err); ok {
			h.sendS3Error(w, apiErr.ErrorCode(), apiErr.ErrorMessage(), key)
		} else {
			h.sendS3Error(w, "InternalError", "Failed to complete multipart upload", key)
		}
		return
	}

	result := CompleteMultipartUploadResult{
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
		Location: "/" + bucketName + "/" + key, // 返回虚拟存储桶路径
		Bucket:   bucketName,                   // 返回虚拟存储桶名称
		Key:      key,
		ETag:     *completeResp.ETag,
	}

	// 获取完成上传后的对象大小
	var objectSize int64
	headResp, err := targetBucket.Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(targetBucket.Config.Name),
		Key:    aws.String(key),
	})
	if err != nil {
		// 如果获取大小失败，记录警告但不影响响应
		log.Printf("Warning: Failed to get object size after multipart upload for key %s: %v", key, err)
		objectSize = 0
	} else if headResp.ContentLength != nil {
		objectSize = *headResp.ContentLength
	}

	// 记录对象元数据（使用实际大小）
	h.storage.RecordObject(key, targetBucket.Config.Name, objectSize, nil)

	// 更新存储桶使用量
	if objectSize > 0 {
		targetBucket.UpdateUsedSize(objectSize)
	}

	// 更新上传会话状态为已完成
	if err := h.storage.UpdateUploadSession(uploadID, len(completeReq.Parts), "completed"); err != nil {
		log.Printf("Failed to update upload session status to completed for uploadID %s: %v", uploadID, err)
		// 不影响主流程
	}

	h.sendXMLResponse(w, http.StatusOK, result)
}

func getAPIError(err error) (smithy.APIError, bool) {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}

	var opErr *smithy.OperationError
	if errors.As(err, &opErr) {
		if apiErr, ok := opErr.Err.(smithy.APIError); ok {
			return apiErr, true
		}
	}

	return nil, false
}

// abortMultipartUploadInternal 内部方法：向后端S3发送中止分片上传请求
func (h *S3Handler) abortMultipartUploadInternal(targetBucket *bucket.BucketInfo, key, uploadID string) error {
	ctx := context.Background()
	_, err := targetBucket.Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(targetBucket.Config.Name),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		log.Printf("Failed to abort multipart upload for key %s, uploadID %s: %v", key, uploadID, err)
		return err
	}

	// 更新上传会话状态为已中止
	if err := h.storage.UpdateUploadSession(uploadID, 0, "aborted"); err != nil {
		log.Printf("Failed to update upload session status to aborted for uploadID %s: %v", uploadID, err)
	}

	log.Printf("Successfully aborted multipart upload for key %s, uploadID %s", key, uploadID)
	return nil
}

// handleAbortMultipartUpload 中止分片上传
func (h *S3Handler) handleAbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]
	uploadID := r.URL.Query().Get("uploadId")

	// 检查请求的存储桶是否为虚拟存储桶
	requestedBucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}

	var targetBucket *bucket.BucketInfo

	// 如果是虚拟存储桶，需要通过映射查找真实存储桶
	if requestedBucket.IsVirtual() {
		// 获取虚拟存储桶映射
		mapping, err := h.storage.GetVirtualBucketMapping(bucketName, key)
		if err != nil {
			// 如果映射不存在，可能是上传已经被中止了，返回成功
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
		// 如果不是虚拟存储桶，拒绝客户端对真实存储桶的直接操作
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 中止分片上传
	ctx := context.Background()
	_, err := targetBucket.Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(targetBucket.Config.Name),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		// 如果中止失败，可能是因为上传已经完成或中止，不需要报错
		log.Printf("Failed to abort multipart upload for key %s: %v", key, err)
	}

	// 更新上传会话状态为已中止
	if err := h.storage.UpdateUploadSession(uploadID, 0, "aborted"); err != nil {
		log.Printf("Failed to update upload session status to aborted for uploadID %s: %v", uploadID, err)
		// 不影响主流程
	}

	// 如果是虚拟存储桶，还需要删除文件级别映射
	if requestedBucket.IsVirtual() {
		h.storage.DeleteVirtualBucketFileMapping(bucketName, key)
	}

	w.WriteHeader(http.StatusNoContent)
}
