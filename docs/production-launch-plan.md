# Phone-Talk 正式平台发布方案

**日期**: 2026-04-16  
**状态**: 原型验证完成 → 推进正式平台部署  
**目标**: 将 tunnelhub + agentgw + agentapp + agentd 从 Cloudflare PoC 迁移至公司内部正式基础设施

---

## 1. 架构总览

```
agentapp (Flutter iOS/Android/Web)
    │ WebSocket wss://
    ▼
Sentry / Ingress (域名 + HTTPS + 内部 APP 白名单)
    │
    ▼
tunnelhub (K8s Deployment, 2 replicas, ClientIP SessionAffinity)
    │ 反向隧道
    ▼
agentgw (本地网关, macOS/Linux 二进制)
    │ SSH Tunnel + WS
    ▼
agentd (远程节点守护进程, Linux 单二进制)
    │ PTY
    ▼
Claude / OpenCode / Codex
```

**核心数据流**: `agentapp ──WS──► tunnelhub ──tunnel──► agentgw ──SSH+WS──► agentd ──PTY──► AI Agent`

---

## 2. 已完成的关键修复（2026-04-16）

### 2.1 tunnelhub 并发读竞争修复
**问题**: `RegisterTunnel` 和 `BridgeApp` 的两个 goroutine 同时在同一个 `*websocket.Conn` 上调用 `ReadMessage()`，导致 `node.list` 等响应帧被抢读丢失。

**修复**: 引入 `tunnelEntry` 结构，由单一 `tunnelReader` goroutine 独占读取 tunnel → app 方向。

**提交**: `be52d59`

### 2.2 tunnelhub K8s 生产化改造
**新增内容**:
- `/health` HTTP endpoint，供 K8s 启动/就绪/存活探针使用
- Service 层增加 `sessionAffinity: ClientIP`（3h timeout），避免 agentgw 注册在 Pod A，但 agentapp 被负载均衡到 Pod B 导致 `502 agentgw offline`
- 配置完整的 `startupProbe` / `readinessProbe` / `livenessProbe`

**提交**: `072a97d`, `d228ba8`

### 2.3 QR 码远程 token 错误修复
**问题**: `agentgw qr` 和 `printQRCode` 在 `tunnelToken` 为空时回退到 `cfg.Token`（agentgw 本地密码 `9e170cb3...`），导致 Remote QR 扫出来连接失败。

**修复**:
- 新增 `currentTunnelToken` 全局变量，与 `currentTunnelURL` 同步跟踪
- `/config/tunnel` GET 接口同时返回 `tunnelUrl` 和 `tunnelToken`
- `printQRCode` 在拿不到 `tunnelToken` 时直接跳过 Remote QR，避免误导

**提交**: `b161d3c`

### 2.4 agentapp 连接体验优化
- **Android adb 自动填 URL**: `MainActivity.kt` 新增 `getLaunchExtras` MethodChannel，支持 `adb shell am start -e url ... -e token ...`
- **断连不踢回 connections 页**: `app.dart` 的 `redirect` 仅在 `client == null`（从未连接）时跳转，临时 WebSocket 断开时允许用户留在 dashboard
- **扫码失败可编辑**: 扫码后连接失败时，弹出可编辑的连接表单
- **连接失败立即反馈**: `ws_client.dart` `connect()` 初始握手失败时 `rethrow`，UI 立即显示错误

**提交**: `be52d59`

### 2.5 agentd 稳定性修复
- `LoadFromStore` 去重：按 `PID+Provider` 和 `ResumeSessionID+Provider` 去重，避免重复 agent 记录
- `agentList` 去重：按 `resume_session_id` 防御性去重
- `Rename` 持久化：重命名 agent 时同步写入 SQLite
- Claude stream-json 行缓冲：修复 PTY 输出中 JSON 事件被截断的问题

**提交**: `be52d59`

---

## 3. 原型验证结果

| 验证项 | 结果 | 备注 |
|--------|------|------|
| iPad Pro 连接远程 tunnel | 通过 | Flutter debug 模式，`node.list` 正常返回 2 节点 |
| Android 连接远程 tunnel | 通过 | adb intent 自动填 URL，可进入 dashboard |
| Python WebSocket E2E | 通过 | `wss://*.trycloudflare.com/ws/fengming.xie` |
| QR 码格式 | 已修复 | `wss://host/ws/user\|token` 格式正确 |
| Cloudflared 稳定性 | 不稳定 | 免费 quick tunnel 2-3 秒频繁重连，**不可用于生产** |

**结论**: 端到端链路已完全打通，**必须切换到正式域名和基础设施**才能稳定使用。

---

## 4. 正式部署方案：tunnelhub（鲁班/K8s）

### 4.1 Docker 镜像
当前镜像:
```
adas-img.nioint.com/aa-perception/tunnelhub:v0.0.2
```
构建命令（已验证）:
```bash
cd tunnelhub
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o tunnelhub ./cmd/tunnelhub/
docker buildx build --platform linux/amd64 -t adas-img.nioint.com/aa-perception/tunnelhub:v0.0.2 . --push
```

### 4.2 K8s 部署清单
文件: `tunnelhub/k8s-deployment.yaml`（已更新）

核心配置:
- **replicas**: 2
- **sessionAffinity**: `ClientIP`，timeout 10800s（确保 WebSocket 粘附到同一 Pod）
- **探针**: `/health` on `7374`
- **资源**: request 64Mi/50m，limit 256Mi/200m
- **Secret**: `phonetalk-tunnelhub-secret`，key `users`

部署命令:
```bash
kubectl create secret generic phonetalk-tunnelhub-secret \
  --from-literal=users="fengming.xie:$(openssl rand -hex 16);yehong.yang:$(openssl rand -hex 16)"

kubectl apply -f tunnelhub/k8s-deployment.yaml
```

### 4.3 域名与 Sentry 配置

#### Step 1: Sentry 申请
访问 https://sentry.nio.com 创建服务。

**Sentry/MK 网关架构说明**（参考 [Sentry2.0 使用说明](https://nio.feishu.cn/wiki/Vh7fwRctIiBACmkbyYmcctZvntc)）:
- Sentry 是公司内部 MK (Moatkeeper) 网关的配置管理平台，配置实时同步到网关生效
- 请求链路: Client → LB → K8s Ingress → MK 网关 → tunnelhub Pod
- MK 网关负责: 签名校验、负载均衡、插件执行、请求转发

创建服务时填写:

| 字段 | 建议值 |
|------|--------|
| 服务名称 | `AD_PhoneTalk_TunnelHub` |
| 项目属性 | **内部** |
| 协议 | `HTTPS` |
| path | `/` |
| target | `phonetalk-tunnelhub-svc:7374` |
| 网关 | 按部署环境选择（如 `mk-dev` / `mk-prod`） |

创建后获得 `app_id` 和 `app_secret`。

**可启用的网关插件**（按需配置）:
- **签名校验**（路由级）: 对访问方进行 sign 校验，防止未授权 Client 调用
- **访问控制**（服务插件）: API 级别白名单，控制哪些 Client 可以访问
- **频次限制**: 限制单 Client 的调用频率
- **IP 黑白名单**: 限制可访问的源 IP 范围
- **Account Token 解析**: 如需对接 SSO，可在网关层解析 Account Token

**对于 tunnelhub 的特殊考虑**:
- `agentapp` 通过 WebSocket 直接连接，无法携带标准 Sentry sign（因为 WebSocket upgrade 握手不支持动态签名）
- `agentgw` 注册隧道时同样无法动态生成 sign
- 因此建议 **不对 `/tunnel/register` 和 `/ws/` 开启签名校验**，改为:
  - 通过 **内部 APP 白名单** 或 **IP 白名单** 限制来源
  - 由 tunnelhub 应用层自行校验 `userID -> token` 映射
- 管理类接口（如有）可以走标准签名校验或 OpenSSO

#### Step 2: 域名申请
| 环境 | 域名 | SSO Endpoint |
|------|------|--------------|
| dev/test | `phonetalk-tunnel-dev.nioint.com` | `https://signin-dev.nio.com` |
| prod | `phonetalk-tunnel.nio.com` | `https://signin.nio.com` |

工单内容:
- 后端端口: `7374`
- 鲁班实例链接: （从 `https://luban.nioint.com` 服务暴露页面复制）
- 是否走 MK: **是**

#### Step 3: 鲁班平台暴露服务
1. 登录 `https://luban.nioint.com` → 服务暴露
2. 类型: 容器服务
3. 名称: `phonetalk-tunnelhub`
4. namespace: `default`（或按团队规范调整）
5. 端口: `7374`
6. 保存后复制鲁班实例链接，填入域名工单

### 4.4 OpenSSO 接入策略

**现状**: tunnelhub 代码目前仅支持静态 `user:password` token 映射，**未实现 OpenSSO/JWT 校验**。

**约束**: OpenSSO 是 OAuth2 浏览器重定向协议，但 tunnelhub 的两类客户端均无法走标准 302 重定向:
- `agentgw` 是后台 Go 进程，无浏览器
- `agentapp` 使用 WebSocket，handshake 不支持重定向

**推荐分层认证方案**:

1. **网络层（Sentry / Ingress）**
   - 将 tunnelhub 注册为 Sentry **内部 APP**
   - 通过 Ingress / Gateway 的 **mTLS 或内部 APP 白名单** 限制来源
   - `/health` 保持无认证（K8s 探针需要）
   - 管理页面（如有）可走 OpenSSO OAuth2

2. **应用层（tunnelhub）**
   - 继续使用 `USERS` Secret 的 `userID -> token` 映射
   - `agentgw` 注册隧道时携带 `Authorization: Bearer <token>` 或 `?token=...`
   - `agentapp` 连接 `/ws/{userId}?token=...` 时由 tunnelhub 校验
   - token 定期轮换，通过更新 K8s Secret + 滚动发布完成

**如需进一步提升安全合规**（安全审计要求）:
- 可以让 agentapp 先通过 OpenSSO 获取 `accessToken`（7 天有效期），然后在 WebSocket handshake 中将 JWT 作为 `Bearer` token 发送给 tunnelhub
- tunnelhub 增加 JWT 验签逻辑（调用 OpenSSO JWKS endpoint，如 `https://signin-dev.nio.com/.well-known/jwks.json`）
- **此方案需要额外开发，当前代码未实现**

---

## 5. agentgw / agentapp / agentd 发布计划

### 5.1 agentgw 二进制发布
- **macOS ARM64**: `agentgw-macos-arm64`
- **Linux AMD64**: `agentgw-linux`

需要同时打包 `agentd-linux` 二进制（内嵌或同目录），否则一键部署到远程节点会失败。

### 5.2 agentapp 分发
- **Android APK**: 已构建 78.3MB，需要公司内部分发渠道（如企业微信/飞书/内部应用市场）
- **iOS IPA**: 需要确认 Apple Developer 企业分发证书或 TestFlight 流程
- **Flutter Web**: `agentgw/static/` 已随 agentgw 一起发布，可直接通过 agentgw HTTP 访问

### 5.3 agentd 部署链路验证
- 确认 `agentgw node.deploy` 在正式环境可用
- 确认 SSH 密钥权限正常
- 确认远程节点 `~/.agentd/config.yaml` 生成正确

---

## 6. 参考文档链接

| 文档 | 链接 | 用途 |
|------|------|------|
| Sentry 服务创建与路由流程 | https://nio.feishu.cn/docx/doxcnSrnL1u5IVDPdwHzp7JxPHf | Sentry 服务申请、路由部署、审批流程 |
| 接入 NIO SSO 手册 | https://nio.feishu.cn/wiki/wikcnY2XmUP4el9Mx955hdIdpyg | SSO 域名映射、环境区分、Sentry 申请入口 |
| OpenSSO Guide & FAQ | https://nio.feishu.cn/docs/doccn6I6QMJBia5rY74zSNdU0rS | OAuth2 接入步骤、token 有效期、MFA、FAQ |
| Sentry Client 创建与 Secret 切换 | https://nio.feishu.cn/wiki/IEuUwcrMfiCZRckBDcLcuR41n5g | app_id/app_secret 管理、权限移交、集群同步 |
| Sentry2.0 使用说明 | https://nio.feishu.cn/wiki/Vh7fwRctIiBACmkbyYmcctZvntc | Sentry/MK 网关架构、插件功能、服务创建、签名规范 |

---

## 7. 下一步行动清单（Action Items）

### 本周必须完成
- [ ] 在 Sentry 创建 `AD_PhoneTalk_TunnelHub` 服务
- [ ] 在鲁班平台暴露 `phonetalk-tunnelhub-svc`
- [ ] 申请 `phonetalk-tunnel-dev.nioint.com` 域名
- [ ] 创建 K8s Secret `phonetalk-tunnelhub-secret`
- [ ] `kubectl apply -f tunnelhub/k8s-deployment.yaml`
- [ ] 验证 `curl https://phonetalk-tunnel-dev.nioint.com/health` 返回 200

### 下周推进
- [ ] 更新 agentgw 默认配置，指向正式域名
- [ ] 重新构建并发布 agentgw + agentd-linux 二进制
- [ ] 分发 agentapp.apk 到 Android 测试机
- [ ] 端到端验证：Android / iOS → 正式 tunnelhub → agentgw → agentd
- [ ] 输出用户安装手册和运维 on-call 文档

### 待决策
- [ ] 是否需要 tunnelhub 内嵌 JWT 验签？（取决于安全审计要求）
- [ ] iOS 分发走企业证书还是 TestFlight / App Store？
- [ ] 是否需要在 K8s 中开启 HPA 自动扩缩容？
- [ ] 日志采集是否必须切 JSON 格式并接入 Prometheus？

---

## 8. 已知风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| Cloudflared 免费隧道极不稳定 | PoC 阶段已验证 2-3 秒重连 | 已切换到公司内部域名 + K8s |
| K8s 多副本无 sessionAffinity | agentapp 可能被路由到无 tunnel 的 Pod | 已在 Service 配置 ClientIP sticky |
| OpenSSO 无法直接用于 WebSocket | 可能导致安全审计不通过 | 先用 Sentry 内部 APP 白名单 + 静态 token；如需要再开发 JWT 验签 |
| iOS 签名/分发流程长 | 影响正式员工使用 | 优先保证 Android + Web 可用；iOS 走 TestFlight 或企业分发 |

---

*本文档由 2026-04-16 原型验证会话生成，后续部署进展应及时更新到此文档。*
