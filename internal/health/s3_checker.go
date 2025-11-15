package health

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Target S3健康检查目标
type S3Target struct {
	ID       string
	Bucket   string
	Endpoint string
	Client   *s3.Client
}

// GetID 实现 Target 接口
func (t *S3Target) GetID() string {
	return t.ID
}

// GetType 实现 Target 接口
func (t *S3Target) GetType() string {
	return "s3"
}

// GetEndpoint 实现 Target 接口
func (t *S3Target) GetEndpoint() string {
	return t.Endpoint
}

// S3Checker S3健康检查器
type S3Checker struct {
	config    Config
	opRecorder OperationRecorder
}

// NewS3Checker 创建S3健康检查器
func NewS3Checker(config Config) *S3Checker {
	if config.Interval == 0 {
		config.Interval = 30 * time.Second
	}
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}
	if config.Retries == 0 {
		config.Retries = 1
	}

	return &S3Checker{
		config: config,
	}
}

// SetOperationRecorder 设置操作记录器
func (c *S3Checker) SetOperationRecorder(recorder OperationRecorder) {
	c.opRecorder = recorder
}

// Check 执行S3健康检查
func (c *S3Checker) Check(ctx context.Context, target Target) Status {
	s3Target, ok := target.(*S3Target)
	if !ok {
		return Status{
			Healthy:     false,
			LastChecked: time.Now(),
			Message:     "invalid target type for S3 checker",
			Error:       fmt.Errorf("expected *S3Target, got %T", target),
		}
	}

	// 创建带超时的context
	checkCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()

	var lastErr error
	for i := 0; i < c.config.Retries; i++ {
		if i > 0 {
			// 重试前等待
			select {
			case <-ctx.Done():
				return Status{
					Healthy:     false,
					LastChecked: time.Now(),
					Message:     "health check cancelled",
					Error:       ctx.Err(),
				}
			case <-time.After(time.Second):
			}
		}

		err := c.performCheck(checkCtx, s3Target)
		if err == nil {
			return Status{
				Healthy:     true,
				LastChecked: time.Now(),
				Message:     fmt.Sprintf("S3 bucket %s is healthy", s3Target.Bucket),
			}
		}
		lastErr = err
	}

	return Status{
		Healthy:     false,
		LastChecked: time.Now(),
		Message:     fmt.Sprintf("S3 bucket %s is unhealthy after %d retries", s3Target.Bucket, c.config.Retries),
		Error:       lastErr,
	}
}

func (c *S3Checker) performCheck(ctx context.Context, target *S3Target) error {
	switch c.config.Strategy {
	case StrategyDetailed:
		return c.performDetailedCheck(ctx, target)
	default:
		return c.performSimpleCheck(ctx, target)
	}
}

// performSimpleCheck 执行简单健康检查（轻量级）
func (c *S3Checker) performSimpleCheck(ctx context.Context, target *S3Target) error {
	// 尝试列出1个对象，这是最轻量级的检查方式
	_, err := target.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(target.Bucket),
		MaxKeys: aws.Int32(1),
	})

	// 记录操作（ListObjectsV2 是 Class A 操作）
	if c.opRecorder != nil {
		c.opRecorder.RecordOperation(target.GetID(), OperationTypeA)
	}

	return err
}

// performDetailedCheck 执行详细健康检查（包含更多验证）
func (c *S3Checker) performDetailedCheck(ctx context.Context, target *S3Target) error {
	// 先执行简单检查
	if err := c.performSimpleCheck(ctx, target); err != nil {
		return err
	}

	// 可以添加更多详细检查，比如：
	// 1. 检查存储桶策略
	// 2. 测试写入权限（创建并删除测试对象）
	// 3. 检查存储桶版本控制状态
	// 4. 测量响应时间等性能指标

	return nil
}

// GetInterval 获取检查间隔
func (c *S3Checker) GetInterval() time.Duration {
	return c.config.Interval
}

// GetTimeout 获取检查超时时间
func (c *S3Checker) GetTimeout() time.Duration {
	return c.config.Timeout
}