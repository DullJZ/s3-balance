package health

import (
	"context"
	"time"
)

// Status 健康检查状态
type Status struct {
	Healthy     bool      // 是否健康
	LastChecked time.Time // 最后检查时间
	Message     string    // 状态信息
	Error       error     // 错误信息（如果有）
}

// Target 健康检查目标
type Target interface {
	// GetID 获取目标唯一标识
	GetID() string
	// GetType 获取目标类型
	GetType() string
	// GetEndpoint 获取目标端点（用于日志和监控）
	GetEndpoint() string
}

// Checker 健康检查器接口
type Checker interface {
	// Check 执行健康检查
	Check(ctx context.Context, target Target) Status
	// GetInterval 获取检查间隔
	GetInterval() time.Duration
	// GetTimeout 获取检查超时时间
	GetTimeout() time.Duration
}

// Strategy 健康检查策略
type Strategy string

const (
	// StrategySimple 简单健康检查（快速探测）
	StrategySimple Strategy = "simple"
	// StrategyDetailed 详细健康检查（包含性能指标）
	StrategyDetailed Strategy = "detailed"
)

// Config 健康检查配置
type Config struct {
	Strategy Strategy      `yaml:"strategy"`
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
	Retries  int           `yaml:"retries"`
}

// DefaultConfig 默认配置
func DefaultConfig() Config {
	return Config{
		Strategy: StrategySimple,
		Interval: 30 * time.Second,
		Timeout:  5 * time.Second,
		Retries:  3,
	}
}

// HealthReporter 健康状态报告器接口
type HealthReporter interface {
	// ReportHealth 报告健康状态
	ReportHealth(targetID string, status Status)
}

// OperationCategory 操作分类
type OperationCategory string

const (
	// OperationTypeA 写入类操作 (ListObjects, PutObject, etc.)
	OperationTypeA OperationCategory = "A"
	// OperationTypeB 读取类操作 (GetObject)
	OperationTypeB OperationCategory = "B"
)

// OperationRecorder 操作记录器接口
type OperationRecorder interface {
	// RecordOperation 记录一次后端操作
	RecordOperation(targetID string, category OperationCategory)
}
