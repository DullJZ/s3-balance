package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/DullJZ/s3-validate/pkg/s3validate"
)

// S3SignatureConfig controls S3 signature validation.
type S3SignatureConfig struct {
	Required      func() bool
	Credentials   func() (string, string)
	OnError       func(http.ResponseWriter, *http.Request, string, string, string)
	SignatureHost func() string // 用于签名验证的Host（为空则使用请求的Host）
}

// credentialsProvider implements s3validate.CredentialsProvider interface.
type credentialsProvider struct {
	getCredentials func() (string, string)
}

func (p *credentialsProvider) SecretKey(ctx context.Context, accessKey string) (string, error) {
	expectedAccessKey, secretKey := p.getCredentials()
	if accessKey != expectedAccessKey {
		return "", fmt.Errorf("invalid access key")
	}
	return secretKey, nil
}

// S3Signature enforces AWS Signature V4 authentication when required.
func S3Signature(cfg S3SignatureConfig) func(http.Handler) http.Handler {
	verifier := &s3validate.Verifier{
		Credentials: &credentialsProvider{
			getCredentials: cfg.Credentials,
		},
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			required := false
			if cfg.Required != nil {
				required = cfg.Required()
			}
			if !required {
				next.ServeHTTP(w, r)
				return
			}

			// 如果配置了签名验证的Host，覆盖请求的Host
			if cfg.SignatureHost != nil {
				if signatureHost := cfg.SignatureHost(); signatureHost != "" {
					r.Host = signatureHost
				}
			}

			result, err := verifier.Verify(r.Context(), r)
			if err != nil {
				invokeOnError(w, r, cfg, "SignatureDoesNotMatch", err.Error())
				return
			}

			// Store verification result in context for potential use by handlers
			_ = result

			next.ServeHTTP(w, r)
		})
	}
}

func invokeOnError(w http.ResponseWriter, r *http.Request, cfg S3SignatureConfig, code, message string) {
	if cfg.OnError != nil {
		cfg.OnError(w, r, code, message, "")
		return
	}
	http.Error(w, message, http.StatusForbidden)
}

// TokenAuthMiddleware 创建Token认证中间件，用于管理API
func TokenAuthMiddleware(validToken string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 从Authorization头中提取token
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error": "missing authorization header"}`, http.StatusUnauthorized)
				return
			}

			// 支持两种格式：
			// 1. Bearer <token>
			// 2. <token>
			token := authHeader
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}

			// 验证token
			if token != validToken {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error": "invalid token"}`, http.StatusUnauthorized)
				return
			}

			// 继续处理请求
			next.ServeHTTP(w, r)
		})
	}
}
