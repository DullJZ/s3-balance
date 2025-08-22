package presigner

import (
	"context"
	"fmt"

	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// CompletedPart 已完成的分片信息
type CompletedPart struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
}

// CompleteMultipartUpload 完成分片上传
func CompleteMultipartUpload(ctx context.Context, bucket *bucket.BucketInfo, key, uploadID string, parts []CompletedPart) error {
	// 转换为AWS SDK格式
	var completedParts []types.CompletedPart
	for _, part := range parts {
		completedParts = append(completedParts, types.CompletedPart{
			PartNumber: aws.Int32(part.PartNumber),
			ETag:       aws.String(part.ETag),
		})
	}

	// 完成分片上传
	_, err := bucket.Client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(bucket.Config.Name),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to complete multipart upload: %w", err)
	}

	return nil
}

// AbortMultipartUpload 中止分片上传
func AbortMultipartUpload(ctx context.Context, bucket *bucket.BucketInfo, key, uploadID string) error {
	_, err := bucket.Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucket.Config.Name),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		return fmt.Errorf("failed to abort multipart upload: %w", err)
	}

	return nil
}

// ListParts 列出已上传的分片
func ListParts(ctx context.Context, bucket *bucket.BucketInfo, key, uploadID string) ([]types.Part, error) {
	var allParts []types.Part
	var nextPartNumberMarker *string

	for {
		output, err := bucket.Client.ListParts(ctx, &s3.ListPartsInput{
			Bucket:           aws.String(bucket.Config.Name),
			Key:              aws.String(key),
			UploadId:         aws.String(uploadID),
			PartNumberMarker: nextPartNumberMarker,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list parts: %w", err)
		}

		allParts = append(allParts, output.Parts...)

		if output.IsTruncated == nil || !*output.IsTruncated {
			break
		}
		nextPartNumberMarker = output.NextPartNumberMarker
	}

	return allParts, nil
}
