# 多阶段构建用于减小镜像大小
FROM golang:1.24-alpine AS builder

# 安装构建依赖（包括CGO支持的sqlite3）
RUN apk add --no-cache gcc musl-dev sqlite-dev git ca-certificates tzdata

# 设置工作目录
WORKDIR /build

# 复制 Go 模块文件
COPY go.mod go.sum ./
RUN go mod download && go mod tidy

# 复制源码
COPY . .

# 构建应用（启用CGO）
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -X main.version=$(git describe --tags --always)' \
    -o s3-balance \
    cmd/s3-balance/main.go

# 运行时镜像
FROM alpine:latest

# 安装运行时依赖（包括sqlite库）
RUN apk --no-cache add ca-certificates tzdata sqlite-libs

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