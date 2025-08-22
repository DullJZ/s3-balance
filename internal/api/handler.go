package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/DullJZ/s3-balance/internal/balancer"
	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/DullJZ/s3-balance/internal/storage"
	"github.com/DullJZ/s3-balance/pkg/presigner"
	"github.com/gorilla/mux"
)

// Handler API处理器
type Handler struct {
	bucketManager *bucket.Manager
	balancer      *balancer.Balancer
	presigner     *presigner.Presigner
	storage       *storage.Service
}

// NewHandler 创建新的API处理器
func NewHandler(
	bucketManager *bucket.Manager,
	balancer *balancer.Balancer,
	presigner *presigner.Presigner,
	storage *storage.Service,
) *Handler {
	return &Handler{
		bucketManager: bucketManager,
		balancer:      balancer,
		presigner:     presigner,
		storage:       storage,
	}
}

// RegisterRoutes 注册路由
func (h *Handler) RegisterRoutes(router *mux.Router) {
	// 健康检查
	router.HandleFunc("/health", h.handleHealth).Methods("GET")
	
	// 存储桶状态
	router.HandleFunc("/api/v1/buckets", h.handleListBuckets).Methods("GET")
	router.HandleFunc("/api/v1/buckets/{bucket}/stats", h.handleBucketStats).Methods("GET")
	
	// 预签名URL生成
	router.HandleFunc("/api/v1/presign/upload", h.handlePresignUpload).Methods("POST")
	router.HandleFunc("/api/v1/presign/download", h.handlePresignDownload).Methods("POST")
	router.HandleFunc("/api/v1/presign/delete", h.handlePresignDelete).Methods("POST")
	router.HandleFunc("/api/v1/presign/multipart", h.handlePresignMultipart).Methods("POST")
	
	// 对象操作（记录元数据）
	router.HandleFunc("/api/v1/objects", h.handleListObjects).Methods("GET")
	router.HandleFunc("/api/v1/objects/{key:.*}", h.handleGetObjectInfo).Methods("GET")
	router.HandleFunc("/api/v1/objects/{key:.*}", h.handleDeleteObject).Methods("DELETE")
}

// 健康检查
func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status": "healthy",
		"time":   time.Now().Unix(),
	}
	h.sendJSON(w, http.StatusOK, response)
}

// 列出所有存储桶状态
func (h *Handler) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets := h.bucketManager.GetAllBuckets()
	
	var bucketList []map[string]interface{}
	for _, b := range buckets {
		bucketList = append(bucketList, map[string]interface{}{
			"name":           b.Config.Name,
			"endpoint":       b.Config.Endpoint,
			"region":         b.Config.Region,
			"max_size":       b.Config.MaxSize,
			"max_size_bytes": b.Config.MaxSizeBytes,
			"used_size":      b.GetUsedSize(),
			"available":      b.IsAvailable(),
			"weight":         b.Config.Weight,
			"enabled":        b.Config.Enabled,
		})
	}
	
	h.sendJSON(w, http.StatusOK, map[string]interface{}{
		"buckets":  bucketList,
		"strategy": h.balancer.GetStrategy(),
	})
}

// 获取单个存储桶统计
func (h *Handler) handleBucketStats(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	
	bucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendError(w, http.StatusNotFound, "bucket not found")
		return
	}
	
	stats := map[string]interface{}{
		"name":            bucket.Config.Name,
		"max_size_bytes":  bucket.Config.MaxSizeBytes,
		"used_size":       bucket.GetUsedSize(),
		"available_space": bucket.GetAvailableSpace(),
		"available":       bucket.IsAvailable(),
		"last_checked":    bucket.LastChecked,
	}
	
	h.sendJSON(w, http.StatusOK, stats)
}

// PresignUploadRequest 上传预签名请求
type PresignUploadRequest struct {
	Key         string            `json:"key"`
	Size        int64            `json:"size"`
	ContentType string            `json:"content_type,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// 生成上传预签名URL
func (h *Handler) handlePresignUpload(w http.ResponseWriter, r *http.Request) {
	var req PresignUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	
	if req.Key == "" {
		h.sendError(w, http.StatusBadRequest, "key is required")
		return
	}
	
	// 选择存储桶
	bucket, err := h.balancer.SelectBucket(req.Key, req.Size)
	if err != nil {
		h.sendError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	
	// 生成预签名URL
	uploadURL, err := h.presigner.GenerateUploadURL(
		context.Background(),
		bucket,
		req.Key,
		req.ContentType,
		req.Metadata,
	)
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "failed to generate upload URL")
		return
	}
	
	// 记录对象元数据
	if err := h.storage.RecordObject(req.Key, bucket.Config.Name, req.Size, req.Metadata); err != nil {
		log.Printf("Failed to record object metadata: %v", err)
	}
	
	// 更新存储桶使用量（预估）
	bucket.UpdateUsedSize(req.Size)
	
	h.sendJSON(w, http.StatusOK, uploadURL)
}

// PresignDownloadRequest 下载预签名请求
type PresignDownloadRequest struct {
	Key string `json:"key"`
}

// 生成下载预签名URL
func (h *Handler) handlePresignDownload(w http.ResponseWriter, r *http.Request) {
	var req PresignDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	
	if req.Key == "" {
		h.sendError(w, http.StatusBadRequest, "key is required")
		return
	}
	
	// 查找对象所在的存储桶
	bucketName, err := h.storage.FindObjectBucket(req.Key)
	if err != nil {
		h.sendError(w, http.StatusNotFound, "object not found")
		return
	}
	
	bucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendError(w, http.StatusNotFound, "bucket not found")
		return
	}
	
	// 生成预签名URL
	downloadURL, err := h.presigner.GenerateDownloadURL(
		context.Background(),
		bucket,
		req.Key,
	)
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "failed to generate download URL")
		return
	}
	
	h.sendJSON(w, http.StatusOK, downloadURL)
}

// PresignDeleteRequest 删除预签名请求
type PresignDeleteRequest struct {
	Key string `json:"key"`
}

// 生成删除预签名URL
func (h *Handler) handlePresignDelete(w http.ResponseWriter, r *http.Request) {
	var req PresignDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	
	if req.Key == "" {
		h.sendError(w, http.StatusBadRequest, "key is required")
		return
	}
	
	// 查找对象所在的存储桶
	bucketName, err := h.storage.FindObjectBucket(req.Key)
	if err != nil {
		h.sendError(w, http.StatusNotFound, "object not found")
		return
	}
	
	bucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendError(w, http.StatusNotFound, "bucket not found")
		return
	}
	
	// 生成预签名URL
	deleteURL, err := h.presigner.GenerateDeleteURL(
		context.Background(),
		bucket,
		req.Key,
	)
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "failed to generate delete URL")
		return
	}
	
	h.sendJSON(w, http.StatusOK, deleteURL)
}

// PresignMultipartRequest 分片上传预签名请求
type PresignMultipartRequest struct {
	Key       string `json:"key"`
	PartCount int    `json:"part_count"`
	Size      int64  `json:"size"`
}

// 生成分片上传预签名URLs
func (h *Handler) handlePresignMultipart(w http.ResponseWriter, r *http.Request) {
	var req PresignMultipartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.sendError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	
	if req.Key == "" || req.PartCount <= 0 {
		h.sendError(w, http.StatusBadRequest, "invalid parameters")
		return
	}
	
	// 选择存储桶
	bucket, err := h.balancer.SelectBucket(req.Key, req.Size)
	if err != nil {
		h.sendError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	
	// 生成预签名URLs
	multipartURLs, err := h.presigner.GenerateMultipartUploadURLs(
		context.Background(),
		bucket,
		req.Key,
		req.PartCount,
	)
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "failed to generate multipart URLs")
		return
	}
	
	// 记录对象元数据
	if err := h.storage.RecordObject(req.Key, bucket.Config.Name, req.Size, nil); err != nil {
		log.Printf("Failed to record object metadata: %v", err)
	}
	
	// 更新存储桶使用量（预估）
	bucket.UpdateUsedSize(req.Size)
	
	h.sendJSON(w, http.StatusOK, multipartURLs)
}

// 列出对象
func (h *Handler) handleListObjects(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	bucketName := r.URL.Query().Get("bucket")
	marker := r.URL.Query().Get("marker")
	limitStr := r.URL.Query().Get("limit")
	
	limit := 100
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	
	// 调用更新后的ListObjects方法，传入所有必需的参数
	objects, err := h.storage.ListObjects(bucketName, prefix, marker, limit)
	if err != nil {
		h.sendError(w, http.StatusInternalServerError, "failed to list objects")
		return
	}
	
	h.sendJSON(w, http.StatusOK, map[string]interface{}{
		"objects": objects,
		"count":   len(objects),
	})
}

// 获取对象信息
func (h *Handler) handleGetObjectInfo(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]
	
	info, err := h.storage.GetObjectInfo(key)
	if err != nil {
		h.sendError(w, http.StatusNotFound, "object not found")
		return
	}
	
	h.sendJSON(w, http.StatusOK, info)
}

// 删除对象（只删除元数据记录）
func (h *Handler) handleDeleteObject(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]
	
	// 获取对象信息以更新存储桶使用量
	info, err := h.storage.GetObjectInfo(key)
	if err != nil {
		h.sendError(w, http.StatusNotFound, "object not found")
		return
	}
	
	// 更新存储桶使用量
	if bucket, ok := h.bucketManager.GetBucket(info.BucketName); ok {
		bucket.UpdateUsedSize(-info.Size)
	}
	
	// 删除元数据记录
	if err := h.storage.DeleteObject(key); err != nil {
		h.sendError(w, http.StatusInternalServerError, "failed to delete object")
		return
	}
	
	h.sendJSON(w, http.StatusOK, map[string]string{
		"message": "object deleted successfully",
	})
}

// 发送JSON响应
func (h *Handler) sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// 发送错误响应
func (h *Handler) sendError(w http.ResponseWriter, status int, message string) {
	h.sendJSON(w, status, map[string]string{
		"error": message,
	})
}
