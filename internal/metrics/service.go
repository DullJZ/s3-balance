package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	bucketHealthy = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "s3_balance_bucket_healthy",
		Help: "Health status of S3 bucket (1 = healthy, 0 = unhealthy)",
	}, []string{"bucket", "endpoint"})

	bucketUsage = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "s3_balance_bucket_usage_bytes",
		Help: "Current usage of S3 bucket in bytes",
	}, []string{"bucket"})

	bucketCapacity = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "s3_balance_bucket_capacity_bytes",
		Help: "Maximum capacity of S3 bucket in bytes",
	}, []string{"bucket"})

	s3OperationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "s3_balance_s3_operations_total",
		Help: "Total number of S3 operations",
	}, []string{"operation", "bucket", "status"})

	s3OperationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "s3_balance_s3_operation_duration_seconds",
		Help:    "Duration of S3 operations in seconds",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation", "bucket"})

	balancerDecisions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "s3_balance_balancer_decisions_total",
		Help: "Total number of load balancing decisions",
	}, []string{"strategy", "bucket"})
)

type Metrics struct{}

func New() *Metrics {
	return &Metrics{}
}

func (m *Metrics) SetBucketHealthy(bucket, endpoint string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1.0
	}
	bucketHealthy.WithLabelValues(bucket, endpoint).Set(value)
}

func (m *Metrics) SetBucketUsage(bucket string, usage, capacity int64) {
	bucketUsage.WithLabelValues(bucket).Set(float64(usage))
	bucketCapacity.WithLabelValues(bucket).Set(float64(capacity))
}

func (m *Metrics) RecordS3Operation(operation, bucket, status string) {
	s3OperationsTotal.WithLabelValues(operation, bucket, status).Inc()
}

func (m *Metrics) RecordS3OperationDuration(operation, bucket string, duration float64) {
	s3OperationDuration.WithLabelValues(operation, bucket).Observe(duration)
}

func (m *Metrics) RecordBalancerDecision(strategy, bucket string) {
	balancerDecisions.WithLabelValues(strategy, bucket).Inc()
}