package balancer

import (
	"fmt"

	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/DullJZ/s3-balance/internal/config"
	"github.com/DullJZ/s3-balance/internal/metrics"
)

// Balancer 负载均衡器
// 负责根据配置的策略选择合适的存储桶
type Balancer struct {
	manager  *bucket.Manager
	strategy Strategy
	config   *config.BalancerConfig
	metrics  *metrics.Metrics
}

// NewBalancer 创建新的负载均衡器
func NewBalancer(manager *bucket.Manager, cfg *config.BalancerConfig) (*Balancer, error) {
	var strategy Strategy
	
	// 根据配置创建对应的策略实例
	switch cfg.Strategy {
	case "round-robin":
		strategy = NewRoundRobinStrategy()
	case "least-space":
		strategy = NewLeastSpaceStrategy()
	case "weighted":
		strategy = NewWeightedStrategy()
	case "consistent-hash":
		strategy = NewConsistentHashStrategy()
	default:
		return nil, fmt.Errorf("unknown balancer strategy: %s", cfg.Strategy)
	}

	return &Balancer{
		manager:  manager,
		strategy: strategy,
		config:   cfg,
		metrics:  nil, // 将在main.go中设置
	}, nil
}

// SelectBucket 选择一个存储桶
// 首先过滤出有足够空间的存储桶，然后使用策略选择
func (b *Balancer) SelectBucket(key string, size int64) (*bucket.BucketInfo, error) {
	// 获取所有可用的存储桶
	buckets := b.manager.GetAvailableBuckets()
	if len(buckets) == 0 {
		return nil, fmt.Errorf("no available buckets")
	}

	// 过滤出有足够空间的存储桶
	var availableBuckets []*bucket.BucketInfo
	for _, bucket := range buckets {
		if bucket.GetAvailableSpace() >= size {
			availableBuckets = append(availableBuckets, bucket)
		}
	}

	if len(availableBuckets) == 0 {
		return nil, fmt.Errorf("no bucket has enough space for %d bytes", size)
	}

	// 使用策略选择存储桶
	selected, err := b.strategy.SelectBucket(availableBuckets, key, size)
	if err != nil {
		return nil, err
	}

	// 记录指标
	if b.metrics != nil && selected != nil {
		b.metrics.RecordBalancerDecision(b.strategy.Name(), selected.Config.Name)
	}

	return selected, nil
}

// GetStrategy 获取当前策略名称
func (b *Balancer) GetStrategy() string {
	return b.strategy.Name()
}

// SetStrategy 动态切换策略
// 允许在运行时更改负载均衡策略
func (b *Balancer) SetStrategy(strategyName string) error {
	var strategy Strategy

	switch strategyName {
	case "round-robin":
		strategy = NewRoundRobinStrategy()
	case "least-space":
		strategy = NewLeastSpaceStrategy()
	case "weighted":
		strategy = NewWeightedStrategy()
	case "consistent-hash":
		strategy = NewConsistentHashStrategy()
	default:
		return fmt.Errorf("unknown balancer strategy: %s", strategyName)
	}

	b.strategy = strategy
	return nil
}

// UpdateStrategy 更新负载均衡策略（热更新用）
func (b *Balancer) UpdateStrategy(strategyName string) error {
	return b.SetStrategy(strategyName)
}

// SetMetrics 设置指标服务
func (b *Balancer) SetMetrics(metrics *metrics.Metrics) {
	b.metrics = metrics
}

// GetAvailableBuckets 获取所有可用的存储桶
// 方便外部直接查询可用存储桶列表
func (b *Balancer) GetAvailableBuckets() []*bucket.BucketInfo {
	return b.manager.GetAvailableBuckets()
}

// GetBucketStats 获取存储桶统计信息
// 返回每个存储桶的使用情况
func (b *Balancer) GetBucketStats() map[string]BucketStats {
	stats := make(map[string]BucketStats)
	buckets := b.manager.GetAllBuckets()
	
	for _, bucket := range buckets {
		stats[bucket.Config.Name] = BucketStats{
			Name:           bucket.Config.Name,
			TotalSpace:     bucket.Config.MaxSizeBytes,
			UsedSpace:      bucket.GetUsedSize(),
			AvailableSpace: bucket.GetAvailableSpace(),
			IsAvailable:    bucket.IsAvailable(),
			Weight:         bucket.Config.Weight,
		}
	}
	
	return stats
}

// BucketStats 存储桶统计信息
type BucketStats struct {
	Name           string `json:"name"`
	TotalSpace     int64  `json:"total_space"`
	UsedSpace      int64  `json:"used_space"`
	AvailableSpace int64  `json:"available_space"`
	IsAvailable    bool   `json:"is_available"`
	Weight         int    `json:"weight"`
}
