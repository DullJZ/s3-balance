package api

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/DullJZ/s3-balance/internal/storage"
)

// sendXMLResponse 发送XML响应
func (h *S3Handler) sendXMLResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(statusCode)

	encoder := xml.NewEncoder(w)
	encoder.Indent("", "  ")

	// 写入XML声明
	w.Write([]byte(xml.Header))

	if err := encoder.Encode(data); err != nil {
		// 如果编码失败，记录错误
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// sendS3Error 发送S3错误响应
func (h *S3Handler) sendS3Error(w http.ResponseWriter, code string, message string, resource string) {
	errorResp := ErrorResponse{
		Code:      code,
		Message:   message,
		Resource:  resource,
		RequestID: fmt.Sprintf("%d", time.Now().UnixNano()),
	}

	w.Header().Set("X-Amz-Error-Code", code)
	w.Header().Set("X-Amz-Error-Message", message)

	statusCode := http.StatusBadRequest
	switch code {
	case "NoSuchBucket", "NoSuchKey":
		statusCode = http.StatusNotFound
	case "BucketAlreadyExists":
		statusCode = http.StatusConflict
	case "InvalidAccessKeyId", "SignatureDoesNotMatch", "AccessDenied":
		statusCode = http.StatusForbidden
	case "InternalError":
		statusCode = http.StatusInternalServerError
	case "InsufficientStorage":
		statusCode = http.StatusInsufficientStorage
	}

	h.sendXMLResponse(w, statusCode, errorResp)
}

// setObjectHeaders 设置对象响应头
func (h *S3Handler) setObjectHeaders(w http.ResponseWriter, obj *storage.Object) {
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	w.Header().Set("Last-Modified", obj.UpdatedAt.Format(http.TimeFormat))
	w.Header().Set("ETag", fmt.Sprintf("\"%x\"", obj.ID))
	if obj.ContentType != "" {
		w.Header().Set("Content-Type", obj.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
}
