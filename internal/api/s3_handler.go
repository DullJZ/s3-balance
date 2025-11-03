package api

import (
	"sync/atomic"

	"github.com/DullJZ/s3-balance/internal/balancer"
	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/DullJZ/s3-balance/internal/config"
	"github.com/DullJZ/s3-balance/internal/metrics"
	"github.com/DullJZ/s3-balance/internal/middleware"
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
	metrics       *metrics.Metrics
	settings      atomic.Value
}

type handlerSettings struct {
	accessKey     string
	secretKey     string
	proxyMode     bool
	authRequired  bool
	virtualHost   bool
	signatureHost string
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
	signatureHost string,

) *S3Handler {
	handler := &S3Handler{
		bucketManager: bucketManager,
		balancer:      balancer,
		presigner:     presigner,
		storage:       storage,
		metrics:       metrics,
	}
	handler.initSettings(accessKey, secretKey, proxyMode, authRequired, virtualHost, signatureHost)
	return handler
}

func (h *S3Handler) initSettings(accessKey, secretKey string, proxyMode, authRequired, virtualHost bool, signatureHost string) {
	h.settings.Store(handlerSettings{
		accessKey:     accessKey,
		secretKey:     secretKey,
		proxyMode:     proxyMode,
		authRequired:  authRequired,
		virtualHost:   virtualHost,
		signatureHost: signatureHost,
	})
}

// RegisterS3Routes 注册S3兼容的路由
func (h *S3Handler) RegisterS3Routes(router *mux.Router) {
	// 公共路由（不需要认证）
	router.HandleFunc("/", h.handleListBuckets).Methods("GET")

	// 带认证/虚拟主机的路由
	protected := router.NewRoute().PathPrefix("/{bucket}").Subrouter()
	// 注意：不使用 StrictSlash(true) 以避免 301 重定向，兼容WinSCP

	// Bucket operations
	protected.HandleFunc("", h.handleBucketOperations).Methods("GET", "HEAD", "PUT", "DELETE")
	protected.HandleFunc("/", h.handleBucketOperations).Methods("GET", "HEAD", "PUT", "DELETE")

	// Multipart upload operations - must be registered before generic object operations
	protected.HandleFunc("/{key:.*}", h.handleUploadPart).Methods("PUT").Queries("partNumber", "{partNumber:[0-9]+}", "uploadId", "{uploadId}")
	protected.HandleFunc("/{key:.*}", h.handleMultipartUpload).Methods("POST").Queries("uploads", "")
	protected.HandleFunc("/{key:.*}", h.handleListMultipartUploads).Methods("GET").Queries("uploads", "")
	protected.HandleFunc("/{key:.*}", h.handleListMultipartParts).Methods("GET").Queries("uploadId", "{uploadId}")
	protected.HandleFunc("/{key:.*}", h.handleCompleteMultipartUpload).Methods("POST").Queries("uploadId", "{uploadId}")
	protected.HandleFunc("/{key:.*}", h.handleAbortMultipartUpload).Methods("DELETE").Queries("uploadId", "{uploadId}")

	// Object operations - must be registered after multipart operations to avoid conflicts
	protected.HandleFunc("/{key:.*}", h.handleObjectOperations).Methods("GET", "HEAD", "PUT", "DELETE")

	// 添加中间件
	protected.Use(h.accessLogMiddleware)
	protected.Use(middleware.VirtualHost(middleware.VirtualHostConfig{
		Enabled: h.virtualHostEnabled,
		BucketExists: func(name string) bool {
			_, ok := h.bucketManager.GetBucket(name)
			return ok
		},
	}))
	protected.Use(middleware.S3Signature(middleware.S3SignatureConfig{
		Required:      h.authRequired,
		Credentials:   h.credentials,
		OnError:       h.handleAuthError,
		SignatureHost: h.signatureHost,
	}))
}

func (h *S3Handler) loadSettings() handlerSettings {
	if v := h.settings.Load(); v != nil {
		return v.(handlerSettings)
	}
	return handlerSettings{}
}

func (h *S3Handler) virtualHostEnabled() bool {
	return h.loadSettings().virtualHost
}

func (h *S3Handler) authRequired() bool {
	return h.loadSettings().authRequired
}

func (h *S3Handler) credentials() (string, string) {
	s := h.loadSettings()
	return s.accessKey, s.secretKey
}

func (h *S3Handler) proxyModeEnabled() bool {
	return h.loadSettings().proxyMode
}

func (h *S3Handler) signatureHost() string {
	return h.loadSettings().signatureHost
}

func (h *S3Handler) UpdateS3APIConfig(cfg *config.S3APIConfig) {
	if cfg == nil {
		return
	}
	h.settings.Store(handlerSettings{
		accessKey:     cfg.AccessKey,
		secretKey:     cfg.SecretKey,
		proxyMode:     cfg.ProxyMode,
		authRequired:  cfg.AuthRequired,
		virtualHost:   cfg.VirtualHost,
		signatureHost: cfg.Host,
	})
}
