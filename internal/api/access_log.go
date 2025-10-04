package api

import (
	"bufio"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

func (h *S3Handler) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.storage == nil {
			next.ServeHTTP(w, r)
			return
		}

		start := time.Now()
		lrw := newLoggingResponseWriter(w)
		next.ServeHTTP(lrw, r)

		vars := mux.Vars(r)
		bucket := vars["bucket"]
		key := vars["key"]
		action := determineAccessAction(r, bucket, key)
		if action == "" && bucket == "" && key == "" {
			return
		}

		success := lrw.statusCode < 400
		errMsg := ""
		if !success {
			if code := lrw.Header().Get("X-Amz-Error-Code"); code != "" {
				errMsg = code
			} else {
				errMsg = http.StatusText(lrw.statusCode)
			}
		}

		size := calculateLogSize(r, lrw)
		duration := time.Since(start)
		h.recordAccessLog(r, action, bucket, key, size, success, errMsg, duration)
	})
}

func (h *S3Handler) recordAccessLog(r *http.Request, action, bucket, key string, size int64, success bool, errMsg string, duration time.Duration) {
	clientIP := extractClientIP(r)
	userAgent := r.UserAgent()
	// 异步记录日志，避免阻塞请求响应
	go func() {
		if err := h.storage.RecordAccessLog(action, key, bucket, clientIP, userAgent, size, success, errMsg, duration.Milliseconds()); err != nil {
			log.Printf("failed to record access log: %v", err)
		}
	}()
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func newLoggingResponseWriter(w http.ResponseWriter) *loggingResponseWriter {
	return &loggingResponseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
	}
}

func (lrw *loggingResponseWriter) WriteHeader(statusCode int) {
	lrw.statusCode = statusCode
	lrw.ResponseWriter.WriteHeader(statusCode)
}

func (lrw *loggingResponseWriter) Write(p []byte) (int, error) {
	n, err := lrw.ResponseWriter.Write(p)
	lrw.bytesWritten += int64(n)
	return n, err
}

func (lrw *loggingResponseWriter) Flush() {
	if flusher, ok := lrw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (lrw *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := lrw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, errors.New("http.Hijacker not supported")
}

func (lrw *loggingResponseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := lrw.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return http.ErrNotSupported
}

func determineAccessAction(r *http.Request, bucket, key string) string {
	method := r.Method
	query := r.URL.Query()

	if bucket == "" && key == "" {
		if method == http.MethodGet {
			return "list_buckets"
		}
		return strings.ToLower(method)
	}

	if key == "" {
		switch method {
		case http.MethodGet:
			if _, ok := query["uploads"]; ok {
				return "list_multipart_uploads"
			}
			return "list_objects"
		case http.MethodHead:
			return "head_bucket"
		case http.MethodPut:
			return "create_bucket"
		case http.MethodDelete:
			return "delete_bucket"
		}
		return strings.ToLower(method)
	}

	switch method {
	case http.MethodGet:
		if _, ok := query["uploads"]; ok {
			return "list_multipart_uploads"
		}
		if _, ok := query["uploadId"]; ok {
			return "list_multipart_parts"
		}
		return "download_object"
	case http.MethodHead:
		return "head_object"
	case http.MethodPut:
		if _, hasUploadID := query["uploadId"]; hasUploadID {
			if _, hasPart := query["partNumber"]; hasPart {
				return "upload_part"
			}
		}
		return "upload_object"
	case http.MethodDelete:
		if _, ok := query["uploadId"]; ok {
			return "abort_multipart_upload"
		}
		return "delete_object"
	case http.MethodPost:
		if _, ok := query["uploads"]; ok {
			return "initiate_multipart_upload"
		}
		if _, ok := query["uploadId"]; ok {
			return "complete_multipart_upload"
		}
	}

	return strings.ToLower(method)
}

func calculateLogSize(r *http.Request, lrw *loggingResponseWriter) int64 {
	switch r.Method {
	case http.MethodPut, http.MethodPost:
		// 对于上传请求，优先使用请求体大小（如果可用）
		if r.ContentLength > 0 {
			return r.ContentLength
		}
		// 对于分块传输（chunked），ContentLength 为 -1，返回 0
		return 0
	}
	// 对于 GET/HEAD 等请求，返回响应体大小
	return lrw.bytesWritten
}

func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
