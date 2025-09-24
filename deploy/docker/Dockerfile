# 多阶段构建用于减小镜像大小
FROM golang:1.24-alpine AS builder

# 安装构建依赖
# 注意：不再需要 gcc, musl-dev, sqlite-dev，因为使用 modernc.org/sqlite 纯Go驱动
RUN apk add --no-cache git ca-certificates tzdata

# 设置工作目录
WORKDIR /build

# 复制 Go 模块文件
COPY go.mod go.sum ./
RUN go mod download && go mod tidy

# 复制源码
COPY . .

# 构建应用
# CGO_ENABLED=0: 禁用CGO，使用纯Go SQLite驱动 (modernc.org/sqlite)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s -X main.version=$(git describe --tags --always)" \
    -o s3-balance \
    cmd/s3-balance/main.go

# 运行时镜像 - 使用轻量级Alpine Linux
FROM alpine:latest

# 安装运行时依赖
RUN apk --no-cache add ca-certificates tzdata

# 设置时区
ENV TZ=Asia/Shanghai

WORKDIR /app

# 复制二进制文件
COPY --from=builder /build/s3-balance /app/s3-balance

# 创建配置和数据目录
RUN mkdir -p /app/config /app/data

# 暴露端口
EXPOSE 8080

# 运行应用
ENTRYPOINT ["/app/s3-balance"]
CMD ["-config", "/app/config/config.yaml"]