package api

import (
	"net"
	"net/http"
	"strings"
)

// virtualHostMiddleware 支持根据 Host 头推断存储桶名称
func (h *S3Handler) virtualHostMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.virtualHost {
			next.ServeHTTP(w, r)
			return
		}

		bucketName := h.bucketFromHost(r.Host)
		if bucketName == "" {
			next.ServeHTTP(w, r)
			return
		}

		// 若路由中已包含桶名称则无需改写
		if strings.HasPrefix(r.URL.Path, "/"+bucketName) {
			next.ServeHTTP(w, r)
			return
		}

		// 确保桶存在
		if _, ok := h.bucketManager.GetBucket(bucketName); !ok {
			next.ServeHTTP(w, r)
			return
		}

		newPath := "/" + bucketName
		if r.URL.Path != "/" {
			newPath += r.URL.Path
		}

		clone := r.Clone(r.Context())
		clone.URL.Path = newPath
		clone.RequestURI = newPath

		next.ServeHTTP(w, clone)
	})
}

func (h *S3Handler) bucketFromHost(host string) string {
	if host == "" {
		return ""
	}

	cleanHost := host
	if strings.Contains(host, ":") {
		hostname, _, err := net.SplitHostPort(host)
		if err == nil {
			cleanHost = hostname
		}
	}

	parts := strings.Split(cleanHost, ".")
	if len(parts) == 0 {
		return ""
	}

	candidate := parts[0]
	if candidate == "" {
		return ""
	}

	return candidate
}
