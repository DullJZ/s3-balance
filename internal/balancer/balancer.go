package balancer

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/DullJZ/s3-balance/internal/config"
)

// Strategy 负载均衡策略接口
type Strategy interface {
	SelectBucket(buckets []*bucket.BucketInfo, key string, size int64) (*bucket.BucketInfo, error)
	Name() string
}

// Balancer 负载均衡器
type Balancer struct {
	manager  *bucket.Manager
	strategy Strategy
	config   *config.BalancerConfig
}

// NewBalancer 创建新的负载均衡器
func NewBalancer(manager *bucket.Manager, cfg *config.BalancerConfig) (*Balancer, error) {
	var strategy Strategy
	
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
	}, nil
}

// SelectBucket 选择一个存储桶
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

	return selected, nil
}

// GetStrategy 获取当前策略名称
func (b *Balancer) GetStrategy() string {
	return b.strategy.Name()
}

// RoundRobinStrategy 轮询策略
type RoundRobinStrategy struct {
	counter uint64
}

// NewRoundRobinStrategy 创建轮询策略
func NewRoundRobinStrategy() *RoundRobinStrategy {
	return &RoundRobinStrategy{}
}

// SelectBucket 选择存储桶（轮询）
func (s *RoundRobinStrategy) SelectBucket(buckets []*bucket.BucketInfo, key string, size int64) (*bucket.BucketInfo, error) {
	if len(buckets) == 0 {
		return nil, fmt.Errorf("no buckets available")
	}
	
	index := atomic.AddUint64(&s.counter, 1) % uint64(len(buckets))
	return buckets[index], nil
}

// Name 返回策略名称
func (s *RoundRobinStrategy) Name() string {
	return "round-robin"
}

// LeastSpaceStrategy 最少使用空间策略
type LeastSpaceStrategy struct{}

// NewLeastSpaceStrategy 创建最少使用空间策略
func NewLeastSpaceStrategy() *LeastSpaceStrategy {
	return &LeastSpaceStrategy{}
}

// SelectBucket 选择存储桶（选择使用空间最少的）
func (s *LeastSpaceStrategy) SelectBucket(buckets []*bucket.BucketInfo, key string, size int64) (*bucket.BucketInfo, error) {
	if len(buckets) == 0 {
		return nil, fmt.Errorf("no buckets available")
	}

	// 按可用空间排序（从大到小）
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].GetAvailableSpace() > buckets[j].GetAvailableSpace()
	})

	return buckets[0], nil
}

// Name 返回策略名称
func (s *LeastSpaceStrategy) Name() string {
	return "least-space"
}

// WeightedStrategy 加权策略
type WeightedStrategy struct {
	mu sync.RWMutex
}

// NewWeightedStrategy 创建加权策略
func NewWeightedStrategy() *WeightedStrategy {
	return &WeightedStrategy{}
}

// SelectBucket 选择存储桶（基于权重）
func (s *WeightedStrategy) SelectBucket(buckets []*bucket.BucketInfo, key string, size int64) (*bucket.BucketInfo, error) {
	if len(buckets) == 0 {
		return nil, fmt.Errorf("no buckets available")
	}

	// 计算总权重
	totalWeight := 0
	for _, b := range buckets {
		totalWeight += b.Config.Weight
	}

	if totalWeight == 0 {
		// 如果所有权重都是0，则随机选择
		return buckets[rand.Intn(len(buckets))], nil
	}

	// 根据权重随机选择
	randomWeight := rand.Intn(totalWeight)
	currentWeight := 0

	for _, b := range buckets {
		currentWeight += b.Config.Weight
		if randomWeight < currentWeight {
			return b, nil
		}
	}

	// 不应该到达这里，但为了安全返回最后一个
	return buckets[len(buckets)-1], nil
}

// Name 返回策略名称
func (s *WeightedStrategy) Name() string {
	return "weighted"
}

// ConsistentHashStrategy 一致性哈希策略
type ConsistentHashStrategy struct {
	replicas int
	ring     map[uint32]*bucket.BucketInfo
	nodes    []uint32
	mu       sync.RWMutex
}

// NewConsistentHashStrategy 创建一致性哈希策略
func NewConsistentHashStrategy() *ConsistentHashStrategy {
	return &ConsistentHashStrategy{
		replicas: 100, // 每个节点的虚拟节点数
		ring:     make(map[uint32]*bucket.BucketInfo),
	}
}

// SelectBucket 选择存储桶（基于一致性哈希）
func (s *ConsistentHashStrategy) SelectBucket(buckets []*bucket.BucketInfo, key string, size int64) (*bucket.BucketInfo, error) {
	if len(buckets) == 0 {
		return nil, fmt.Errorf("no buckets available")
	}

	// 更新哈希环
	s.updateRing(buckets)

	// 计算key的哈希值
	hash := s.hash(key)

	// 在环上找到第一个大于等于hash的节点
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx := sort.Search(len(s.nodes), func(i int) bool {
		return s.nodes[i] >= hash
	})

	// 如果没找到，返回第一个节点（环形结构）
	if idx == len(s.nodes) {
		idx = 0
	}

	return s.ring[s.nodes[idx]], nil
}

// updateRing 更新哈希环
func (s *ConsistentHashStrategy) updateRing(buckets []*bucket.BucketInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 清空现有环
	s.ring = make(map[uint32]*bucket.BucketInfo)
	s.nodes = nil

	// 为每个存储桶添加虚拟节点
	for _, b := range buckets {
		for i := 0; i < s.replicas; i++ {
			virtualKey := fmt.Sprintf("%s-%d", b.Config.Name, i)
			hash := s.hash(virtualKey)
			s.ring[hash] = b
			s.nodes = append(s.nodes, hash)
		}
	}

	// 排序节点
	sort.Slice(s.nodes, func(i, j int) bool {
		return s.nodes[i] < s.nodes[j]
	})
}

// hash 计算哈希值
func (s *ConsistentHashStrategy) hash(key string) uint32 {
	h := md5.Sum([]byte(key))
	return binary.BigEndian.Uint32(h[:4])
}

// Name 返回策略名称
func (s *ConsistentHashStrategy) Name() string {
	return "consistent-hash"
}
