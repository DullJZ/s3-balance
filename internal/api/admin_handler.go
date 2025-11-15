package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/DullJZ/s3-balance/internal/balancer"
	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/DullJZ/s3-balance/internal/config"
	"github.com/gorilla/mux"
)

// AdminHandler 管理API处理器
type AdminHandler struct {
	bucketManager *bucket.Manager
	balancer      *balancer.Balancer
	config        *config.Config
	configManager *config.Manager
}

// NewAdminHandler 创建新的管理API处理器
func NewAdminHandler(
	bucketManager *bucket.Manager,
	balancer *balancer.Balancer,
	cfg *config.Config,
	configManager *config.Manager,
) *AdminHandler {
	return &AdminHandler{
		bucketManager: bucketManager,
		balancer:      balancer,
		config:        cfg,
		configManager: configManager,
	}
}

// BucketResponse 存储桶响应结构
type BucketResponse struct {
	Name            string    `json:"name"`
	Endpoint        string    `json:"endpoint"`
	Region          string    `json:"region"`
	MaxSize         string    `json:"max_size"`
	MaxSizeBytes    int64     `json:"max_size_bytes"`
	UsedSize        int64     `json:"used_size"`
	AvailableSize   int64     `json:"available_size"`
	UsagePercent    float64   `json:"usage_percent"`
	Weight          int       `json:"weight"`
	Enabled         bool      `json:"enabled"`
	Available       bool      `json:"available"`
	Virtual         bool      `json:"virtual"`
	LastChecked     time.Time `json:"last_checked"`
	OperationCountA int64     `json:"operation_count_a"`
	OperationCountB int64     `json:"operation_count_b"`
	OperationLimits struct {
		TypeA int `json:"type_a"`
		TypeB int `json:"type_b"`
	} `json:"operation_limits"`
}

// BucketsListResponse 存储桶列表响应结构
type BucketsListResponse struct {
	Total   int                `json:"total"`
	Buckets []BucketResponse   `json:"buckets"`
}

// HealthResponse 健康状态响应结构
type HealthResponse struct {
	Status          string    `json:"status"`
	Timestamp       time.Time `json:"timestamp"`
	LoadBalancer    string    `json:"load_balancer_strategy"`
	TotalBuckets    int       `json:"total_buckets"`
	AvailableBuckets int      `json:"available_buckets"`
	Database        string    `json:"database_type"`
}

// RegisterRoutes 注册管理API路由
// 注意: router 参数应该是已经带有 /api 前缀的子路由器
func (h *AdminHandler) RegisterRoutes(router *mux.Router) {
	// 注册路由，同时支持 OPTIONS 方法用于 CORS 预检
	router.HandleFunc("/buckets", h.ListBuckets).Methods(http.MethodGet, http.MethodOptions)
	router.HandleFunc("/buckets/{name}", h.GetBucketDetail).Methods(http.MethodGet, http.MethodOptions)
	router.HandleFunc("/health", h.GetHealth).Methods(http.MethodGet, http.MethodOptions)
	router.HandleFunc("/config", h.GetConfig).Methods(http.MethodGet, http.MethodOptions)
	router.HandleFunc("/config", h.UpdateConfig).Methods(http.MethodPost, http.MethodOptions)
}

// ListBuckets 获取存储桶列表
func (h *AdminHandler) ListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets := h.bucketManager.GetAllBuckets()

	response := BucketsListResponse{
		Total:   len(buckets),
		Buckets: make([]BucketResponse, 0, len(buckets)),
	}

	for _, b := range buckets {
		bucketResp := h.convertBucketInfo(b)
		response.Buckets = append(response.Buckets, bucketResp)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetBucketDetail 获取存储桶详情
func (h *AdminHandler) GetBucketDetail(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	bucketInfo, exists := h.bucketManager.GetBucket(name)
	if !exists {
		http.Error(w, `{"error": "bucket not found"}`, http.StatusNotFound)
		return
	}

	response := h.convertBucketInfo(bucketInfo)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetHealth 获取系统健康状态
func (h *AdminHandler) GetHealth(w http.ResponseWriter, r *http.Request) {
	buckets := h.bucketManager.GetAllBuckets()
	availableBuckets := h.bucketManager.GetAvailableBuckets()

	status := "healthy"
	if len(availableBuckets) == 0 {
		status = "unhealthy"
	} else if len(availableBuckets) < len(buckets)/2 {
		status = "degraded"
	}

	response := HealthResponse{
		Status:           status,
		Timestamp:        time.Now(),
		LoadBalancer:     h.config.Balancer.Strategy,
		TotalBuckets:     len(buckets),
		AvailableBuckets: len(availableBuckets),
		Database:         h.config.Database.Type,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetConfig 获取当前配置
func (h *AdminHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	if h.configManager == nil {
		http.Error(w, `{"error": "config manager not available"}`, http.StatusInternalServerError)
		return
	}

	currentConfig := h.configManager.GetConfig()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(currentConfig)
}

// UpdateConfig 更新配置
func (h *AdminHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	if h.configManager == nil {
		http.Error(w, `{"error": "config manager not available"}`, http.StatusInternalServerError)
		return
	}

	// 解析请求体
	var newConfig config.Config
	if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
		http.Error(w, `{"error": "invalid JSON format: `+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	// 设置默认值
	newConfig.SetDefaults()

	// 更新配置
	if err := h.configManager.UpdateConfig(&newConfig); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":   "validation failed",
			"message": err.Error(),
		})
		return
	}

	// 返回成功响应
	response := map[string]interface{}{
		"success": true,
		"message": "Configuration updated successfully. Changes will take effect automatically.",
		"config":  &newConfig,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// convertBucketInfo 转换BucketInfo为BucketResponse
func (h *AdminHandler) convertBucketInfo(b *bucket.BucketInfo) BucketResponse {
	resp := BucketResponse{
		Name:            b.Config.Name,
		Endpoint:        b.Config.Endpoint,
		Region:          b.Config.Region,
		MaxSize:         b.Config.MaxSize,
		MaxSizeBytes:    b.Config.MaxSizeBytes,
		UsedSize:        b.UsedSize,
		Weight:          b.Config.Weight,
		Enabled:         b.Config.Enabled,
		Available:       b.Available,
		Virtual:         b.Config.Virtual,
		LastChecked:     b.LastChecked,
		OperationCountA: b.GetOperationCount(bucket.OperationTypeA),
		OperationCountB: b.GetOperationCount(bucket.OperationTypeB),
	}

	resp.OperationLimits.TypeA = b.Config.OperationLimits.TypeA
	resp.OperationLimits.TypeB = b.Config.OperationLimits.TypeB

	// 计算可用空间
	if b.Config.MaxSizeBytes > 0 {
		resp.AvailableSize = b.Config.MaxSizeBytes - b.UsedSize
		if resp.AvailableSize < 0 {
			resp.AvailableSize = 0
		}
		// 计算使用百分比
		resp.UsagePercent = float64(b.UsedSize) / float64(b.Config.MaxSizeBytes) * 100
	} else {
		resp.AvailableSize = -1 // -1 表示无限制
		resp.UsagePercent = 0
	}

	return resp
}
