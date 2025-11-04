package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 全局配置结构
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Buckets  []BucketConfig `yaml:"buckets"`
	Balancer BalancerConfig `yaml:"balancer"`
	Metrics  MetricsConfig  `yaml:"metrics"`
	S3API    S3APIConfig    `yaml:"s3api"`
	API      APIConfig      `yaml:"api"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	IdleTimeout  time.Duration `yaml:"idle_timeout"`
}

// BucketConfig S3存储桶配置
type BucketConfig struct {
	Name            string               `yaml:"name"`              // 桶名称
	Endpoint        string               `yaml:"endpoint"`          // S3端点
	Region          string               `yaml:"region"`            // 区域
	AccessKeyID     string               `yaml:"access_key_id"`     // 访问密钥ID
	SecretAccessKey string               `yaml:"secret_access_key"` // 访问密钥
	MaxSize         string               `yaml:"max_size"`          // 最大容量 (例如: "10GB")
	MaxSizeBytes    int64                `yaml:"-"`                 // 内部使用，字节为单位
	Weight          int                  `yaml:"weight"`            // 权重 (用于负载均衡)
	Enabled         bool                 `yaml:"enabled"`           // 是否启用
	PathStyle       bool                 `yaml:"path_style"`        // 是否使用路径风格访问
	Virtual         bool                 `yaml:"virtual"`           // 是否为虚拟存储桶（仅S3 API中可见）
	OperationLimits OperationLimitConfig `yaml:"operation_limits"`
}

// OperationLimitConfig 后端操作次数限制配置
type OperationLimitConfig struct {
	TypeA int `yaml:"type_a"` // 类型A操作上限（0表示不限制）
	TypeB int `yaml:"type_b"` // 类型B操作上限（0表示不限制）
}

// BalancerConfig 负载均衡配置
type BalancerConfig struct {
	Strategy          string        `yaml:"strategy"`            // 负载均衡策略: "round-robin", "least-space", "weighted"
	HealthCheckPeriod time.Duration `yaml:"health_check_period"` // 健康检查周期
	UpdateStatsPeriod time.Duration `yaml:"update_stats_period"` // 统计更新周期
	RetryAttempts     int           `yaml:"retry_attempts"`      // 重试次数
	RetryDelay        time.Duration `yaml:"retry_delay"`         // 重试延迟
}

// MetricsConfig 监控指标配置
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"`
	// Port    int    `yaml:"port"`  // 目前未使用，与主服务共享端口
}

// S3APIConfig S3兼容API配置
type S3APIConfig struct {
	AccessKey    string `yaml:"access_key"`    // S3访问密钥ID
	SecretKey    string `yaml:"secret_key"`    // S3秘密访问密钥
	VirtualHost  bool   `yaml:"virtual_host"`  // 是否使用虚拟主机模式
	ProxyMode    bool   `yaml:"proxy_mode"`    // 是否使用代理模式（而非重定向）
	AuthRequired bool   `yaml:"auth_required"` // 是否需要认证
	Host         string `yaml:"host"`          // 用于签名验证的Host（为空则使用请求的Host）
}

// APIConfig 管理API配置
type APIConfig struct {
	Enabled bool   `yaml:"enabled"` // 是否启用管理API
	Token   string `yaml:"token"`   // API访问令牌
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Type            string `yaml:"type"`              // 数据库类型: sqlite, mysql, postgres
	DSN             string `yaml:"dsn"`               // 数据源名称
	MaxOpenConns    int    `yaml:"max_open_conns"`    // 最大打开连接数
	MaxIdleConns    int    `yaml:"max_idle_conns"`    // 最大空闲连接数
	ConnMaxLifetime int    `yaml:"conn_max_lifetime"` // 连接最大生命周期（秒）
	LogLevel        string `yaml:"log_level"`         // 日志级别: silent, error, warn, info
	AutoMigrate     bool   `yaml:"auto_migrate"`      // 是否自动迁移
}

// Load 从文件加载配置
func Load(configPath string) (*Config, error) {
	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var config Config
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	// 解析容量大小
	for i := range config.Buckets {
		if err := config.Buckets[i].ParseMaxSize(); err != nil {
			return nil, fmt.Errorf("failed to parse max size for bucket %s: %w",
				config.Buckets[i].Name, err)
		}
	}

	// 设置默认值
	config.SetDefaults()

	return &config, nil
}

// SetDefaults 设置默认配置值
func (c *Config) SetDefaults() {
	if c.Server.Host == "" {
		c.Server.Host = "0.0.0.0"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 30 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 30 * time.Second
	}
	if c.Server.IdleTimeout == 0 {
		c.Server.IdleTimeout = 60 * time.Second
	}

	if c.Balancer.Strategy == "" {
		c.Balancer.Strategy = "least-space"
	}
	if c.Balancer.HealthCheckPeriod == 0 {
		c.Balancer.HealthCheckPeriod = 30 * time.Second
	}
	if c.Balancer.UpdateStatsPeriod == 0 {
		c.Balancer.UpdateStatsPeriod = 60 * time.Second
	}
	if c.Balancer.RetryAttempts == 0 {
		c.Balancer.RetryAttempts = 3
	}
	if c.Balancer.RetryDelay == 0 {
		c.Balancer.RetryDelay = time.Second
	}

	if c.Metrics.Path == "" {
		c.Metrics.Path = "/metrics"
	}

	// 数据库默认值
	if c.Database.Type == "" {
		c.Database.Type = "sqlite"
	}
	if c.Database.Type == "sqlite" && c.Database.DSN == "" {
		c.Database.DSN = "data/s3-balance.db"
	}
	if c.Database.MaxOpenConns == 0 {
		c.Database.MaxOpenConns = 25
	}
	if c.Database.MaxIdleConns == 0 {
		c.Database.MaxIdleConns = 5
	}
	if c.Database.ConnMaxLifetime == 0 {
		c.Database.ConnMaxLifetime = 300
	}
	if c.Database.LogLevel == "" {
		c.Database.LogLevel = "warn"
	}

	// S3 API默认值
	if c.S3API.AccessKey == "" {
		c.S3API.AccessKey = "AKIAIOSFODNN7EXAMPLE"
	}
	if c.S3API.SecretKey == "" {
		c.S3API.SecretKey = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	}

	// 管理API默认值
	if c.API.Token == "" {
		c.API.Token = "your-secure-api-token-here"
	}
}

// ParseMaxSize 解析最大容量字符串为字节
func (bc *BucketConfig) ParseMaxSize() error {
	if bc.MaxSize == "" {
		bc.MaxSizeBytes = 0 // 无限制
		return nil
	}

	var size int64
	var unit string
	_, err := fmt.Sscanf(bc.MaxSize, "%d%s", &size, &unit)
	if err != nil {
		return fmt.Errorf("invalid size format: %s", bc.MaxSize)
	}

	switch unit {
	case "B", "b":
		bc.MaxSizeBytes = size
	case "KB", "kb", "K", "k":
		bc.MaxSizeBytes = size * 1024
	case "MB", "mb", "M", "m":
		bc.MaxSizeBytes = size * 1024 * 1024
	case "GB", "gb", "G", "g":
		bc.MaxSizeBytes = size * 1024 * 1024 * 1024
	case "TB", "tb", "T", "t":
		bc.MaxSizeBytes = size * 1024 * 1024 * 1024 * 1024
	default:
		return fmt.Errorf("unsupported unit: %s", unit)
	}

	return nil
}
