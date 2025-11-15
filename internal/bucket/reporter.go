package bucket

import (
	"log"

	"github.com/DullJZ/s3-balance/internal/health"
	"github.com/DullJZ/s3-balance/internal/metrics"
)

// MetricsReporter 实现 health.HealthReporter 和 health.StatsReporter 接口
type MetricsReporter struct {
	metrics *metrics.Metrics
	buckets map[string]*BucketInfo
	manager *Manager
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
		if !bucket.operationLimitReached {
			bucket.Available = status.Healthy
		}
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

// RecordOperation 实现 health.OperationRecorder 接口
func (r *MetricsReporter) RecordOperation(targetID string, category health.OperationCategory) {
	r.manager.mu.RLock()
	bucket, exists := r.manager.buckets[targetID]
	storage := r.manager.storage
	r.manager.mu.RUnlock()

	if !exists {
		return
	}

	// 转换 health.OperationCategory 到 bucket.OperationCategory
	var bucketCategory OperationCategory
	switch category {
	case health.OperationTypeA:
		bucketCategory = OperationTypeA
	case health.OperationTypeB:
		bucketCategory = OperationTypeB
	default:
		return
	}

	// 更新 Prometheus 指标
	if r.metrics != nil {
		r.metrics.RecordBackendOperation(targetID, string(bucketCategory))
	}

	// 持久化操作计数到数据库并更新内存计数
	var disabled bool
	if storage != nil {
		// 先持久化到数据库
		newCount, err := storage.IncrementBucketOperation(targetID, string(bucketCategory))
		if err != nil {
			log.Printf("Failed to persist health check operation count for bucket %s: %v", targetID, err)
			// 如果数据库更新失败，仍然更新内存计数
			disabled = bucket.RecordOperation(bucketCategory)
		} else {
			// 使用数据库返回的最新计数更新内存
			disabled = bucket.SetOperationCount(bucketCategory, newCount)
		}
	} else {
		// 没有 storage service，只更新内存
		disabled = bucket.RecordOperation(bucketCategory)
	}

	if disabled {
		log.Printf("Bucket %s disabled after exceeding %s-type operation limit (detected by health check)", targetID, bucketCategory)
	}
}
