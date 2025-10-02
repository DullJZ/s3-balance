# S3 Balance

S3 Balance is an S3-compatible load balancer written in Go. It automatically selects the best target bucket among multiple object storage backends and provides a complete S3 API. The project supports various backends (AWS S3, MinIO, OSS, etc.), pluggable balancing strategies, health checks and metrics collection, proxy/redirect transfer modes, simple authentication, and virtual host access.

## Key Features

- **Standard S3 API**: Bucket and object operations, multipart uploads, and pre-signed URLs are fully compatible with native S3.
- **Multi-bucket Scheduling**: Supports strategies like round-robin, least-space, weighted, and consistent hashing, with hot-swapping capabilities.
- **Health Monitoring**: Periodic health checks and capacity statistics, with Prometheus metrics exposed at `/metrics`.
- **Virtual Bucket Mapping**: Only virtual bucket names are exposed externally, while real buckets are transparently scheduled in the backend.
- **Proxy or Redirect Mode**: Choose between service-forwarded data or pre-signed URLs for direct client connections.
- **SigV4 Authentication**: When `s3api.auth_required` is enabled, AWS Signature Version 4 requests are validated using `github.com/DullJZ/s3-validate`.

## Quick Start

### Docker Compose

#### S3 Balance Service Only

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

#### With Prometheus & Grafana Monitoring

```bash
# Clone the repository
git clone https://github.com/DullJZ/s3-balance.git --depth=1
cd s3-balance/deploy/docker

# Modify the configuration
vim config.yaml

# Start the service
docker compose up -d
```

### Docker
```bash
# Pull the image
docker pull dulljz/s3-balance:latest
# Prepare configuration and data directories
mkdir -p ~/s3-balance/config ~/s3-balance/data
wget https://raw.githubusercontent.com/DullJZ/s3-balance/refs/heads/main/config/config.example.yaml -O ~/s3-balance/config/config.yaml
vim ~/s3-balance/config/config.yaml   # Configure backend buckets, database, and S3 API options
# Run the service
docker run -d --name s3-balance -p 8080:8080 -v ~/s3-balance/config:/app/config -v ~/s3-balance/data:/app/data dulljz/s3-balance:latest
```

### Local Build and Run

```bash
# Clone the repository
git clone https://github.com/DullJZ/s3-balance.git --depth=1
cd s3-balance

# Install dependencies
go mod tidy

# Prepare configuration
cp config/config.example.yaml config/config.yaml
vim config/config.yaml   # Configure backend buckets, database, and S3 API options

# Run the service
go run cmd/s3-balance/main.go -config config/config.yaml
# Or build the binary:
go build -o s3-balance cmd/s3-balance/main.go
./s3-balance -config config/config.yaml
```

## Configuration Highlights

- `server`: Listening address and timeout settings.
- `database`: GORM supports sqlite/mysql/postgres and stores object metadata, multipart sessions, etc.
- `buckets`: Lists real and virtual buckets. Entries with `virtual: true` are exposed externally, while real buckets are marked as `virtual: false`. Supports `path_style` and `max_size` settings.
- `balancer`: Strategy (`round-robin`|`least-space`|`weighted`), health check intervals, retry counts, and delays.
- `metrics`: Whether to enable Prometheus metrics and their path.
- `s3api`: Access/Secret Key, `proxy_mode` (true=proxy, false=redirect), `auth_required` (SigV4 validation), `virtual_host` (host-style routing).

## API & Testing

- Default listening at `http://localhost:8080`, supports `GET /health` for health checks and `GET /metrics` for metrics.
- Compatibility can be verified using AWS CLI, s3cmd, MinIO Client, or `python3 test_virtual_bucket_s3.py`. Modify the endpoint and credentials in the script before running.

## Project Structure

```
cmd/s3-balance/      # Service entrypoint
internal/api/        # S3 routing, object/multipart handling
internal/bucket/     # Bucket client & health monitoring
internal/balancer/   # Strategy implementations & metrics
internal/middleware/ # Middleware for SigV4, virtual hosts, etc.
internal/storage/    # GORM models and services
pkg/presigner/       # Pre-signed URL utilities
config/              # Example configurations and deployment manifests
deploy/              # Docker/Kubernetes/Helm manifests
```