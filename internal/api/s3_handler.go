package api

import (
	"github.com/DullJZ/s3-balance/internal/balancer"
	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/DullJZ/s3-balance/internal/metrics"
	"github.com/DullJZ/s3-balance/internal/storage"
	"github.com/DullJZ/s3-balance/pkg/presigner"
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
	metrics       *metrics.Metrics
	proxyMode     bool
	authRequired  bool
	virtualHost   bool
}

// NewS3Handler 创建新的S3兼容API处理器
func NewS3Handler(
	bucketManager *bucket.Manager,
	balancer *balancer.Balancer,
	presigner *presigner.Presigner,
	storage *storage.Service,
	accessKey string,
	secretKey string,
	metrics *metrics.Metrics,
	proxyMode bool,
	authRequired bool,
	virtualHost bool,
) *S3Handler {
	return &S3Handler{
		bucketManager: bucketManager,
		balancer:      balancer,
		presigner:     presigner,
		storage:       storage,
		accessKey:     accessKey,
		secretKey:     secretKey,
		metrics:       metrics,
		proxyMode:     proxyMode,
		authRequired:  authRequired,
		virtualHost:   virtualHost,
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
	router.Use(h.virtualHostMiddleware)
	router.Use(h.authMiddleware)
}
