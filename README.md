# S3 Balance

S3 Balance 是一个用 Go 编写的 S3 兼容负载均衡器，可在多套对象存储之间自动选择最佳目标桶，向上提供完整 S3 API。项目支持多种后端（AWS S3、MinIO、OSS 等）、可插拔负载策略、健康检查与统计采集、代理/重定向两种传输模式，以及简易认证和虚拟主机访问。

## 关键特性

- **标准 S3 API**：桶与对象操作、分片上传、预签名 URL 均与原生 S3 一致。
- **多桶调度**：支持轮询、剩余空间、加权和一致性哈希等策略，可热切换。
- **健康监控**：周期性探活与容量统计，Prometheus 指标暴露在 `/metrics`。
- **虚拟桶映射**：对外只暴露虚拟桶名称，真实桶在后端透明调度。
- **代理或重定向模式**：可选择由服务转发数据，或返回预签名 URL 让客户端直连。
- **Basic Auth**：配置 `s3api.auth_required` 后启用基础认证，与 `access_key/secret_key` 对应。

## 快速开始

```bash
# 安装依赖
go mod tidy

# 准备配置
cp config/config.example.yaml config/config.yaml
vim config/config.yaml   # 设置后端桶、数据库、S3 API 选项

# 运行
go run cmd/s3-balance/main.go -config config/config.yaml
# 或构建：
go build -o s3-balance cmd/s3-balance/main.go
./s3-balance -config config/config.yaml

# Docker 构建

docker build -t s3-balance .
docker run -p 8080:8080 -v $(pwd)/config:/root/config s3-balance
```

## 配置要点

- `server`：监听地址与超时时间。
- `database`：GORM 支持 sqlite/mysql/postgres，并存储对象元数据、分片会话等。
- `buckets`：列出真实与虚拟桶。`virtual: true` 的条目会对外暴露，真实桶为 `virtual: false`。可设置 `path_style` 与 `max_size`。
- `balancer`：策略 (`round-robin`|`least-space`|`weighted`|`consistent-hash`)、健康检查周期、重试次数与延迟。
- `metrics`：是否启用 Prometheus 指标及路径。
- `s3api`：Access/Secret Key、`proxy_mode`（true=服务代理，false=重定向）、`auth_required`（Basic Auth）、`virtual_host`（Host-style 路由）。

## API & 测试

- 默认监听 `http://localhost:8080`，支持 `GET /health` 健康检查、`GET /metrics` 指标。
- 可使用 AWS CLI、s3cmd、MinIO Client 或 `python3 test_virtual_bucket_s3.py` 验证兼容性；脚本运行前需修改 endpoint 与凭据。
- Go 单元测试：`go test ./...`。

## 项目结构

```
cmd/s3-balance/     # 服务入口
internal/api/       # S3 路由、对象/分片处理
internal/bucket/    # 桶客户端 & 健康监控
internal/balancer/  # 策略实现与指标
internal/middleware/# 共用中间件（BasicAuth、VirtualHost）
internal/storage/   # GORM 模型与服务
pkg/presigner/      # 预签名 URL 工具
config/             # 示例配置与部署清单
deploy/             # Docker/Kubernetes/Helm 清单
```
