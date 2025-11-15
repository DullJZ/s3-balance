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

	if h.metrics != nil {
		h.metrics.RecordBackendOperation(b.Config.Name, string(category))
	}

	var disabled bool

	if h.storage != nil {
		newCount, err := h.storage.IncrementBucketOperation(b.Config.Name, string(category))
		if err != nil {
			log.Printf("failed to persist backend operation count for bucket %s: %v", b.Config.Name, err)
			disabled = b.RecordOperation(category)
		} else {
			disabled = b.SetOperationCount(category, newCount)
		}
	} else {
		disabled = b.RecordOperation(category)
	}

	if disabled {
		log.Printf("Bucket %s disabled after exceeding %s-type operation limit", b.Config.Name, category)
	}
}
