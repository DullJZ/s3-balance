package presigner

import (
	"context"
	"fmt"
	"time"

	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Presigner 预签名URL生成器
type Presigner struct {
	uploadExpiry   time.Duration
	downloadExpiry time.Duration
}

// NewPresigner 创建新的预签名URL生成器
func NewPresigner(uploadExpiry, downloadExpiry time.Duration) *Presigner {
	// 设置默认值
	if uploadExpiry == 0 {
		uploadExpiry = 15 * time.Minute
	}
	if downloadExpiry == 0 {
		downloadExpiry = 60 * time.Minute
	}

	return &Presigner{
		uploadExpiry:   uploadExpiry,
		downloadExpiry: downloadExpiry,
	}
}

// UploadURL 生成上传预签名URL
type UploadURL struct {
	URL        string            `json:"url"`
	Method     string            `json:"method"`
	Headers    map[string]string `json:"headers,omitempty"`
	Expiry     time.Time        `json:"expiry"`
	BucketName string           `json:"bucket_name"`
	Key        string           `json:"key"`
}

// GenerateUploadURL 生成上传预签名URL
func (p *Presigner) GenerateUploadURL(ctx context.Context, bucket *bucket.BucketInfo, key string, contentType string, metadata map[string]string) (*UploadURL, error) {
	presignClient := s3.NewPresignClient(bucket.Client)

	// 构建PutObject请求
	putObjectInput := &s3.PutObjectInput{
		Bucket: aws.String(bucket.Config.Name),
		Key:    aws.String(key),
	}

	// 设置Content-Type
	if contentType != "" {
		putObjectInput.ContentType = aws.String(contentType)
	}

	// 设置元数据
	if len(metadata) > 0 {
		putObjectInput.Metadata = metadata
	}

	// 生成预签名URL
	presignRequest, err := presignClient.PresignPutObject(ctx, putObjectInput, func(opts *s3.PresignOptions) {
		opts.Expires = p.uploadExpiry
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate upload presigned URL: %w", err)
	}

	// 转换Headers为map[string]string
	headers := make(map[string]string)
	for k, v := range presignRequest.SignedHeader {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	
	return &UploadURL{
		URL:        presignRequest.URL,
		Method:     presignRequest.Method,
		Headers:    headers,
		Expiry:     time.Now().Add(p.uploadExpiry),
		BucketName: bucket.Config.Name,
		Key:        key,
	}, nil
}

// DownloadURL 生成下载预签名URL
type DownloadURL struct {
	URL        string    `json:"url"`
	Method     string    `json:"method"`
	Expiry     time.Time `json:"expiry"`
	BucketName string    `json:"bucket_name"`
	Key        string    `json:"key"`
}

// GenerateDownloadURL 生成下载预签名URL
func (p *Presigner) GenerateDownloadURL(ctx context.Context, bucket *bucket.BucketInfo, key string) (*DownloadURL, error) {
	presignClient := s3.NewPresignClient(bucket.Client)

	// 构建GetObject请求
	getObjectInput := &s3.GetObjectInput{
		Bucket: aws.String(bucket.Config.Name),
		Key:    aws.String(key),
	}

	// 生成预签名URL
	presignRequest, err := presignClient.PresignGetObject(ctx, getObjectInput, func(opts *s3.PresignOptions) {
		opts.Expires = p.downloadExpiry
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate download presigned URL: %w", err)
	}

	return &DownloadURL{
		URL:        presignRequest.URL,
		Method:     presignRequest.Method,
		Expiry:     time.Now().Add(p.downloadExpiry),
		BucketName: bucket.Config.Name,
		Key:        key,
	}, nil
}

// DeleteURL 生成删除预签名URL
type DeleteURL struct {
	URL        string    `json:"url"`
	Method     string    `json:"method"`
	Expiry     time.Time `json:"expiry"`
	BucketName string    `json:"bucket_name"`
	Key        string    `json:"key"`
}

// GenerateDeleteURL 生成删除预签名URL
func (p *Presigner) GenerateDeleteURL(ctx context.Context, bucket *bucket.BucketInfo, key string) (*DeleteURL, error) {
	presignClient := s3.NewPresignClient(bucket.Client)

	// 构建DeleteObject请求
	deleteObjectInput := &s3.DeleteObjectInput{
		Bucket: aws.String(bucket.Config.Name),
		Key:    aws.String(key),
	}

	// 生成预签名URL
	presignRequest, err := presignClient.PresignDeleteObject(ctx, deleteObjectInput, func(opts *s3.PresignOptions) {
		opts.Expires = 5 * time.Minute // 删除操作的URL有效期较短
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate delete presigned URL: %w", err)
	}

	return &DeleteURL{
		URL:        presignRequest.URL,
		Method:     presignRequest.Method,
		Expiry:     time.Now().Add(5 * time.Minute),
		BucketName: bucket.Config.Name,
		Key:        key,
	}, nil
}

// MultipartUploadURLs 分片上传预签名URLs
type MultipartUploadURLs struct {
	UploadID   string              `json:"upload_id"`
	PartURLs   map[int]string     `json:"part_urls"`
	BucketName string            `json:"bucket_name"`
	Key        string            `json:"key"`
	Expiry     time.Time         `json:"expiry"`
}

// GenerateMultipartUploadURLs 生成分片上传预签名URLs
func (p *Presigner) GenerateMultipartUploadURLs(ctx context.Context, bucket *bucket.BucketInfo, key string, partCount int) (*MultipartUploadURLs, error) {
	// 初始化分片上传
	createResp, err := bucket.Client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket.Config.Name),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create multipart upload: %w", err)
	}

	presignClient := s3.NewPresignClient(bucket.Client)
	partURLs := make(map[int]string)

	// 为每个分片生成预签名URL
	for i := 1; i <= partCount; i++ {
		uploadPartInput := &s3.UploadPartInput{
			Bucket:     aws.String(bucket.Config.Name),
			Key:        aws.String(key),
			UploadId:   createResp.UploadId,
			PartNumber: aws.Int32(int32(i)),
		}

		presignRequest, err := presignClient.PresignUploadPart(ctx, uploadPartInput, func(opts *s3.PresignOptions) {
			opts.Expires = p.uploadExpiry
		})
		if err != nil {
			// 如果失败，中止分片上传
			bucket.Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(bucket.Config.Name),
				Key:      aws.String(key),
				UploadId: createResp.UploadId,
			})
			return nil, fmt.Errorf("failed to generate part %d presigned URL: %w", i, err)
		}

		partURLs[i] = presignRequest.URL
	}

	// 注意：CompleteMultipartUpload 和 AbortMultipartUpload 需要在客户端直接调用
	// 因为它们需要提供额外的参数（如Parts列表），不适合预签名
	
	return &MultipartUploadURLs{
		UploadID:   *createResp.UploadId,
		PartURLs:   partURLs,
		BucketName: bucket.Config.Name,
		Key:        key,
		Expiry:     time.Now().Add(p.uploadExpiry),
	}, nil
}
