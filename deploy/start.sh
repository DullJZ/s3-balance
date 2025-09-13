#!/bin/bash

# S3 Balance 快速部署脚本
# 支持 Docker、Docker Compose、Kubernetes、HELM 四种部署方式

set -e

# 显示帮助信息
show_help() {
    echo "S3 Balance 快速部署脚本"
    echo ""
    echo "用法: ./start.sh [选项]"
    echo ""
    echo "选项:"
    echo "  -m, --mode MODE     部署模式: docker|compose|kubernetes|helm"
    echo "  -e, --env ENV       环境: dev|test|prod (默认: dev)"
    echo "  -b, --build         构建镜像"
    echo "  -p, --port PORT     服务端口 (默认: 8080)"
    echo "  -h, --help          显示帮助信息"
    echo ""
    echo "示例:"
    echo "  ./start.sh -m compose -e prod"
    echo "  ./start.sh -m kubernetes --build"
    echo "  ./start.sh -m helm -e test"
}

# 默认参数
MODE="compose"
ENV="dev"
BUILD=false
PORT="8080"

# 解析命令行参数
while [[ $# -gt 0 ]]; do
    case $1 in
        -m|--mode)
            MODE="$2"
            shift 2
            ;;
        -e|--env)
            ENV="$2"
            shift 2
            ;;
        -b|--build)
            BUILD=true
            shift
            ;;
        -p|--port)
            PORT="$2"
            shift 2
            ;;
        -h|--help)
            show_help
            exit 0
            ;;
        *)
            echo "未知选项: $1"
            show_help
            exit 1
            ;;
    esac
done

# 输出配置信息
echo "🚀 S3 Balance 快速部署开始..."
echo "📋 配置信息："
echo "  部署模式: $MODE"
echo "  环境: $ENV"
echo "  构建镜像: $BUILD"
echo "  服务端口: $PORT"
echo ""

# 检查必要工具
check_requirements() {
    local tool=$1
    local tool_name=$2
    if ! command -v $tool &> /dev/null; then
        echo "❌ $tool_name 未安装"
        return 1
    fi
    echo "✅ $tool_name 已安装"
}

# Docker 模式部署
deploy_docker() {
    echo "📦 Docker 单容器部署..."
    
    check_requirements docker "Docker" || exit 1
    
    if [ "$BUILD" = true ]; then
        echo "🔨 构建 Docker 镜像..."
        docker build -t s3-balance:$MODE-$ENV -f deploy/docker/Dockerfile .
    fi
    
    echo "🚀 启动容器..."
    docker run -d \
        --name s3-balance-$ENV \
        -p $PORT:8080 \
        -v $(pwd)/deploy/docker/config.docker.yaml:/app/config/config.yaml \
        -v s3-balance-data:/app/data \
        -e TZ=Asia/Shanghai \
        --restart unless-stopped \
        s3-balance:$MODE-$ENV
    
    echo "✅ Docker 容器已启动"
}

# Docker Compose 模式部署
deploy_compose() {
    echo "🐳 Docker Compose 部署..."
    
    check_requirements docker "Docker" || exit 1
    check_requirements docker-compose "Docker Compose" || exit 1
    
    if [ "$BUILD" = true ]; then
        echo "🔨 构建 Docker 镜像..."
        cd deploy/docker
        docker build -t s3-balance:$MODE-$ENV -f Dockerfile ../..
    fi
    
    echo "🚀 启动服务栈..."
    cd deploy/docker
    
    # 修改 docker-compose.yml 中的端口
    if [ "$PORT" != "8080" ]; then
        sed -i.bak "s/8080:8080/$PORT:8080/" docker-compose.yml
    fi
    
    docker-compose up -d
    
    # 恢复配置文件
    if [ "$PORT" != "8080" ]; then
        mv docker-compose.yml.bak docker-compose.yml
    fi
    
    cd ../..
    echo "✅ Docker Compose 服务已启动"
}

# Kubernetes 模式部署
deploy_kubernetes() {
    echo "☸️ Kubernetes 部署..."
    
    check_requirements kubectl "kubectl" || exit 1
    check_requirements kubectl "Kubernetes 集群连接" || exit 1
    
    echo "🚀 应用 Kubernetes 配置..."
    cd deploy/kubernetes
    
    # 替换环境变量
    if [ "$ENV" != "dev" ]; then
        # 根据环境替换镜像标签
        sed -i.bak 's/image: s3-balance:latest/image: s3-balance:'"$ENV"'/' deployment.yaml
    fi
    
    kubectl apply -f namespace.yaml
    kubectl apply -f configmap.yaml
    kubectl apply -f deployment.yaml
    kubectl apply -f service.yaml
    kubectl apply -f ingress.yaml
    
    # 如果是生产环境，应用 HPA
    if [ "$ENV" = "prod" ]; then
        kubectl apply -f hpa.yaml
        echo "📊 已启用水平自动扩缩容"
    fi
    
    # 恢复配置文件
    if [ -f deployment.yaml.bak ]; then
        mv deployment.yaml.bak deployment.yaml
    fi
    
    cd ../..
    echo "✅ Kubernetes 部署完成"
}

# HELM 模式部署
deploy_helm() {
    echo "🔧 HELM Chart 部署..."
    
    check_requirements helm "HELM" || exit 1
    check_requirements kubectl "kubectl" || exit 1
    
    echo "🚀 部署 HELM Chart..."
    cd deploy/helm
    
    # 根据环境选择 values 文件
    VALUES_FILE="values.yaml"
    if [ "$ENV" = "prod" ] && [ -f "production-values.yaml" ]; then
        VALUES_FILE="production-values.yaml"
    fi
    
    # 安装或升级 Chart
    if helm status s3-balance-$ENV 2>/dev/null; then
        echo "⬆️ 升级现有 HELM 发布..."
        helm upgrade s3-balance-$ENV s3-balance -f $VALUES_FILE \
            --namespace s3-balance-$ENV \
            --create-namespace \
            --wait
    else
        echo "📦 安装新的 HELM Chart..."
        helm install s3-balance-$ENV s3-balance -f $VALUES_FILE \
            --namespace s3-balance-$ENV \
            --create-namespace \
            --wait
    fi
    
    cd ../..
    echo "✅ HELM Chart 部署完成"
}

# 通用后处理
post_deploy() {
    echo ""
    echo "⏳ 等待服务启动..."
    sleep 10
    
    case $MODE in
        docker)
            echo ""
            echo "✅ Docker 部署完成！"
            echo "🔗 访问地址："
            echo "  📊 指标端点: http://localhost:$PORT/metrics"
            echo ""
            echo "📝 Docker 命令："
            echo "  docker logs -f s3-balance-$ENV    # 查看日志"
            echo "  docker stop s3-balance-$ENV      # 停止服务"
            echo "  docker rm s3-balance-$ENV         # 删除容器"
            ;;
        
        compose)
            echo ""
            echo "✅ Docker Compose 部署完成！"
            echo "🔗 访问地址："
            echo "  📊 Grafana 面板: http://localhost:3000 (admin/admin123)"
            echo "  🔥 Prometheus: http://localhost:9090"
            echo "  📈 指标端点: http://localhost:$PORT/metrics"
            echo ""
            echo "📝 Compose 命令："
            echo "  cd deploy/docker && docker-compose logs -f    # 查看日志"
            echo "  cd deploy/docker && docker-compose down     # 停止服务"
            ;;
        
        kubernetes)
            echo ""
            echo "✅ Kubernetes 部署完成！"
            echo "🔗 检查状态："
            echo "  kubectl get pods -n s3-balance"
            echo "  kubectl get svc -n s3-balance"
            echo "  kubectl get ingress -n s3-balance"
            echo ""
            echo "📋 常用命令："
            echo "  kubectl logs -f deployment/s3-balance-deployment -n s3-balance"
            echo "  kubectl scale deployment s3-balance-deployment --replicas=5 -n s3-balance"
            ;;
        
        helm)
            echo ""
            echo "✅ HELM Chart 部署完成！"
            echo "🔗 检查状态："
            echo "  helm status s3-balance-$ENV -n s3-balance-$ENV"
            echo "  kubectl get pods -n s3-balance-$ENV"
            echo "  kubectl get svc -n s3-balance-$ENV"
            echo ""
            echo "🔧 HELM 命令："
            echo "  helm uninstall s3-balance-$ENV -n s3-balance-$ENV"
            echo "  helm list -A"
            ;;
    esac
    
    echo ""
    echo "📊 监控指标查询："
    echo "  - 存储桶健康: s3_balance_bucket_healthy"
    echo "  - QPS: rate(s3_balance_s3_operations_total[1m])"
    echo "  - 延迟: histogram_quantile(0.95, s3_balance_s3_operation_duration_seconds_bucket)"
    echo ""
    echo "🎉 部署完成！享受 S3 Balance 服务！"
}

# 主执行逻辑
main() {
    echo "🚀 开始部署 S3 Balance..."
    
    # 根据模式执行部署
    case $MODE in
        docker)
            deploy_docker
            ;;
        compose)
            deploy_compose
            ;;
        kubernetes)
            deploy_kubernetes
            ;;
        helm)
            deploy_helm
            ;;
        *)
            echo "❌ 未知部署模式: $MODE"
            show_help
            exit 1
            ;;
    esac
    
    post_deploy
}

# 执行主函数
main