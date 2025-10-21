package api

import (
	"log"

	"github.com/DullJZ/s3-balance/internal/bucket"
)

// recordBackendOperation increments backend operation counters and disables the bucket if limits are exceeded.
func (h *S3Handler) recordBackendOperation(b *bucket.BucketInfo, category bucket.OperationCategory) {
	if b == nil {
		return
	}
	if disabled := b.RecordOperation(category); disabled {
		log.Printf("Bucket %s disabled after exceeding %s-type operation limit", b.Config.Name, category)
	}
}
