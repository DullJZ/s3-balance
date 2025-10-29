package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/DullJZ/s3-balance/internal/storage"
	"github.com/gorilla/mux"
)

// StatsHandler 统计数据处理器
type StatsHandler struct {
	storage *storage.Service
}

// NewStatsHandler 创建统计处理器
func NewStatsHandler(storage *storage.Service) *StatsHandler {
	return &StatsHandler{
		storage: storage,
	}
}

// RegisterRoutes 注册统计API路由
func (h *StatsHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/api/stats/monthly", h.GetCurrentMonthStats).Methods("GET")
	router.HandleFunc("/api/stats/monthly/{year}/{month}", h.GetMonthlyStats).Methods("GET")
	router.HandleFunc("/api/stats/monthly/range", h.GetMonthlyStatsRange).Methods("GET")
	router.HandleFunc("/api/stats/bucket/{bucket}/history", h.GetBucketHistory).Methods("GET")
}

// MonthlyStatsResponse 月度统计响应
type MonthlyStatsResponse struct {
	Year   int                   `json:"year"`
	Month  int                   `json:"month"`
	Bucket string                `json:"bucket"`
	Stats  BucketOperationCounts `json:"stats"`
}

// BucketOperationCounts 存储桶操作计数
type BucketOperationCounts struct {
	OperationCountA int64 `json:"operation_count_a"`
	OperationCountB int64 `json:"operation_count_b"`
	Total           int64 `json:"total"`
}

// GetCurrentMonthStats 获取当前月份的统计
func (h *StatsHandler) GetCurrentMonthStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.storage.GetCurrentMonthStats()
	if err != nil {
		log.Printf("Failed to get current month stats: %v", err)
		http.Error(w, "Failed to fetch statistics", http.StatusInternalServerError)
		return
	}

	response := h.formatMonthlyStats(stats)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetMonthlyStats 获取指定月份的统计
func (h *StatsHandler) GetMonthlyStats(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	year, err := strconv.Atoi(vars["year"])
	if err != nil {
		http.Error(w, "Invalid year", http.StatusBadRequest)
		return
	}

	month, err := strconv.Atoi(vars["month"])
	if err != nil || month < 1 || month > 12 {
		http.Error(w, "Invalid month", http.StatusBadRequest)
		return
	}

	stats, err := h.storage.GetMonthlyStats(year, month)
	if err != nil {
		log.Printf("Failed to get monthly stats: %v", err)
		http.Error(w, "Failed to fetch statistics", http.StatusInternalServerError)
		return
	}

	response := h.formatMonthlyStats(stats)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetMonthlyStatsRange 获取时间范围内的统计
func (h *StatsHandler) GetMonthlyStatsRange(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	startYear, err := strconv.Atoi(query.Get("start_year"))
	if err != nil {
		http.Error(w, "Invalid start_year", http.StatusBadRequest)
		return
	}

	startMonth, err := strconv.Atoi(query.Get("start_month"))
	if err != nil || startMonth < 1 || startMonth > 12 {
		http.Error(w, "Invalid start_month", http.StatusBadRequest)
		return
	}

	endYear, err := strconv.Atoi(query.Get("end_year"))
	if err != nil {
		http.Error(w, "Invalid end_year", http.StatusBadRequest)
		return
	}

	endMonth, err := strconv.Atoi(query.Get("end_month"))
	if err != nil || endMonth < 1 || endMonth > 12 {
		http.Error(w, "Invalid end_month", http.StatusBadRequest)
		return
	}

	stats, err := h.storage.GetMonthlyStatsRange(startYear, startMonth, endYear, endMonth)
	if err != nil {
		log.Printf("Failed to get monthly stats range: %v", err)
		http.Error(w, "Failed to fetch statistics", http.StatusInternalServerError)
		return
	}

	response := h.formatMonthlyStats(stats)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// GetBucketHistory 获取指定存储桶的历史统计
func (h *StatsHandler) GetBucketHistory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucket := vars["bucket"]

	// 获取查询参数中的月份数，默认12个月
	months := 12
	if monthsStr := r.URL.Query().Get("months"); monthsStr != "" {
		if m, err := strconv.Atoi(monthsStr); err == nil && m > 0 {
			months = m
		}
	}

	stats, err := h.storage.GetBucketMonthlyHistory(bucket, months)
	if err != nil {
		log.Printf("Failed to get bucket history: %v", err)
		http.Error(w, "Failed to fetch statistics", http.StatusInternalServerError)
		return
	}

	response := h.formatMonthlyStats(stats)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// formatMonthlyStats 格式化月度统计数据
func (h *StatsHandler) formatMonthlyStats(stats []storage.BucketMonthlyStats) []MonthlyStatsResponse {
	result := make([]MonthlyStatsResponse, 0, len(stats))

	for _, stat := range stats {
		result = append(result, MonthlyStatsResponse{
			Year:   stat.Year,
			Month:  stat.Month,
			Bucket: stat.BucketName,
			Stats: BucketOperationCounts{
				OperationCountA: stat.OperationCountA,
				OperationCountB: stat.OperationCountB,
				Total:           stat.OperationCountA + stat.OperationCountB,
			},
		})
	}

	return result
}

// ArchiveCurrentMonth 手动触发归档当前月份（管理API）
func (h *StatsHandler) ArchiveCurrentMonth(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	year, month := now.Year(), int(now.Month())

	if err := h.storage.ArchiveMonthlyStats(year, month); err != nil {
		log.Printf("Failed to archive monthly stats: %v", err)
		http.Error(w, "Failed to archive statistics", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Monthly statistics archived successfully",
		"year":    strconv.Itoa(year),
		"month":   strconv.Itoa(month),
	})
}
