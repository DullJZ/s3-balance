package health

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// StatsCollector 统计信息收集器接口
type StatsCollector interface {
	// CollectStats 收集统计信息
	CollectStats(ctx context.Context, target Target) (*Stats, error)
}

// Stats 统计信息
type Stats struct {
	TargetID    string
	UsedSize    int64     // 已使用大小（字节）
	ObjectCount int64     // 对象数量
	LastUpdated time.Time // 最后更新时间
}

// S3StatsCollector S3统计信息收集器
type S3StatsCollector struct {
	timeout    time.Duration
	opRecorder OperationRecorder
}

// NewS3StatsCollector 创建S3统计信息收集器
func NewS3StatsCollector(timeout time.Duration) *S3StatsCollector {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &S3StatsCollector{
		timeout: timeout,
	}
}

// SetOperationRecorder 设置操作记录器
func (c *S3StatsCollector) SetOperationRecorder(recorder OperationRecorder) {
	c.opRecorder = recorder
}

// CollectStats 收集S3存储桶统计信息
func (c *S3StatsCollector) CollectStats(ctx context.Context, target Target) (*Stats, error) {
	s3Target, ok := target.(*S3Target)
	if !ok {
		return nil, fmt.Errorf("expected *S3Target, got %T", target)
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var totalSize int64
	var objectCount int64
	var continuationToken *string

	for {
		output, err := s3Target.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s3Target.Bucket),
			ContinuationToken: continuationToken,
		})

		// 记录操作（每次 ListObjectsV2 调用都是 Class A 操作）
		if c.opRecorder != nil {
			c.opRecorder.RecordOperation(s3Target.GetID(), OperationTypeA)
		}

		if err != nil {
			return nil, err
		}

		for _, obj := range output.Contents {
			objectCount++
			if obj.Size != nil {
				totalSize += *obj.Size
			}
		}

		if output.IsTruncated == nil || !*output.IsTruncated {
			break
		}
		continuationToken = output.NextContinuationToken
	}

	return &Stats{
		TargetID:    s3Target.GetID(),
		UsedSize:    totalSize,
		ObjectCount: objectCount,
		LastUpdated: time.Now(),
	}, nil
}

// StatsMonitor 统计信息监控器
type StatsMonitor struct {
	collector StatsCollector
	targets   map[string]Target
	stats     map[string]*Stats
	reporter  StatsReporter
	mu        sync.RWMutex
	stopChan  chan struct{}
	interval  time.Duration
}

// StatsReporter 统计信息报告器接口
type StatsReporter interface {
	// ReportStats 报告统计信息
	ReportStats(stats *Stats)
}

// NewStatsMonitor 创建统计信息监控器
func NewStatsMonitor(collector StatsCollector, interval time.Duration, reporter StatsReporter) *StatsMonitor {
	if interval == 0 {
		interval = 60 * time.Second
	}
	return &StatsMonitor{
		collector: collector,
		targets:   make(map[string]Target),
		stats:     make(map[string]*Stats),
		reporter:  reporter,
		stopChan:  make(chan struct{}),
		interval:  interval,
	}
}

// RegisterTarget 注册监控目标
func (m *StatsMonitor) RegisterTarget(target Target) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.targets[target.GetID()] = target
}

// UnregisterTarget 注销监控目标
func (m *StatsMonitor) UnregisterTarget(targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.targets, targetID)
	delete(m.stats, targetID)
}

// Start 启动统计监控
func (m *StatsMonitor) Start(ctx context.Context) {
	// 立即执行一次收集
	m.collectAll(ctx)
	
	// 启动定期收集
	go m.run(ctx)
}

// Stop 停止统计监控
func (m *StatsMonitor) Stop() {
	close(m.stopChan)
}

// run 运行统计收集循环
func (m *StatsMonitor) run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.collectAll(ctx)
		}
	}
}

// collectAll 收集所有目标的统计信息
func (m *StatsMonitor) collectAll(ctx context.Context) {
	m.mu.RLock()
	targets := make([]Target, 0, len(m.targets))
	for _, target := range m.targets {
		targets = append(targets, target)
	}
	m.mu.RUnlock()

	// 并发收集所有目标的统计信息
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			m.collectTarget(ctx, t)
		}(target)
	}
	wg.Wait()
}

// collectTarget 收集单个目标的统计信息
func (m *StatsMonitor) collectTarget(ctx context.Context, target Target) {
	stats, err := m.collector.CollectStats(ctx, target)
	if err != nil {
		// 记录错误但不影响其他目标
		return
	}
	
	// 更新统计信息
	m.mu.Lock()
	m.stats[target.GetID()] = stats
	m.mu.Unlock()
	
	// 报告统计信息
	if m.reporter != nil {
		m.reporter.ReportStats(stats)
	}
}

// GetStats 获取指定目标的统计信息
func (m *StatsMonitor) GetStats(targetID string) (*Stats, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats, ok := m.stats[targetID]
	return stats, ok
}

// GetAllStats 获取所有目标的统计信息
func (m *StatsMonitor) GetAllStats() map[string]*Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	result := make(map[string]*Stats, len(m.stats))
	for id, stats := range m.stats {
		result[id] = stats
	}
	return result
}