package bucket

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/DullJZ/s3-balance/internal/config"
	"github.com/DullJZ/s3-balance/internal/health"
	"github.com/DullJZ/s3-balance/internal/metrics"
	"github.com/DullJZ/s3-balance/internal/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// OperationCategory 表示后端操作分类
type OperationCategory string

const (
	// OperationTypeA 表示写入类操作
	OperationTypeA OperationCategory = "A"
	// OperationTypeB 表示读取类操作
	OperationTypeB OperationCategory = "B"
)

// BucketInfo 存储桶信息
type BucketInfo struct {
	Config                config.BucketConfig
	Client                *s3.Client
	UsedSize              int64     // 已使用容量（字节）
	Available             bool      // 是否可用（由health监控更新）
	LastChecked           time.Time // 最后检查时间（由health监控更新）
	mu                    sync.RWMutex
	operationCountA       int64
	operationCountB       int64
	operationLimitReached bool
}

// Manager 存储桶管理器
type Manager struct {
	buckets       map[string]*BucketInfo
	mu            sync.RWMutex
	config        *config.Config
	stopChan      chan struct{}
	metrics       *metrics.Metrics
	healthMonitor *health.Monitor
	statsMonitor  *health.StatsMonitor
	monitorCtx    context.Context
	storage       *storage.Service
}

// NewManager 创建新的存储桶管理器
func NewManager(cfg *config.Config, metrics *metrics.Metrics, storageService *storage.Service) (*Manager, error) {
	m := &Manager{
		buckets:  make(map[string]*BucketInfo),
		config:   cfg,
		stopChan: make(chan struct{}),
		metrics:  metrics,
		storage:  storageService,
	}

	// 初始化所有存储桶客户端
	for _, bucketCfg := range cfg.Buckets {
		if !bucketCfg.Enabled {
			continue
		}

		client, err := createS3Client(bucketCfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create S3 client for bucket %s: %w", bucketCfg.Name, err)
		}

		info := &BucketInfo{
			Config:      bucketCfg,
			Client:      client,
			Available:   true,
			LastChecked: time.Now(),
		}

		m.buckets[bucketCfg.Name] = info
	}

	// 初始化健康监控
	m.initHealthMonitoring()

	// 加载持久化的操作计数
	m.loadOperationCounts()

	return m, nil
}

func (m *Manager) loadOperationCounts() {
	if m.storage == nil {
		return
	}

	counts, err := m.storage.GetBucketOperationCounts()
	if err != nil {
		log.Printf("Failed to load bucket operation counts: %v", err)
		return
	}

	m.mu.RLock()
	buckets := make(map[string]*BucketInfo, len(m.buckets))
	for name, info := range m.buckets {
		buckets[name] = info
	}
	m.mu.RUnlock()

	for name, info := range buckets {
		if info == nil {
			continue
		}

		oc, ok := counts[name]
		if !ok {
			continue
		}

		if info.SetOperationCount(OperationTypeA, oc.CountA) {
			log.Printf("Bucket %s disabled after exceeding A-type operation limit (persisted)", name)
		}
		if info.SetOperationCount(OperationTypeB, oc.CountB) {
			log.Printf("Bucket %s disabled after exceeding B-type operation limit (persisted)", name)
		}
	}
}

// createS3Client 创建S3客户端
func createS3Client(bucketCfg config.BucketConfig) (*s3.Client, error) {
	// 创建自定义端点解析器
	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		if bucketCfg.Endpoint != "" {
			return aws.Endpoint{
				URL:               bucketCfg.Endpoint,
				SigningRegion:     bucketCfg.Region,
				HostnameImmutable: true,
			}, nil
		}
		// 返回错误以使用默认解析器
		return aws.Endpoint{}, &aws.EndpointNotFoundError{}
	})

	// 配置AWS SDK
	cfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
		awsconfig.WithRegion(bucketCfg.Region),
		awsconfig.WithEndpointResolverWithOptions(customResolver),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				bucketCfg.AccessKeyID,
				bucketCfg.SecretAccessKey,
				"",
			),
		),
	)
	if err != nil {
		return nil, err
	}

	// 创建S3客户端
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = bucketCfg.PathStyle
	})

	return client, nil
}

// initHealthMonitoring 初始化健康监控系统
func (m *Manager) initHealthMonitoring() {
	// 创建指标报告器
	reporter := NewMetricsReporter(m.metrics, m)

	// 创建健康检查配置
	healthConfig := health.Config{
		Strategy: health.StrategySimple,
		Interval: m.config.Balancer.HealthCheckPeriod,
		Timeout:  5 * time.Second,
		Retries:  1,
	}

	// 创建S3健康检查器
	healthChecker := health.NewS3Checker(healthConfig)

	// 创建健康监控器
	m.healthMonitor = health.NewMonitor(healthChecker, reporter)

	// 创建统计收集器
	statsCollector := health.NewS3StatsCollector(30 * time.Second)

	// 创建统计监控器
	m.statsMonitor = health.NewStatsMonitor(
		statsCollector,
		m.config.Balancer.UpdateStatsPeriod,
		reporter,
	)

	// 注册所有非虚拟存储桶到监控系统
	for _, bucket := range m.buckets {
		if bucket.Config.Virtual {
			continue
		}

		target := &health.S3Target{
			ID:       bucket.Config.Name,
			Bucket:   bucket.Config.Name,
			Endpoint: bucket.Config.Endpoint,
			Client:   bucket.Client,
		}

		m.healthMonitor.RegisterTarget(target)
		m.statsMonitor.RegisterTarget(target)
	}
}

// Start 启动管理器（健康检查和统计更新）
func (m *Manager) Start(ctx context.Context) {
	m.monitorCtx = ctx
	m.startMonitors()
}

func (m *Manager) startMonitors() {
	ctx := m.monitorCtx
	if ctx == nil {
		return
	}

	// 启动健康监控
	if m.healthMonitor != nil {
		m.healthMonitor.Start(ctx)
	}

	// 启动统计监控
	if m.statsMonitor != nil {
		m.statsMonitor.Start(ctx)
	}
}

// Stop 停止管理器
func (m *Manager) Stop() {
	close(m.stopChan)

	// 停止健康监控
	if m.healthMonitor != nil {
		m.healthMonitor.Stop()
	}

	// 停止统计监控
	if m.statsMonitor != nil {
		m.statsMonitor.Stop()
	}
}

// GetBucket 获取指定名称的存储桶
func (m *Manager) GetBucket(name string) (*BucketInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	bucket, ok := m.buckets[name]
	return bucket, ok
}

// GetAllBuckets 获取所有存储桶
func (m *Manager) GetAllBuckets() []*BucketInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	buckets := make([]*BucketInfo, 0, len(m.buckets))
	for _, b := range m.buckets {
		buckets = append(buckets, b)
	}
	return buckets
}

// GetAvailableBuckets 获取所有可用的存储桶（排除虚拟存储桶）
func (m *Manager) GetAvailableBuckets() []*BucketInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var available []*BucketInfo
	for _, b := range m.buckets {
		b.mu.RLock()
		// 虚拟存储桶不用于负载均衡，排除它们
		if !b.Config.Virtual && b.Available && (b.Config.MaxSizeBytes == 0 || b.UsedSize < b.Config.MaxSizeBytes) {
			available = append(available, b)
		}
		b.mu.RUnlock()
	}
	return available
}

// GetAvailableSpace 获取存储桶的可用空间
func (b *BucketInfo) GetAvailableSpace() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.Config.MaxSizeBytes == 0 {
		return 1 << 62 // 返回一个很大的数表示无限制
	}
	return b.Config.MaxSizeBytes - b.UsedSize
}

// IsAvailable 检查存储桶是否可用
func (b *BucketInfo) IsAvailable() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Available
}

// GetUsedSize 获取已使用容量
func (b *BucketInfo) GetUsedSize() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.UsedSize
}

// UpdateUsedSize 更新已使用容量
func (b *BucketInfo) UpdateUsedSize(delta int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.UsedSize += delta
}

// RecordOperation 记录一次后端操作并根据配置判断是否需要禁用存储桶
func (b *BucketInfo) RecordOperation(category OperationCategory) bool {
	if b == nil {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.Config.Virtual {
		return false
	}

	var (
		limit int64
		count *int64
	)

	switch category {
	case OperationTypeA:
		b.operationCountA++
		count = &b.operationCountA
		limit = int64(b.Config.OperationLimits.TypeA)
	case OperationTypeB:
		b.operationCountB++
		count = &b.operationCountB
		limit = int64(b.Config.OperationLimits.TypeB)
	default:
		return false
	}

	if limit <= 0 || count == nil {
		return false
	}

	if !b.operationLimitReached && *count >= limit {
		b.Available = false
		b.operationLimitReached = true
		return true
	}

	return false
}

// SetOperationCount 设置指定类别的操作计数并检查上限
func (b *BucketInfo) SetOperationCount(category OperationCategory, value int64) bool {
	if b == nil {
		return false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.Config.Virtual {
		return false
	}

	var limit int64

	switch category {
	case OperationTypeA:
		b.operationCountA = value
		limit = int64(b.Config.OperationLimits.TypeA)
	case OperationTypeB:
		b.operationCountB = value
		limit = int64(b.Config.OperationLimits.TypeB)
	default:
		return false
	}

	if limit <= 0 {
		return false
	}

	if !b.operationLimitReached && value >= limit {
		b.Available = false
		b.operationLimitReached = true
		return true
	}

	return false
}

// IsVirtual 检查是否为虚拟存储桶
func (b *BucketInfo) IsVirtual() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Config.Virtual
}

// GetOperationCount 获取指定类型的操作计数
func (b *BucketInfo) GetOperationCount(category OperationCategory) int64 {
	if b == nil {
		return 0
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	switch category {
	case OperationTypeA:
		return b.operationCountA
	case OperationTypeB:
		return b.operationCountB
	default:
		return 0
	}
}

// GetVirtualBuckets 获取所有虚拟存储桶
func (m *Manager) GetVirtualBuckets() []*BucketInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var virtual []*BucketInfo
	for _, b := range m.buckets {
		if b.IsVirtual() {
			virtual = append(virtual, b)
		}
	}
	return virtual
}

// GetRealBuckets 获取所有真实存储桶
func (m *Manager) GetRealBuckets() []*BucketInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var real []*BucketInfo
	for _, b := range m.buckets {
		if !b.IsVirtual() {
			real = append(real, b)
		}
	}
	return real
}

// UpdateConfig 更新配置（支持热更新）
func (m *Manager) UpdateConfig(newConfig *config.Config) error {
	log.Println("Updating bucket manager configuration...")

	m.mu.Lock()
	oldConfig := m.config
	m.config = newConfig

	// 检查是否需要重新创建存储桶
	needsRestart := m.checkIfRestartNeeded(oldConfig, newConfig)
	restartMonitors := false

	if needsRestart {
		log.Println("Bucket configuration changed significantly, recreating buckets...")

		// 停止现有的监控
		if m.healthMonitor != nil {
			m.healthMonitor.Stop()
		}
		if m.statsMonitor != nil {
			m.statsMonitor.Stop()
		}

		// 重新创建bucket映射
		m.buckets = make(map[string]*BucketInfo)

		// 初始化所有存储桶客户端
		for _, bucketCfg := range newConfig.Buckets {
			if !bucketCfg.Enabled {
				continue
			}

			client, err := createS3Client(bucketCfg)
			if err != nil {
				// 如果创建失败，恢复旧配置并回滚
				m.config = oldConfig
				m.mu.Unlock()
				return fmt.Errorf("failed to create S3 client for bucket %s: %v", bucketCfg.Name, err)
			}

			info := &BucketInfo{
				Config:      bucketCfg,
				Client:      client,
				Available:   true,
				LastChecked: time.Now(),
			}

			m.buckets[bucketCfg.Name] = info
		}

		// 重新初始化监控
		m.initHealthMonitoring()
		restartMonitors = true
	} else {
		// 只更新监控间隔（需要重启监控来改变间隔）
		log.Println("Updating monitoring intervals...")
		if m.healthMonitor != nil {
			m.healthMonitor.Stop()
		}
		if m.statsMonitor != nil {
			m.statsMonitor.Stop()
		}
		// 重新初始化监控以应用新的间隔
		m.initHealthMonitoring()
		restartMonitors = true
	}

	m.mu.Unlock()

	m.loadOperationCounts()

	if restartMonitors {
		m.startMonitors()
	}

	log.Println("Bucket manager configuration updated successfully")
	return nil
}

// checkIfRestartNeeded 检查是否需要重启bucket manager
func (m *Manager) checkIfRestartNeeded(oldConfig, newConfig *config.Config) bool {
	// 检查bucket数量变化
	if len(oldConfig.Buckets) != len(newConfig.Buckets) {
		return true
	}

	// 检查bucket配置变化
	for i, oldBucket := range oldConfig.Buckets {
		if i >= len(newConfig.Buckets) {
			return true
		}
		newBucket := newConfig.Buckets[i]

		// 检查关键配置项
		if oldBucket.Name != newBucket.Name ||
			oldBucket.Endpoint != newBucket.Endpoint ||
			oldBucket.AccessKeyID != newBucket.AccessKeyID ||
			oldBucket.SecretAccessKey != newBucket.SecretAccessKey ||
			oldBucket.Region != newBucket.Region ||
			oldBucket.Enabled != newBucket.Enabled ||
			oldBucket.Virtual != newBucket.Virtual {
			return true
		}
	}

	return false
}
