package bucket

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/DullJZ/s3-balance/internal/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// BucketInfo 存储桶信息
type BucketInfo struct {
	Config      config.BucketConfig
	Client      *s3.Client
	UsedSize    int64     // 已使用容量（字节）
	Available   bool      // 是否可用
	LastChecked time.Time // 最后检查时间
	mu          sync.RWMutex
}

// Manager 存储桶管理器
type Manager struct {
	buckets  map[string]*BucketInfo
	mu       sync.RWMutex
	config   *config.Config
	stopChan chan struct{}
}

// NewManager 创建新的存储桶管理器
func NewManager(cfg *config.Config) (*Manager, error) {
	m := &Manager{
		buckets:  make(map[string]*BucketInfo),
		config:   cfg,
		stopChan: make(chan struct{}),
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

	return m, nil
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

// Start 启动管理器（健康检查和统计更新）
func (m *Manager) Start(ctx context.Context) {
	// 启动健康检查
	go m.healthCheckLoop(ctx)
	
	// 启动统计更新
	go m.statsUpdateLoop(ctx)
}

// Stop 停止管理器
func (m *Manager) Stop() {
	close(m.stopChan)
}

// healthCheckLoop 健康检查循环
func (m *Manager) healthCheckLoop(ctx context.Context) {
	ticker := time.NewTicker(m.config.Balancer.HealthCheckPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.checkAllBuckets(ctx)
		}
	}
}

// statsUpdateLoop 统计更新循环
func (m *Manager) statsUpdateLoop(ctx context.Context) {
	ticker := time.NewTicker(m.config.Balancer.UpdateStatsPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.updateAllStats(ctx)
		}
	}
}

// checkAllBuckets 检查所有存储桶的健康状态
func (m *Manager) checkAllBuckets(ctx context.Context) {
	m.mu.RLock()
	buckets := make([]*BucketInfo, 0, len(m.buckets))
	for _, b := range m.buckets {
		buckets = append(buckets, b)
	}
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, bucket := range buckets {
		wg.Add(1)
		go func(b *BucketInfo) {
			defer wg.Done()
			m.checkBucket(ctx, b)
		}(bucket)
	}
	wg.Wait()
}

// checkBucket 检查单个存储桶
func (m *Manager) checkBucket(ctx context.Context, bucket *BucketInfo) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// 尝试列出存储桶（用于健康检查）
	_, err := bucket.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(bucket.Config.Name),
		MaxKeys: aws.Int32(1),
	})

	bucket.mu.Lock()
	bucket.Available = err == nil
	bucket.LastChecked = time.Now()
	bucket.mu.Unlock()
}

// updateAllStats 更新所有存储桶的统计信息
func (m *Manager) updateAllStats(ctx context.Context) {
	m.mu.RLock()
	buckets := make([]*BucketInfo, 0, len(m.buckets))
	for _, b := range m.buckets {
		buckets = append(buckets, b)
	}
	m.mu.RUnlock()

	var wg sync.WaitGroup
	for _, bucket := range buckets {
		wg.Add(1)
		go func(b *BucketInfo) {
			defer wg.Done()
			m.updateBucketStats(ctx, b)
		}(bucket)
	}
	wg.Wait()
}

// updateBucketStats 更新单个存储桶的统计信息
func (m *Manager) updateBucketStats(ctx context.Context, bucket *BucketInfo) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var totalSize int64
	var continuationToken *string

	for {
		output, err := bucket.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket.Config.Name),
			ContinuationToken: continuationToken,
		})
		if err != nil {
			break
		}

		for _, obj := range output.Contents {
			if obj.Size != nil {
				totalSize += *obj.Size
			}
		}

		if output.IsTruncated == nil || !*output.IsTruncated {
			break
		}
		continuationToken = output.NextContinuationToken
	}

	bucket.mu.Lock()
	bucket.UsedSize = totalSize
	bucket.mu.Unlock()
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

// GetAvailableBuckets 获取所有可用的存储桶
func (m *Manager) GetAvailableBuckets() []*BucketInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	var available []*BucketInfo
	for _, b := range m.buckets {
		b.mu.RLock()
		if b.Available && (b.Config.MaxSizeBytes == 0 || b.UsedSize < b.Config.MaxSizeBytes) {
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
