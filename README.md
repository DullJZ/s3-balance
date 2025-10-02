# S3 Balance

中文 | [English](./README_EN.md)

S3 Balance 是一个用 Go 编写的 S3 兼容负载均衡器，可在多套对象存储之间自动选择最佳目标桶，向上提供完整 S3 API。项目支持多种后端（AWS S3、MinIO、OSS 等）、可插拔负载策略、健康检查与统计采集、代理/重定向两种传输模式，以及简易认证和虚拟主机访问。

## 关键特性

- **标准 S3 API**：桶与对象操作、分片上传、预签名 URL 均与原生 S3 一致。
- **多桶调度**：支持轮询、剩余空间、加权和一致性哈希等策略，可热切换。
- **健康监控**：周期性探活与容量统计，Prometheus 指标暴露在 `/metrics`。
- **虚拟桶映射**：对外只暴露虚拟桶名称，真实桶在后端透明调度。
- **代理或重定向模式**：可选择由服务转发数据，或返回预签名 URL 让客户端直连。
- **SigV4 认证**：配置 `s3api.auth_required` 后，通过 `github.com/DullJZ/s3-validate` 校验 AWS Signature Version 4 请求。

## 快速开始

### Docker Compose

#### 仅S3 Balance服务

```yaml
services:
  s3-balance:
    image: dulljz/s3-balance:latest
    container_name: s3-balance
    ports:
      - "8080:8080"
    volumes:
      - ./config.yaml:/app/config/config.yaml
      - ./data:/app/data
    environment:
      - TZ=Asia/Shanghai
    restart: unless-stopped
    command: ["/app/s3-balance", "-config", "/app/config/config.yaml"]
```
#### 含Prometheus & Grafana监控

```bash
# 克隆仓库
git clone https://github.com/DullJZ/s3-balance.git --depth=1
cd s3-balance/deploy/docker

# 修改配置
vim config.yaml

# 启动服务
docker compose up -d
```

### Docker
```bash
# 拉取镜像
docker pull dulljz/s3-balance:latest
# 准备配置与数据目录
mkdir -p ~/s3-balance/config ~/s3-balance/data
wget https://raw.githubusercontent.com/DullJZ/s3-balance/refs/heads/main/config/config.example.yaml -O ~/s3-balance/config/config.yaml
vim ~/s3-balance/config/config.yaml   # 设置后端桶、数据库、S3 API 选项
# 运行
docker run -d --name s3-balance -p 8080:8080 -v ~/s3-balance/config:/app/config -v ~/s3-balance/data:/app/data dulljz/s3-balance:latest
```

### 本地编译运行

```bash
# 克隆仓库
git clone https://github.com/DullJZ/s3-balance.git --depth=1
cd s3-balance

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

```

## 配置要点

- `server`：监听地址与超时时间。
- `database`：GORM 支持 sqlite/mysql/postgres，并存储对象元数据、分片会话等。
- `buckets`：列出真实与虚拟桶。`virtual: true` 的条目会对外暴露，真实桶为 `virtual: false`。可设置 `path_style` 与 `max_size`。
- `balancer`：策略 (`round-robin`|`least-space`|`weighted`)、健康检查周期、重试次数与延迟。
- `metrics`：是否启用 Prometheus 指标及路径。
- `s3api`：Access/Secret Key、`proxy_mode`（true=服务代理，false=重定向）、`auth_required`（SigV4 校验）、`virtual_host`（Host-style 路由）。

## API & 测试

- 默认监听 `http://localhost:8080`，支持 `GET /health` 健康检查、`GET /metrics` 指标。
- 可使用 AWS CLI、s3cmd、MinIO Client 或 `python3 test_virtual_bucket_s3.py` 验证兼容性；脚本运行前需修改 endpoint 与凭据。

## 项目结构

```
cmd/s3-balance/      # 服务入口
internal/api/        # S3 路由、对象/分片处理
internal/bucket/     # 桶客户端 & 健康监控
internal/balancer/   # 策略实现与指标
internal/middleware/ # SigV4、虚拟主机等中间件
internal/storage/    # GORM 模型与服务
pkg/presigner/       # 预签名 URL 工具
config/              # 示例配置与部署清单
deploy/              # Docker/Kubernetes/Helm 清单
```
