package api

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DullJZ/s3-balance/internal/balancer"
	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/DullJZ/s3-balance/internal/storage"
	"github.com/DullJZ/s3-balance/pkg/presigner"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gorilla/mux"
)

// S3Handler S3兼容的API处理器
type S3Handler struct {
	bucketManager *bucket.Manager
	balancer      *balancer.Balancer
	presigner     *presigner.Presigner
	storage       *storage.Service
	accessKey     string
	secretKey     string
}

// NewS3Handler 创建新的S3兼容API处理器
func NewS3Handler(
	bucketManager *bucket.Manager,
	balancer *balancer.Balancer,
	presigner *presigner.Presigner,
	storage *storage.Service,
	accessKey string,
	secretKey string,
) *S3Handler {
	return &S3Handler{
		bucketManager: bucketManager,
		balancer:      balancer,
		presigner:     presigner,
		storage:       storage,
		accessKey:     accessKey,
		secretKey:     secretKey,
	}
}

// RegisterS3Routes 注册S3兼容的路由
func (h *S3Handler) RegisterS3Routes(router *mux.Router) {
	// Service operations
	router.HandleFunc("/", h.handleListBuckets).Methods("GET")

	// Bucket operations
	router.HandleFunc("/{bucket}", h.handleBucketOperations).Methods("GET", "HEAD", "PUT", "DELETE")

	// Multipart upload operations - must be registered before generic object operations
	// Upload part handler - must handle PUT with partNumber and uploadId
	router.HandleFunc("/{bucket}/{key:.*}", h.handleUploadPart).Methods("PUT").Queries("partNumber", "{partNumber:[0-9]+}", "uploadId", "{uploadId}")
	// Initiate multipart upload
	router.HandleFunc("/{bucket}/{key:.*}", h.handleMultipartUpload).Methods("POST").Queries("uploads", "")
	// List multipart uploads
	router.HandleFunc("/{bucket}/{key:.*}", h.handleListMultipartUploads).Methods("GET").Queries("uploads", "")
	// List parts
	router.HandleFunc("/{bucket}/{key:.*}", h.handleListMultipartParts).Methods("GET").Queries("uploadId", "{uploadId}")
	// Complete multipart upload
	router.HandleFunc("/{bucket}/{key:.*}", h.handleCompleteMultipartUpload).Methods("POST").Queries("uploadId", "{uploadId}")
	// Abort multipart upload
	router.HandleFunc("/{bucket}/{key:.*}", h.handleAbortMultipartUpload).Methods("DELETE").Queries("uploadId", "{uploadId}")

	// Object operations - must be registered after multipart operations to avoid conflicts
	router.HandleFunc("/{bucket}/{key:.*}", h.handleObjectOperations).Methods("GET", "HEAD", "PUT", "DELETE")

	// 添加认证中间件
	router.Use(h.s3AuthMiddleware)
}

// S3 XML响应结构体定义
type ListBucketsResult struct {
	XMLName xml.Name `xml:"ListAllMyBucketsResult"`
	Xmlns   string   `xml:"xmlns,attr"`
	Owner   Owner    `xml:"Owner"`
	Buckets Buckets  `xml:"Buckets"`
}

type Owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type Buckets struct {
	Bucket []BucketInfo `xml:"Bucket"`
}

type BucketInfo struct {
	Name         string    `xml:"Name"`
	CreationDate time.Time `xml:"CreationDate"`
}

type ListBucketResult struct {
	XMLName        xml.Name       `xml:"ListBucketResult"`
	Xmlns          string         `xml:"xmlns,attr"`
	Name           string         `xml:"Name"`
	Prefix         string         `xml:"Prefix"`
	Marker         string         `xml:"Marker"`
	MaxKeys        int            `xml:"MaxKeys"`
	IsTruncated    bool           `xml:"IsTruncated"`
	Contents       []ObjectInfo   `xml:"Contents"`
	CommonPrefixes []CommonPrefix `xml:"CommonPrefixes,omitempty"`
}

type ObjectInfo struct {
	Key          string    `xml:"Key"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
	Owner        Owner     `xml:"Owner"`
}

type CommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

type InitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Xmlns    string   `xml:"xmlns,attr"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

type ListMultipartUploadsResult struct {
	XMLName            xml.Name       `xml:"ListMultipartUploadsResult"`
	Xmlns              string         `xml:"xmlns,attr"`
	Bucket             string         `xml:"Bucket"`
	KeyMarker          string         `xml:"KeyMarker"`
	UploadIdMarker     string         `xml:"UploadIdMarker"`
	NextKeyMarker      string         `xml:"NextKeyMarker"`
	NextUploadIdMarker string         `xml:"NextUploadIdMarker"`
	MaxUploads         int            `xml:"MaxUploads"`
	IsTruncated        bool           `xml:"IsTruncated"`
	Uploads            []Upload       `xml:"Upload"`
	CommonPrefixes     []CommonPrefix `xml:"CommonPrefixes,omitempty"`
}

type Upload struct {
	Key          string    `xml:"Key"`
	UploadID     string    `xml:"UploadId"`
	Initiator    Owner     `xml:"Initiator"`
	Owner        Owner     `xml:"Owner"`
	StorageClass string    `xml:"StorageClass"`
	Initiated    time.Time `xml:"Initiated"`
}

type ListPartsResult struct {
	XMLName              xml.Name `xml:"ListPartsResult"`
	Xmlns                string   `xml:"xmlns,attr"`
	Bucket               string   `xml:"Bucket"`
	Key                  string   `xml:"Key"`
	UploadID             string   `xml:"UploadId"`
	PartNumberMarker     int      `xml:"PartNumberMarker"`
	NextPartNumberMarker int      `xml:"NextPartNumberMarker"`
	MaxParts             int      `xml:"MaxParts"`
	IsTruncated          bool     `xml:"IsTruncated"`
	Parts                []Part   `xml:"Part"`
}

type Part struct {
	PartNumber   int       `xml:"PartNumber"`
	LastModified time.Time `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
}

type CompleteMultipartUpload struct {
	XMLName xml.Name `xml:"CompleteMultipartUpload"`
	Parts   []Part   `xml:"Part"`
}

type CompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Xmlns    string   `xml:"xmlns,attr"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

type ErrorResponse struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource"`
	RequestID string   `xml:"RequestId"`
}

// s3AuthMiddleware S3认证中间件（简化版）
func (h *S3Handler) s3AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 简化的认证实现，实际应该验证AWS Signature
		// 这里只做基本的header检查
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			// 允许匿名访问（用于测试）
			// 在生产环境中应该要求认证
		}

		next.ServeHTTP(w, r)
	})
}

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

// handleObjectOperations 处理对象相关操作
func (h *S3Handler) handleObjectOperations(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]

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

	// 默认使用预签名重定向模式，只有明确指定时才使用代理模式
	if r.URL.Query().Get("proxy") == "true" {
		// 代理模式：服务器下载内容并返回给客户端
		resp, err := http.Get(downloadInfo.URL)
		if err != nil {
			h.sendS3Error(w, "InternalError", "Failed to fetch object", key)
			return
		}
		defer resp.Body.Close()

		// 复制响应头
		for k, v := range resp.Header {
			w.Header()[k] = v
		}

		// 复制响应体
		io.Copy(w, resp.Body)
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

// setObjectHeaders 设置对象响应头
func (h *S3Handler) setObjectHeaders(w http.ResponseWriter, obj *storage.Object) {
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Last-Modified", obj.UpdatedAt.Format(http.TimeFormat))
	w.Header().Set("ETag", fmt.Sprintf("\"%x\"", obj.ID))
	if obj.ContentType != "" {
		w.Header().Set("Content-Type", obj.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
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

	var bucket *bucket.BucketInfo
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
		bucket, ok = h.bucketManager.GetBucket(mapping.RealBucketName)
		if !ok {
			h.sendS3Error(w, "InternalError", "Mapped real bucket not found", key)
			return
		}
	} else {
		// 如果不是虚拟存储桶，拒绝客户端对真实存储桶的直接DELETE操作
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 生成预签名删除URL
	deleteInfo, err := h.presigner.GenerateDeleteURL(
		context.Background(),
		bucket,
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
		
		// 更新上传会话的分片数
		session, err := h.storage.GetUploadSession(uploadID)
		if err != nil {
			log.Printf("Failed to get upload session for uploadID %s: %v", uploadID, err)
		} else {
			// 更新已完成的分片数
			if err := h.storage.UpdateUploadSession(uploadID, session.CompletedParts+1, "pending"); err != nil {
				log.Printf("Failed to update upload session for uploadID %s: %v", uploadID, err)
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
	if err := h.storage.RecordUploadSession(uploadID, key, targetBucket.Config.Name, 0, 0); err != nil {
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
		Xmlns:       "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:      bucketName,
		KeyMarker:   keyMarker,
		UploadIdMarker: uploadIdMarker,
		MaxUploads:  maxUploads,
		IsTruncated: isTruncated,
		Uploads:     allUploads,
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

	// 完成分片上传
	ctx := context.Background()
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
		h.sendS3Error(w, "InternalError", "Failed to complete multipart upload", key)
		return
	}

	result := CompleteMultipartUploadResult{
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
		Location: "/" + bucketName + "/" + key, // 返回虚拟存储桶路径
		Bucket:   bucketName, // 返回虚拟存储桶名称
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

// sendXMLResponse 发送XML响应
func (h *S3Handler) sendXMLResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(statusCode)

	encoder := xml.NewEncoder(w)
	encoder.Indent("", "  ")

	// 写入XML声明
	w.Write([]byte(xml.Header))

	if err := encoder.Encode(data); err != nil {
		// 如果编码失败，记录错误
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// sendS3Error 发送S3错误响应
func (h *S3Handler) sendS3Error(w http.ResponseWriter, code string, message string, resource string) {
	errorResp := ErrorResponse{
		Code:      code,
		Message:   message,
		Resource:  resource,
		RequestID: fmt.Sprintf("%d", time.Now().UnixNano()),
	}

	statusCode := http.StatusBadRequest
	switch code {
	case "NoSuchBucket", "NoSuchKey":
		statusCode = http.StatusNotFound
	case "BucketAlreadyExists":
		statusCode = http.StatusConflict
	case "InvalidAccessKeyId", "SignatureDoesNotMatch":
		statusCode = http.StatusForbidden
	case "InternalError":
		statusCode = http.StatusInternalServerError
	case "InsufficientStorage":
		statusCode = http.StatusInsufficientStorage
	}

	h.sendXMLResponse(w, statusCode, errorResp)
}

// 辅助函数：解析S3路径
func parseS3Path(requestPath string) (bucket string, key string) {
	requestPath = strings.TrimPrefix(requestPath, "/")
	parts := strings.SplitN(requestPath, "/", 2)

	if len(parts) > 0 {
		bucket = parts[0]
	}
	if len(parts) > 1 {
		key = parts[1]
	}

	return bucket, key
}

// 辅助函数：URL编码/解码
func urlEncodePath(p string) string {
	return strings.ReplaceAll(url.QueryEscape(p), "+", "%20")
}

func urlDecodePath(p string) string {
	decoded, _ := url.QueryUnescape(p)
	return decoded
}
