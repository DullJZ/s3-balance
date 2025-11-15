package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/DullJZ/s3-balance/internal/config"
	"github.com/DullJZ/s3-balance/internal/storage"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	_ "modernc.org/sqlite"
)

// DB 全局数据库连接
var DB *gorm.DB

// Initialize 初始化数据库连接
func Initialize(cfg *config.DatabaseConfig) error {
	var err error

	// 设置日志级别
	logLevel := getLogLevel(cfg.LogLevel)

	// GORM配置
	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
		NowFunc: func() time.Time {
			return time.Now().Local()
		},
		QueryFields: true,
	}

	// 根据数据库类型创建连接
	switch cfg.Type {
	case "sqlite":
		DB, err = connectSQLite(cfg.DSN, gormConfig)
	case "mysql":
		DB, err = connectMySQL(cfg.DSN, gormConfig)
	case "postgres", "postgresql":
		DB, err = connectPostgreSQL(cfg.DSN, gormConfig)
	default:
		return fmt.Errorf("unsupported database type: %s", cfg.Type)
	}

	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// 获取底层SQL数据库连接
	sqlDB, err := DB.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}

	// 设置连接池参数
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetime) * time.Second)

	// 测试连接
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	// 自动迁移
	if cfg.AutoMigrate {
		if err := AutoMigrate(); err != nil {
			return fmt.Errorf("failed to auto migrate: %w", err)
		}
	}

	log.Printf("Successfully connected to %s database", cfg.Type)
	return nil
}

// connectSQLite 连接SQLite数据库（使用modernc.org/sqlite，支持非CGO）
func connectSQLite(dsn string, gormConfig *gorm.Config) (*gorm.DB, error) {
	// 创建数据目录（如果不存在）
	dir := filepath.Dir(dsn)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
		}
	}

	// 添加SQLite特定参数
	if dsn != ":memory:" {
		dsn = fmt.Sprintf("%s?_journal_mode=WAL&_timeout=5000&_synchronous=NORMAL&_cache_size=10000", dsn)
	}

	// 使用modernc.org/sqlite驱动（纯Go实现，无需CGO）
	dialector := sqlite.Dialector{
		DriverName: "sqlite",
		DSN:        dsn,
	}

	return gorm.Open(dialector, gormConfig)
} // connectMySQL 连接MySQL数据库
func connectMySQL(dsn string, gormConfig *gorm.Config) (*gorm.DB, error) {
	// MySQL DSN示例: user:password@tcp(localhost:3306)/dbname?charset=utf8mb4&parseTime=True&loc=Local

	// 如果DSN中没有指定字符集，添加默认字符集
	if dsn != "" {
		dsn = ensureMySQLParams(dsn)
	}

	return gorm.Open(mysql.Open(dsn), gormConfig)
}

// connectPostgreSQL 连接PostgreSQL数据库
func connectPostgreSQL(dsn string, gormConfig *gorm.Config) (*gorm.DB, error) {
	// PostgreSQL DSN示例: host=localhost user=user password=password dbname=mydb port=5432 sslmode=disable TimeZone=Asia/Shanghai

	return gorm.Open(postgres.Open(dsn), gormConfig)
}

// ensureMySQLParams 确保MySQL DSN包含必要的参数
func ensureMySQLParams(dsn string) string {
	params := map[string]string{
		"charset":   "utf8mb4",
		"parseTime": "True",
		"loc":       "Local",
	}

	separator := "?"
	if len(dsn) > 0 && dsn[len(dsn)-1] == '?' {
		separator = ""
	} else if contains(dsn, "?") {
		separator = "&"
	}

	for key, value := range params {
		if !contains(dsn, key+"=") {
			dsn = fmt.Sprintf("%s%s%s=%s", dsn, separator, key, value)
			separator = "&"
		}
	}

	return dsn
}

// contains 检查字符串是否包含子串
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// getLogLevel 获取GORM日志级别
func getLogLevel(level string) logger.LogLevel {
	switch level {
	case "silent":
		return logger.Silent
	case "error":
		return logger.Error
	case "warn", "warning":
		return logger.Warn
	case "info":
		return logger.Info
	default:
		return logger.Warn
	}
}

// AutoMigrate 自动迁移数据库表
func AutoMigrate() error {
	models := []interface{}{
		&storage.Object{},
		&storage.BucketStats{},
		&storage.BucketMonthlyStats{},
		&storage.UploadSession{},
		&storage.AccessLog{},
		&storage.VirtualBucketMapping{},
	}

	for _, model := range models {
		if err := DB.AutoMigrate(model); err != nil {
			return fmt.Errorf("failed to migrate %T: %w", model, err)
		}
	}

	log.Println("Database migration completed successfully")
	return nil
}

// Close 关闭数据库连接
func Close() error {
	if DB != nil {
		sqlDB, err := DB.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}

// HealthCheck 健康检查
func HealthCheck() error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return sqlDB.PingContext(ctx)
}

// Transaction 执行事务
func Transaction(fn func(*gorm.DB) error) error {
	return DB.Transaction(fn)
}

// GetDB 获取数据库连接
func GetDB() *gorm.DB {
	return DB
}
