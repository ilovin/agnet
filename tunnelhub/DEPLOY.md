# TunnelHub 部署指南（多用户版）

## 本地快速验证（Cloudflare Tunnel / 内网任意机器）

### 1. 编译

```bash
cd tunnelhub
CGO_ENABLED=0 GOOS=linux go build -o tunnelhub ./cmd/tunnelhub
```

### 2. 启动 tunnelhub（多用户）

```bash
# 方式 A：多用户白名单（推荐）
export USERS="alice:poc-token-a;bob:poc-token-b;carol:poc-token-c"
go run ./cmd/tunnelhub

# 方式 B：单用户兼容旧逻辑
export TUNNEL_SECRET="poc-secret-123"
go run ./cmd/tunnelhub
```

### 3. 启动本地 agentgw + tunnel client

```bash
cd agentgw

# alice
export AGENTGW_TUNNEL_URL="ws://localhost:7374/tunnel/register?userId=alice"
export AGENTGW_TUNNEL_TOKEN="poc-token-a"
./agentgw start
```

另一个同事 Bob：

```bash
# bob
export AGENTGW_TUNNEL_URL="ws://localhost:7374/tunnel/register?userId=bob"
export AGENTGW_TUNNEL_TOKEN="poc-token-b"
./agentgw start
```

### 4. 公网暴露（Cloudflare Tunnel，临时 PoC）

```bash
cloudflared tunnel --url http://localhost:7374
```

拿到临时域名：`https://phonetalk-abc123.trycloudflare.com`

### 5. agentapp 连接

```dart
// alice
final url = 'wss://phonetalk-abc123.trycloudflare.com/ws/alice?token=poc-token-a';

// bob
final url = 'wss://phonetalk-abc123.trycloudflare.com/ws/bob?token=poc-token-b';
```

---

## 腾讯云 K8s 正式部署

### 1. 构建镜像

```bash
cd tunnelhub

# 编译 Linux 静态二进制
CGO_ENABLED=0 GOOS=linux go build -o tunnelhub ./cmd/tunnelhub

# 构建镜像（把 your-namespace 换成实际的）
docker build -t adas-img.nioint.com/aa-perception/tunnelhub:v0.0.1 .

# 推送（需先 docker login）
docker push adas-img.nioint.com/aa-perception/tunnelhub:v0.0.1
```

### 2. 修改 K8s 配置

编辑 `k8s-deployment.yaml`：
- 镜像地址换成你实际 build 出来的
- `namespace` 按需调整

### 3. 创建 Secret 并部署

```bash
# 多用户场景：把 USERS 写入 secret
kubectl create secret generic phonetalk-tunnelhub-secret \
  --from-literal=users="alice:$(openssl rand -hex 16);bob:$(openssl rand -hex 16)"

# 单用户场景（兼容）
# kubectl create secret generic phonetalk-tunnelhub-secret \
#   --from-literal=tunnel-secret="$(openssl rand -hex 32)"

kubectl apply -f k8s-deployment.yaml
```

### 4. 验证

```bash
kubectl get pods -l app=phonetalk-tunnelhub
kubectl port-forward svc/phonetalk-tunnelhub-svc 7374:7374

# 另开终端
curl http://localhost:7374/health   # ok
```

### 5. 鲁班平台暴露服务

1. 登录 `https://luban.nioint.com` → **服务暴露**
2. 暴露服务：
   - 类型：容器服务
   - 名称：`phonetalk-tunnelhub`
   - namespace：`default`
   - 端口：`7374`
3. 保存后复制**鲁班实例链接**

### 6. 申请域名

- **内网**：`phonetalk-tunnel-dev.nioint.com`（fx 平台低成本）
- **公网**：`phonetalk-tunnel.nio.com`（Jira DOPS 工单 + 安全评审）

工单填写：
- 后端端口：`7374`
- 鲁班实例链接：上一步复制的
- 是否走 MK：**是**

### 7. Sentry 配置

1. 创建服务 `DD_PhoneTalk_Tunnel`
2. 创建路由，绑定域名
3. `/ws/` 路径启用 OpenSSO / Account Token 校验

---

## 多用户原理

- `tunnelhub` 维护 `userID -> token` 映射
- `agentgw` 启动时带上 `?userId=alice` 和对应 token 注册 tunnel
- `agentapp` 访问 `/ws/alice?token=...` 时，hub 校验 token 并桥接到 alice 的 tunnel
- 每个用户完全隔离，互不影响
