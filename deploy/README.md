# S3 Balance 部署指南

本目录包含 S3 Balance 服务的各种部署方案，从简单的 Docker Compose 到生产级的 Kubernetes 部署。

## 目录结构

```
deploy/
├── docker/                    # Docker 和 Docker Compose 部署
│   ├── docker-compose.yml     # Docker Compose 配置
│   ├── Dockerfile            # Docker 镜像构建文件
│   ├── start-monitoring.sh    # 一键启动脚本
│   └── config.docker.yaml    # Docker 环境配置
├── kubernetes/               # Kubernetes 原生部署
│   ├── namespace.yaml        # 命名空间
│   ├── configmap.yaml         # 配置映射
│   ├── deployment.yaml        # 部署配置
│   ├── service.yaml           # 服务暴露
│   └── ingress.yaml           # 入口配置
├── helm/                     # HELM Chart 部署
│   └── s3-balance/           # HELM Chart 目录
└── monitoring/               # 监控系统配置
    ├── prometheus.yml        # Prometheus 配置
    ├── s3_balance_alerts.yml # 告警规则
    └── grafana/              # Grafana 配置和仪表板
```

## 🐳 Docker Compose 部署（推荐开发环境）

### 快速启动（含完整监控栈）
```bash
cd deploy/docker
./start-monitoring.sh
```

### 手动启动
```bash
cd deploy/docker
docker-compose up -d
```

### 访问地址
- S3 Balance API: http://localhost:8080
- Grafana 面板: http://localhost:3000 (admin/admin123)
- Prometheus: http://localhost:9090
- 监控指标: http://localhost:8080/metrics

### 配置文件
编辑 `config.docker.yaml` 来自定义：
- 存储桶配置
- 负载均衡策略
- 监控指标设置

## ☸️ Kubernetes 部署（推荐生产环境）

### 基础部署
```bash
cd deploy/kubernetes
kubectl apply -f namespace.yaml
kubectl apply -f configmap.yaml
kubectl apply -f deployment.yaml
kubectl apply -f service.yaml
```

### 高可用部署
```bash
kubectl apply -f hpa.yaml  # 水平自动扩缩容
kubectl apply -f pdb.yaml  # Pod 中断预算
```

### 外部访问
```bash
kubectl apply -f ingress.yaml
```

### 配置说明
- 使用 ConfigMap 管理配置文件
- 支持 HPA 自动扩缩容
- 集成集群 DNS 服务发现
- 支持多环境配置（dev/test/prod）

## 📊 HELM Chart 部署（推荐企业环境）

### 安装 Chart
```bash
cd deploy/helm
helm install s3-balance ./s3-balance
```

### 自定义参数
```bash
helm install s3-balance ./s3-balance -f custom-values.yaml
```

### 升级版本
```bash
helm upgrade s3-balance ./s3-balance
```

### HELM 优势
- 参数化配置
- 多环境管理
- 版本控制
- 依赖管理

## 🔧 监控集成

所有的部署方案都集成了完整的监控系统：

### Prometheus 指标
- S3 Balance 业务指标
- 系统资源指标
- 自定义应用指标

### Grafana 仪表板
- 服务概览面板
- 性能分析面板
- 容量监控面板
- 错误率跟踪面板

### 告警规则
- 存储桶健康状态告警
- 高错误率告警
- 高延迟告警
- 容量使用率告警

## 🚨 部署要求

### 系统要求
- Docker & Docker Compose（开发环境）
- Kubernetes 1.19+（生产环境）
- HELM 3.0+（企业环境）

### 资源要求
- CPU: 0.5-2 core
- Memory: 512MB-2GB
- Storage: 1GB-100GB（根据使用情况）
- Network: 内网访问存储桶

### 网络要求
- 访问后端 S3 存储桶
- Prometheus Pushgateway（可选）
- 外部监控系统（可选）

## 📋 环境变量

### S3 Balance
- `CONFIG_FILE`: 配置文件路径
- `LOG_LEVEL`: 日志级别 (debug/info/warn/error)
- `TZ`: 时区设置

### Prometheus
- `STORAGE_TSDB_RETENTION_TIME`: 数据保留时间
- `WEB_ENABLE_LIFECYCLE`: 启用生命周期管理

### Grafana
- `GF_SECURITY_ADMIN_USER`: 管理员用户名
- `GF_SECURITY_ADMIN_PASSWORD`: 管理员密码
- `GF_USERS_ALLOW_SIGN_UP`: 是否允许注册

## 🎯 最佳实践

### 生产环境建议
1. **资源限制**: 设置合理的CPU/内存限制
2. **健康检查**: 配置完整的探针检查
3. **持久化**: 重要数据使用持久化存储
4. **备份**: 定期备份配置和数据库
5. **监控**: 集成现有监控系统

### 高可用建议
1. **多副本**: 部署多个实例
2. **负载均衡**: 集群内负载均衡
3. **自动扩缩容**: 基于负载自动扩缩容
4. **多区域**: 跨可用区部署

### 安全建议
1. **镜像安全**: 使用官方基础镜像
2. **访问控制**: 配置RBAC权限
3. **网络隔离**: 使用网络策略
4. **敏感信息**: 使用Secret管理凭证

## 🔧 故障排除

### 常见问题

**Pod 启动失败**
```bash
kubectl describe pod <pod-name>
kubectl logs <pod-name>
```

**配置错误**
```bash
kubectl get configmap s3-balance-config -o yaml
```

**服务无法访问**
```bash
kubectl get service s3-balance-service
kubectl get ingress s3-balance-ingress
```

### 性能调优
- 调整负载均衡策略
- 优化数据库连接池
- 配置合理的缓存策略
- 监控资源使用情况

## 🔗 集成外部系统

### 已有 Prometheus
在外部 Prometheus 配置中添加：
```yaml
scrape_configs:
  - job_name: 's3-balance'
    static_configs:
      - targets: ['s3-balance-service.default.svc.cluster.local:8080']
```

### 现有监控系统
- 通过 exporters 暴露指标
- 使用统一的日志格式
- 集成告警通知渠道

## 📖 参考文献

- [Docker Compose 文档](https://docs.docker.com/compose/)
- [Kubernetes 官方文档](https://kubernetes.io/docs/)
- [HELM 官方文档](https://helm.sh/docs/)
- [Prometheus 最佳实践](https://prometheus.io/docs/practices/)
- [Grafana 文档](https://grafana.com/docs/)