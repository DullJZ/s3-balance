package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DullJZ/s3-balance/internal/api"
	"github.com/DullJZ/s3-balance/internal/balancer"
	"github.com/DullJZ/s3-balance/internal/bucket"
	"github.com/DullJZ/s3-balance/internal/config"
	"github.com/DullJZ/s3-balance/internal/database"
	"github.com/DullJZ/s3-balance/internal/metrics"
	"github.com/DullJZ/s3-balance/internal/middleware"
	"github.com/DullJZ/s3-balance/internal/scheduler"
	"github.com/DullJZ/s3-balance/internal/storage"
	"github.com/DullJZ/s3-balance/pkg/presigner"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	// 解析命令行参数
	var configFile string
	flag.StringVar(&configFile, "config", "config/config.yaml", "Path to configuration file")
	flag.Parse()

	// 创建配置管理器
	configManager, err := config.NewManager(configFile)
	if err != nil {
		log.Fatalf("Failed to create config manager: %v", err)
	}
	defer configManager.Close()

	// 获取初始配置
	cfg := configManager.GetConfig()

	// 初始化数据库
	if err := database.Initialize(&cfg.Database); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// 创建存储服务
	storageService := storage.NewService(database.GetDB())

	// 创建指标服务
	metricsService := metrics.New()

	// 创建存储桶管理器
	bucketManager, err := bucket.NewManager(cfg, metricsService, storageService)
	if err != nil {
		log.Fatalf("Failed to create bucket manager: %v", err)
	}

	// 启动存储桶管理器（健康检查和统计更新）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bucketManager.Start(ctx)

	// 创建负载均衡器
	lb, err := balancer.NewBalancer(bucketManager, &cfg.Balancer)
	if err != nil {
		log.Fatalf("Failed to create balancer: %v", err)
	}

	// 设置指标服务
	lb.SetMetrics(metricsService)

	// 创建预签名URL生成器
	signer := presigner.NewPresigner(
		15*time.Minute, // 上传URL有效期
		60*time.Minute, // 下载URL有效期
	)

	// 启动定期清理过期上传会话的任务
	startSessionCleaner(ctx, storageService)

	// 启动月度统计归档任务（每小时检查一次）
	monthlyArchiver := scheduler.NewMonthlyArchiver(storageService, 1*time.Hour)
	monthlyArchiver.Start()
	defer monthlyArchiver.Stop()

	// 创建S3兼容API处理器
	s3Handler := api.NewS3Handler(
		bucketManager,
		lb,
		signer,
		storageService,
		cfg.S3API.AccessKey,
		cfg.S3API.SecretKey,
		metricsService,
		cfg.S3API.ProxyMode,
		cfg.S3API.AuthRequired,
		cfg.S3API.VirtualHost,
		cfg.S3API.Host,
	)

	// 注册配置热更新回调
	configManager.OnConfigChange(func(newConfig *config.Config) {
		log.Println("Configuration changed, updating components...")

		// 更新bucket manager配置
		if err := bucketManager.UpdateConfig(newConfig); err != nil {
			log.Printf("Failed to update bucket manager config: %v", err)
		}

		// 更新负载均衡器配置
		if err := lb.UpdateStrategy(newConfig.Balancer.Strategy); err != nil {
			log.Printf("Failed to update load balancer strategy: %v", err)
		}

		// 更新S3 API设置
		s3Handler.UpdateS3APIConfig(&newConfig.S3API)

		log.Println("Components updated successfully")
	})

	// 设置路由
	router := mux.NewRouter()

	// 添加指标端点
	if cfg.Metrics.Enabled {
		router.Path(cfg.Metrics.Path).Handler(promhttp.Handler())
		log.Printf("Metrics server enabled at %s", cfg.Metrics.Path)
	}

	// 注册管理API路由（如果启用）
	// 必须在S3路由之前注册，因为S3路由使用 /{bucket} 通配符会匹配所有路径
	if cfg.API.Enabled {
		log.Println("Management API enabled")
		adminHandler := api.NewAdminHandler(bucketManager, lb, cfg, configManager)
		statsHandler := api.NewStatsHandler(storageService)

		// 创建子路由器并应用中间件
		apiRouter := router.PathPrefix("/api").Subrouter()
		apiRouter.Use(corsMiddleware) // 先应用 CORS 中间件，处理 OPTIONS 预检请求
		apiRouter.Use(middleware.TokenAuthMiddleware(cfg.API.Token))
		adminHandler.RegisterRoutes(apiRouter)
		statsHandler.RegisterRoutes(apiRouter)

		log.Printf("Management API endpoints available at /api/*")
	}

	// 运行在S3兼容模式
	log.Println("Running in S3-compatible mode")
	s3Handler.RegisterS3Routes(router)

	// 添加CORS中间件
	router.Use(corsMiddleware)

	// 添加日志中间件
	router.Use(loggingMiddleware)

	// 创建HTTP服务器
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	// 启动服务器
	go func() {
		log.Printf("Starting S3 Balance Service on %s", srv.Addr)
		log.Printf("Load balancing strategy: %s", cfg.Balancer.Strategy)
		log.Printf("Managed buckets: %d", len(cfg.Buckets))

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	// 优雅关闭
	log.Println("Shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	// 停止存储桶管理器
	bucketManager.Stop()
	cancel()

	log.Println("Server stopped")
}

// CORS中间件
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// 日志中间件
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// 包装ResponseWriter以捕获状态码
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(wrapped, r)

		log.Printf(
			"[%s] %s %s %d %v",
			r.RemoteAddr,
			r.Method,
			r.RequestURI,
			wrapped.statusCode,
			time.Since(start),
		)
	})
}

// responseWriter 包装器用于捕获状态码
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// startSessionCleaner 启动定期清理过期会话的任务
func startSessionCleaner(ctx context.Context, storageService *storage.Service) {
	go func() {
		// 初始延迟，避免启动时立即执行
		time.Sleep(1 * time.Minute)

		// 每小时清理一次过期的上传会话
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				log.Println("Stopping session cleaner")
				return
			case <-ticker.C:
				log.Println("Cleaning expired upload sessions...")
				if err := storageService.CleanExpiredSessions(); err != nil {
					log.Printf("Failed to clean expired sessions: %v", err)
				} else {
					log.Println("Successfully cleaned expired upload sessions")
				}

				// 同时中止在S3存储桶中过期的分片上传
				cleanupS3MultipartUploads(ctx, storageService)
			}
		}
	}()
}

// cleanupS3MultipartUploads 清理S3存储桶中过期的分片上传
func cleanupS3MultipartUploads(_ context.Context, storageService *storage.Service) {
	// 获取所有过期的会话
	sessions, err := storageService.GetPendingUploadSessions("", "", "", 0)
	if err != nil {
		log.Printf("Failed to get pending sessions for cleanup: %v", err)
		return
	}

	for _, session := range sessions {
		if session.IsExpired() {
			log.Printf("Found expired session: uploadID=%s, key=%s", session.UploadID, session.Key)
			// 此处可以添加调用S3 AbortMultipartUpload的逻辑
			// 但需要知道对应的真实存储桶信息
		}
	}
}
