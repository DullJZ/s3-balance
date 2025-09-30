package middleware

import (
	"net"
	"net/http"
	"strings"
)

// VirtualHostConfig controls host-style bucket resolution.
type VirtualHostConfig struct {
	Enabled      func() bool
	BucketExists func(string) bool
}

// VirtualHost rewrites host-style requests (bucket.example.com) into path-style paths.
func VirtualHost(cfg VirtualHostConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			enabled := false
			if cfg.Enabled != nil {
				enabled = cfg.Enabled()
			}
			if !enabled {
				next.ServeHTTP(w, r)
				return
			}

			bucket := bucketFromHost(r.Host)
			if bucket == "" {
				next.ServeHTTP(w, r)
				return
			}

			if cfg.BucketExists != nil && !cfg.BucketExists(bucket) {
				next.ServeHTTP(w, r)
				return
			}

			if strings.HasPrefix(r.URL.Path, "/"+bucket) {
				next.ServeHTTP(w, r)
				return
			}

			newPath := "/" + bucket
			if r.URL.Path != "/" {
				newPath += r.URL.Path
			}

			clone := r.Clone(r.Context())
			clone.URL.Path = newPath
			clone.RequestURI = newPath

			next.ServeHTTP(w, clone)
		})
	}
}

func bucketFromHost(host string) string {
	if host == "" {
		return ""
	}

	hostname := host
	if strings.Contains(host, ":") {
		h, _, err := net.SplitHostPort(host)
		if err == nil {
			hostname = h
		}
	}

	parts := strings.Split(hostname, ".")
	if len(parts) == 0 {
		return ""
	}

	return parts[0]
}
