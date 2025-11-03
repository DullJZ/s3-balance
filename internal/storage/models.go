package storage

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

// Object 对象信息模型
type Object struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	Key         string         `gorm:"size:512;not null" json:"key"`
	BucketName  string         `gorm:"index;size:255;not null" json:"bucket_name"`
	Size        int64          `gorm:"not null;default:0" json:"size"`
	Metadata    JSON           `gorm:"type:json" json:"metadata,omitempty"`
	ContentType string         `gorm:"size:128" json:"content_type,omitempty"`
	ETag        string         `gorm:"size:128" json:"etag,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 指定表名
func (Object) TableName() string {
	return "objects"
}

// BucketStats 存储桶统计信息模型
type BucketStats struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	BucketName      string    `gorm:"uniqueIndex;size:255;not null" json:"bucket_name"`
	ObjectCount     int64     `gorm:"not null;default:0" json:"object_count"`
	TotalSize       int64     `gorm:"not null;default:0" json:"total_size"`
	OperationCountA int64     `gorm:"not null;default:0" json:"operation_count_a"`
	OperationCountB int64     `gorm:"not null;default:0" json:"operation_count_b"`
	LastCheckedAt   time.Time `json:"last_checked_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TableName 指定表名
func (BucketStats) TableName() string {
	return "bucket_stats"
}

// BucketMonthlyStats 存储桶月度统计信息模型
type BucketMonthlyStats struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	BucketName      string    `gorm:"uniqueIndex:idx_bucket_month;size:255;not null" json:"bucket_name"`
	Year            int       `gorm:"uniqueIndex:idx_bucket_month;not null" json:"year"`
	Month           int       `gorm:"uniqueIndex:idx_bucket_month;not null" json:"month"`
	OperationCountA int64     `gorm:"not null;default:0" json:"operation_count_a"`
	OperationCountB int64     `gorm:"not null;default:0" json:"operation_count_b"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TableName 指定表名
func (BucketMonthlyStats) TableName() string {
	return "bucket_monthly_stats"
}

// VirtualBucketMapping 虚拟存储桶文件级映射模型
type VirtualBucketMapping struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	VirtualBucketName string    `gorm:"index;size:255;not null" json:"virtual_bucket_name"`
	ObjectKey         string    `gorm:"index;size:512;not null" json:"object_key"`
	RealBucketName    string    `gorm:"index;size:255;not null" json:"real_bucket_name"`
	CreatedAt         time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt         time.Time `gorm:"not null" json:"updated_at"`
}

// TableName 指定表名
func (VirtualBucketMapping) TableName() string {
	return "virtual_bucket_mappings"
}

// UploadSession 上传会话模型（用于跟踪分片上传）
type UploadSession struct {
	ID             uint           `gorm:"primaryKey" json:"id"`
	UploadID       string         `gorm:"uniqueIndex;size:512;not null" json:"upload_id"` // 增加到512字符以支持长uploadID
	Key            string         `gorm:"index;size:512;not null" json:"key"`
	BucketName     string         `gorm:"index;size:255;not null" json:"bucket_name"`
	CompletedParts int            `gorm:"not null;default:0" json:"completed_parts"`
	Size           int64          `gorm:"not null;default:0" json:"size"`
	Status         string         `gorm:"size:32;not null;default:'pending'" json:"status"` // pending, completed, aborted
	ExpiresAt      time.Time      `gorm:"index" json:"expires_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 指定表名
func (UploadSession) TableName() string {
	return "upload_sessions"
}

// AccessLog 访问日志模型
type AccessLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Action       string    `gorm:"index;size:32;not null" json:"action"` // upload, download, delete
	Key          string    `gorm:"index;size:512;not null" json:"key"`
	BucketName   string    `gorm:"index;size:255" json:"bucket_name"`
	Size         int64     `gorm:"default:0" json:"size"`
	ClientIP     string    `gorm:"size:64" json:"client_ip"`
	UserAgent    string    `gorm:"size:512" json:"user_agent"`
	Host         string    `gorm:"size:255" json:"host"`
	Success      bool      `gorm:"not null" json:"success"`
	ErrorMsg     string    `gorm:"type:text" json:"error_msg,omitempty"`
	ResponseTime int64     `gorm:"default:0" json:"response_time"` // 响应时间（毫秒）
	CreatedAt    time.Time `gorm:"index" json:"created_at"`
}

// TableName 指定表名
func (AccessLog) TableName() string {
	return "access_logs"
}

// JSON 自定义JSON类型，用于存储元数据
type JSON map[string]interface{}

// Value 实现driver.Valuer接口
func (j JSON) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

// Scan 实现sql.Scanner接口
func (j *JSON) Scan(value interface{}) error {
	if value == nil {
		*j = make(map[string]interface{})
		return nil
	}

	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		data = []byte("{}")
	}

	return json.Unmarshal(data, j)
}

// BeforeCreate GORM钩子 - 创建前
func (o *Object) BeforeCreate(tx *gorm.DB) error {
	if o.Metadata == nil {
		o.Metadata = make(JSON)
	}
	return nil
}

// BeforeCreate GORM钩子 - 创建前设置过期时间
func (u *UploadSession) BeforeCreate(tx *gorm.DB) error {
	if u.ExpiresAt.IsZero() {
		u.ExpiresAt = time.Now().Add(24 * time.Hour) // 默认24小时过期
	}
	return nil
}

// IsExpired 检查上传会话是否过期
func (u *UploadSession) IsExpired() bool {
	return time.Now().After(u.ExpiresAt)
}

// ObjectFilter 对象查询过滤器
type ObjectFilter struct {
	Key        string
	BucketName string
	Prefix     string
	MinSize    int64
	MaxSize    int64
	StartTime  time.Time
	EndTime    time.Time
	Limit      int
	Offset     int
}

// AccessLogFilter 访问日志查询过滤器
type AccessLogFilter struct {
	Action     string
	Key        string
	BucketName string
	ClientIP   string
	Success    *bool
	StartTime  time.Time
	EndTime    time.Time
	Limit      int
	Offset     int
}
