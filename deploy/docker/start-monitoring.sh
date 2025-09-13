#!/bin/bash

# S3 Balance + 监控栈 一键启动（修复版）

set -e

echo "🚀 正在启动 S3 Balance + 监控栈（修复CGO版本）..."

# 检查 Docker 和 Docker Compose
if ! command -v docker &> /dev/null; then
    echo "❌ Docker 未安装，请先安装 Docker"
    exit 1
fi

if ! command -v docker-compose &> /dev/null; then
    echo "❌ Docker Compose 未安装，请先安装 Docker Compose"
    exit 1
fi

# 创建必要目录
echo "📁 创建必要目录..."
mkdir -p data grafana/provisioning/datasources grafana/provisioning/dashboards

# 构建 S3 Balance 镜像（使用固定 Go 版本）
echo "🔨 构建 S3 Balance 镜像..."
docker build -t s3-balance:latest -f ../docker/Dockerfile ../..

# 停止已有容器（如果存在）
echo "🛑 清理已有容器..."
docker-compose down 2>/dev/null || true

# 启动服务
echo "🐳 启动 Docker 容器..."
docker-compose up -d

# 等待服务启动
echo "⏳ 等待服务启动..."
sleep 25

# 检查服务状态
echo "🔍 检查服务状态..."
if docker-compose ps | grep -q "Up"; then
    echo ""
    echo "✅ 服务启动完成！"
else
    echo ""
    echo "❌ 服务启动可能失败，检查日志："
    echo "docker-compose logs"
    exit 1
fi

# 输出访问信息
echo ""
echo "🔗 访问地址："
echo "  📊 Grafana 面板: http://localhost:3000 (用户名: admin, 密码: admin123)"
echo "  🔥 Prometheus: http://localhost:9090"
echo "  📈 指标端点: http://localhost:8080/metrics"
echo "  🐳 Node 指标: http://localhost:9100/metrics"
echo ""
echo "🔧 管理命令："
echo "  docker-compose logs -f s3-balance  # 查看 S3 Balance 日志"
echo "  docker-compose logs -f prometheus # 查看 Prometheus 日志"
echo "  docker-compose logs -f grafana    # 查看 Grafana 日志"
echo "  docker-compose down             # 停止所有服务"
echo "  docker-compose restart s3-balance # 重启 S3 Balance"
echo ""
echo "📊 指标查询示例："
echo "  - 存储桶健康: s3_balance_bucket_healthy"
echo "  - QPS: rate(s3_balance_s3_operations_total[1m])"
echo "  - 延迟: histogram_quantile(0.95, s3_balance_s3_operation_duration_seconds_bucket)"
echo ""
echo "🎉 享受完整的 S3 Balance 监控体验！"