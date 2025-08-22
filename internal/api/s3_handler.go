package api

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
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
	
	// Object operations
	router.HandleFunc("/{bucket}/{key:.*}", h.handleObjectOperations).Methods("GET", "HEAD", "PUT", "DELETE")
	
	// Multipart upload operations
	router.HandleFunc("/{bucket}/{key:.*}", h.handleMultipartUpload).Methods("POST").Queries("uploads", "")
	router.HandleFunc("/{bucket}/{key:.*}", h.handleListMultipartUploads).Methods("GET").Queries("uploads", "")
	router.HandleFunc("/{bucket}/{key:.*}", h.handleListMultipartParts).Methods("GET").Queries("uploadId", "")
	router.HandleFunc("/{bucket}/{key:.*}", h.handleCompleteMultipartUpload).Methods("POST").Queries("uploadId", "")
	router.HandleFunc("/{bucket}/{key:.*}", h.handleAbortMultipartUpload).Methods("DELETE").Queries("uploadId", "")
	
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
	XMLName   xml.Name `xml:"InitiateMultipartUploadResult"`
	Xmlns     string   `xml:"xmlns,attr"`
	Bucket    string   `xml:"Bucket"`
	Key       string   `xml:"Key"`
	UploadID  string   `xml:"UploadId"`
}

type ListMultipartUploadsResult struct {
	XMLName          xml.Name              `xml:"ListMultipartUploadsResult"`
	Xmlns            string                `xml:"xmlns,attr"`
	Bucket           string                `xml:"Bucket"`
	KeyMarker        string                `xml:"KeyMarker"`
	UploadIdMarker   string                `xml:"UploadIdMarker"`
	NextKeyMarker    string                `xml:"NextKeyMarker"`
	NextUploadIdMarker string              `xml:"NextUploadIdMarker"`
	MaxUploads      int                   `xml:"MaxUploads"`
	IsTruncated     bool                  `xml:"IsTruncated"`
	Uploads         []Upload              `xml:"Upload"`
	CommonPrefixes   []CommonPrefix        `xml:"CommonPrefixes,omitempty"`
}

type Upload struct {
	Key       string    `xml:"Key"`
	UploadID  string    `xml:"UploadId"`
	Initiator Owner      `xml:"Initiator"`
	Owner     Owner      `xml:"Owner"`
	StorageClass string   `xml:"StorageClass"`
	Initiated time.Time `xml:"Initiated"`
}

type ListPartsResult struct {
	XMLName          xml.Name `xml:"ListPartsResult"`
	Xmlns            string   `xml:"xmlns,attr"`
	Bucket           string   `xml:"Bucket"`
	Key              string   `xml:"Key"`
	UploadID         string   `xml:"UploadId"`
	PartNumberMarker int      `xml:"PartNumberMarker"`
	NextPartNumberMarker int   `xml:"NextPartNumberMarker"`
	MaxParts         int      `xml:"MaxParts"`
	IsTruncated      bool     `xml:"IsTruncated"`
	Parts            []Part   `xml:"Part"`
}

type Part struct {
	PartNumber   int       `xml:"PartNumber"`
	LastModified time.Time `xml:"LastModified"`
	ETag        string    `xml:"ETag"`
	Size        int64     `xml:"Size"`
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
		if b.IsAvailable() {
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
	if _, ok := h.bucketManager.GetBucket(bucketName); !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}
	
	// 解析查询参数
	prefix := r.URL.Query().Get("prefix")
	marker := r.URL.Query().Get("marker")
	maxKeysStr := r.URL.Query().Get("max-keys")
	delimiter := r.URL.Query().Get("delimiter")
	
	maxKeys := 1000
	if maxKeysStr != "" {
		if mk, err := strconv.Atoi(maxKeysStr); err == nil {
			maxKeys = mk
		}
	}
	
	// 从存储中获取对象列表
	objects, err := h.storage.ListObjects(bucketName, prefix, marker, maxKeys)
	if err != nil {
		h.sendS3Error(w, "InternalError", "Internal server error", bucketName)
		return
	}
	
	result := ListBucketResult{
		Xmlns:       "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:        bucketName,
		Prefix:      prefix,
		Marker:      marker,
		MaxKeys:     maxKeys,
		IsTruncated: false, // 简化实现
		Contents:    make([]ObjectInfo, 0),
	}
	
	// 处理分隔符逻辑
	if delimiter != "" {
		// 简化的分隔符处理
		result.CommonPrefixes = make([]CommonPrefix, 0)
	}
	
	for _, obj := range objects {
		result.Contents = append(result.Contents, ObjectInfo{
			Key:          obj.Key,
			LastModified: obj.UpdatedAt,
			ETag:         fmt.Sprintf("\"%x\"", obj.ID), // 简化的ETag
			Size:         obj.Size,
			StorageClass: "STANDARD",
			Owner: Owner{
				ID:          "s3-balance",
				DisplayName: "S3 Balance Service",
			},
		})
	}
	
	h.sendXMLResponse(w, http.StatusOK, result)
}

// handleHeadBucket 检查存储桶是否存在
func (h *S3Handler) handleHeadBucket(w http.ResponseWriter, r *http.Request, bucketName string) {
	if _, ok := h.bucketManager.GetBucket(bucketName); !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	
	w.WriteHeader(http.StatusOK)
}

// handleCreateBucket 创建存储桶（虚拟实现）
func (h *S3Handler) handleCreateBucket(w http.ResponseWriter, r *http.Request, bucketName string) {
	// 在负载均衡场景下，不真正创建bucket，只返回成功
	// 实际的bucket应该在配置中预先定义
	w.Header().Set("Location", "/" + bucketName)
	w.WriteHeader(http.StatusOK)
}

// handleDeleteBucket 删除存储桶（虚拟实现）
func (h *S3Handler) handleDeleteBucket(w http.ResponseWriter, r *http.Request, bucketName string) {
	// 在负载均衡场景下，不真正删除bucket
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
	// 查找对象所在的实际存储桶
	actualBucketName, err := h.storage.FindObjectBucket(key)
	if err != nil {
		h.sendS3Error(w, "NoSuchKey", "The specified key does not exist", key)
		return
	}
	
	bucket, ok := h.bucketManager.GetBucket(actualBucketName)
	if !ok {
		h.sendS3Error(w, "InternalError", "Internal server error", bucketName)
		return
	}
	
	// 生成预签名下载URL
	downloadInfo, err := h.presigner.GenerateDownloadURL(
		context.Background(),
		bucket,
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
	// 从存储中获取对象信息
	obj, err := h.storage.GetObjectInfo(key)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Last-Modified", obj.UpdatedAt.Format(http.TimeFormat))
	w.Header().Set("ETag", fmt.Sprintf("\"%x\"", obj.ID))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
}

// handlePutObject 上传对象（默认使用预签名URL重定向）
func (h *S3Handler) handlePutObject(w http.ResponseWriter, r *http.Request, bucketName string, key string) {
	// 获取内容长度
	contentLength := r.ContentLength
	if contentLength < 0 {
		h.sendS3Error(w, "MissingContentLength", "Content-Length header is required", key)
		return
	}
	
	// 选择目标存储桶
	targetBucket, err := h.balancer.SelectBucket(key, contentLength)
	if err != nil {
		h.sendS3Error(w, "InsufficientStorage", "No bucket has enough space", key)
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
		h.sendS3Error(w, "InternalError", "Failed to generate upload URL", key)
		return
	}
	
	// 默认使用预签名重定向模式，只有明确指定时才使用代理模式
	if r.URL.Query().Get("proxy") == "true" {
		// 代理模式：读取请求体并上传到预签名URL
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
			h.sendS3Error(w, "InternalError", "Upload failed", key)
		}
	} else {
		// 重定向模式：返回307临时重定向让客户端直接上传（默认）
		w.Header().Set("Location", uploadInfo.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}
}

// handleDeleteObject 删除对象
func (h *S3Handler) handleDeleteObject(w http.ResponseWriter, r *http.Request, bucketName string, key string) {
	// 查找对象所在的实际存储桶
	actualBucketName, err := h.storage.FindObjectBucket(key)
	if err != nil {
		// 对象不存在，S3规范要求返回204
		w.WriteHeader(http.StatusNoContent)
		return
	}
	
	bucket, ok := h.bucketManager.GetBucket(actualBucketName)
	if !ok {
		h.sendS3Error(w, "InternalError", "Internal server error", bucketName)
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
	
	// S3规范要求删除操作总是返回204
	w.WriteHeader(http.StatusNoContent)
}

// handleMultipartUpload 初始化分片上传
func (h *S3Handler) handleMultipartUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]
	
	// 选择目标存储桶
	targetBucket, err := h.balancer.SelectBucket(key, 0) // 分片上传时不检查空间
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to select bucket for upload", key)
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
	
	result := InitiateMultipartUploadResult{
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:   targetBucket.Config.Name,
		Key:      key,
		UploadID: *createResp.UploadId,
	}
	
	h.sendXMLResponse(w, http.StatusOK, result)
}

// handleListMultipartUploads 列出分片上传
func (h *S3Handler) handleListMultipartUploads(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]
	
	// 检查bucket是否存在
	if _, ok := h.bucketManager.GetBucket(bucketName); !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}
	
	// 简化实现：返回空列表
	result := ListMultipartUploadsResult{
		Xmlns:       "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:      bucketName,
		KeyMarker:   key,
		MaxUploads:  1000,
		IsTruncated: false,
		Uploads:     make([]Upload, 0),
	}
	
	h.sendXMLResponse(w, http.StatusOK, result)
}

// handleListMultipartParts 列出分片上传的分片
func (h *S3Handler) handleListMultipartParts(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]
	uploadID := r.URL.Query().Get("uploadId")
	
	// 检查bucket是否存在
	if _, ok := h.bucketManager.GetBucket(bucketName); !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}
	
	// 简化实现：返回空列表
	result := ListPartsResult{
		Xmlns:             "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:            bucketName,
		Key:               key,
		UploadID:          uploadID,
		PartNumberMarker:  0,
		MaxParts:          1000,
		IsTruncated:       false,
		Parts:             make([]Part, 0),
	}
	
	h.sendXMLResponse(w, http.StatusOK, result)
}

// handleCompleteMultipartUpload 完成分片上传
func (h *S3Handler) handleCompleteMultipartUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]
	uploadID := r.URL.Query().Get("uploadId")
	
	// 查找对象所在的实际存储桶（简化实现，使用配置的bucket）
	bucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}
	
	// 解析请求体以获取分片列表
	var completeReq CompleteMultipartUpload
	body, _ := io.ReadAll(r.Body)
	xml.Unmarshal(body, &completeReq)
	
	// 完成分片上传
	ctx := context.Background()
	var parts []types.CompletedPart
	for _, part := range completeReq.Parts {
		parts = append(parts, types.CompletedPart{
			ETag:       aws.String(part.ETag),
			PartNumber: aws.Int32(int32(part.PartNumber)),
		})
	}
	
	completeResp, err := bucket.Client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucket.Config.Name),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: parts,
		},
	})
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to complete multipart upload", key)
		return
	}
	
	result := CompleteMultipartUploadResult{
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
		Location:  "/" + bucket.Config.Name + "/" + key,
		Bucket:   bucket.Config.Name,
		Key:      key,
		ETag:     *completeResp.ETag,
	}
	
	// 记录对象元数据（简化：假设总大小）
	h.storage.RecordObject(key, bucket.Config.Name, 0, nil)
	
	h.sendXMLResponse(w, http.StatusOK, result)
}

// handleAbortMultipartUpload 中止分片上传
func (h *S3Handler) handleAbortMultipartUpload(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucketName := vars["bucket"]
	key := vars["key"]
	uploadID := r.URL.Query().Get("uploadId")
	
	// 查找对象所在的实际存储桶（简化实现，使用配置的bucket）
	bucket, ok := h.bucketManager.GetBucket(bucketName)
	if !ok {
		h.sendS3Error(w, "NoSuchBucket", "The specified bucket does not exist", bucketName)
		return
	}
	
	// 中止分片上传
	ctx := context.Background()
	_, err := bucket.Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucket.Config.Name),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		h.sendS3Error(w, "InternalError", "Failed to abort multipart upload", key)
		return
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
