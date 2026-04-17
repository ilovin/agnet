#!/usr/bin/env bash
set -euo pipefail

# tunnelhub v0.0.3 构建与部署脚本
# 用法:
#   cd tunnelhub/scripts
#   ./build_and_deploy.sh [dev|prod]
#
# 环境要求:
#   - docker buildx 可用并已登录 adas-img.nioint.com
#   - kubectl 已配置目标集群
#   - k8s-deployment.yaml 在同目录的上一级

ENV="${1:-dev}"
IMAGE="adas-img.nioint.com/aa-perception/tunnelhub:v0.0.3"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TUNNELHUB_DIR="$(dirname "$SCRIPT_DIR")"

echo "=========================================="
echo "Building tunnelhub v0.0.3 for linux/amd64"
echo "Image: $IMAGE"
echo "=========================================="

cd "$TUNNELHUB_DIR"

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o tunnelhub ./cmd/tunnelhub/

docker buildx build --platform linux/amd64 -t "$IMAGE" . --push

echo ""
echo "=========================================="
echo "Build completed. Image pushed to:"
echo "$IMAGE"
echo "=========================================="

echo ""
echo "=========================================="
echo "Deploying to K8s (env=$ENV)"
echo "=========================================="

# 可选: 创建 emergency fallback secret (本地 LAN 模式回退用)
kubectl create secret generic phonetalk-tunnelhub-secret \
  --from-literal=users="fengming.xie:$(openssl rand -hex 16)" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f "$TUNNELHUB_DIR/k8s-deployment.yaml"

echo ""
echo "=========================================="
echo "Deployment applied. Waiting for rollout..."
echo "=========================================="

kubectl rollout status deployment/phonetalk-tunnelhub --timeout=120s

echo ""
echo "=========================================="
echo "Done. Health check endpoint:"
echo "  http://phonetalk-tunnelhub-svc.default.svc.cluster.local:7374/health"
echo "=========================================="
