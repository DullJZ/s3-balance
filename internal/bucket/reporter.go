package bucket

import (
	"github.com/DullJZ/s3-balance/internal/health"
	"github.com/DullJZ/s3-balance/internal/metrics"
)

// MetricsReporter 实现 health.HealthReporter 和 health.StatsReporter 接口
type MetricsReporter struct {
	metrics  *metrics.Metrics
	buckets  map[string]*BucketInfo
	manager  *Manager
}

// NewMetricsReporter 创建指标报告器
func NewMetricsReporter(metrics *metrics.Metrics, manager *Manager) *MetricsReporter {
	return &MetricsReporter{
		metrics: metrics,
		manager: manager,
	}
}

// ReportHealth 实现 health.HealthReporter 接口
func (r *MetricsReporter) ReportHealth(targetID string, status health.Status) {
	if r.metrics == nil {
		return
	}
	
	// 更新存储桶可用性状态
	r.manager.mu.RLock()
	bucket, exists := r.manager.buckets[targetID]
	r.manager.mu.RUnlock()
	
	if exists {
		bucket.mu.Lock()
		bucket.Available = status.Healthy
		bucket.LastChecked = status.LastChecked
		bucket.mu.Unlock()
		
		// 更新 Prometheus 指标
		r.metrics.SetBucketHealthy(targetID, bucket.Config.Endpoint, status.Healthy)
	}
}

// ReportStats 实现 health.StatsReporter 接口
func (r *MetricsReporter) ReportStats(stats *health.Stats) {
	if r.metrics == nil {
		return
	}
	
	// 更新存储桶使用统计
	r.manager.mu.RLock()
	bucket, exists := r.manager.buckets[stats.TargetID]
	r.manager.mu.RUnlock()
	
	if exists {
		bucket.mu.Lock()
		bucket.UsedSize = stats.UsedSize
		bucket.mu.Unlock()
		
		// 更新 Prometheus 指标
		r.metrics.SetBucketUsage(stats.TargetID, stats.UsedSize, bucket.Config.MaxSizeBytes)
	}
}