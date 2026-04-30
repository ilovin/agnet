# Phone-Talk 正式平台发布方案

**日期**: 2026-04-17  
**状态**: v0.0.3 双模式认证版本（OpenSSO + 本地静态回退），待上 dev 环境验证  
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

## 2. 双模式认证设计

| 模式 | 使用场景 | 认证方式 |
|------|---------|---------|
| **本地精简模式** | 局域网直连 `ws://192.168.x.x` 或本机调试 | 静态 `USERS` Secret (`userID:password`) |
| **正式 OpenSSO 模式** | 通过域名 `wss://phonetalk-tunnel*.nio.com` | OpenSSO profile API 验证 access token |

### 2.1 模式切换开关

tunnelhub 通过环境变量 `OPENSSO_PROFILE_URL` 自动切换：

```yaml
env:
  - name: OPENSSO_PROFILE_URL
    value: "https://signin.nio.com/oauth2/profile"
```

- **设置了** `OPENSSO_PROFILE_URL` → 强制 OpenSSO，所有请求必须携带有效 access token
- **未设置** → 回退本地 `USERS` 静态验证（保留做 LAN 内网回退和 emergency 访问）

### 2.2 agentgw OpenSSO 登录

新增 `agentgw login` 命令，完整 OAuth2 授权码流程：

```bash
# 1. 登录获取 accessToken + username
$ agentgw login
Please open the following URL in your browser and sign in:
https://signin.nio.com/oauth2/authorize?...
Login successful! userId=fengming.xie
Token saved to: ~/.agentgw/oauth.json

# 2. 启动时自动读取 token 注册 tunnel
$ agentgw start --tunnel-url wss://phonetalk-tunnel-dev.nioint.com/ws/fengming.xie
```

内部流程：
1. 启动本地临时 HTTP server 监听 `localhost:8384/callback`
2. 打印 authorize URL，用户在公司浏览器登录
3. 捕获回调 `code`，POST `accessToken` 接口换 token
4. 用 `accessToken` 调 `profile` 接口获取 `userId`
5. 持久化 `{userId, accessToken, refreshToken}` 到 `~/.agentgw/oauth.json`

### 2.3 agentapp 连接方式

- **LAN 模式**: `ws://192.168.1.10:7374/ws/default?token=本地密码`
- **OpenSSO 模式**: `wss://phonetalk-tunnel-dev.nioint.com/ws/fengming.xie?token=<accessToken>`

agentapp 需要新增 Flutter WebView OAuth2 登录（待开发），或在首次连接时由用户手动粘贴 `accessToken`。

---

## 3. 已完成的关键修复（2026-04-16 ~ 04-17）

### 3.1 tunnelhub v0.0.3 双模式认证
- 新增 `internal/sso/validator.go`：封装 OpenSSO profile API 验证 + 5 分钟内存缓存
- `internal/hub/hub.go`：`auth()` 优先 OpenSSO，未配置时 fallback `USERS`
- `cmd/tunnelhub/main.go`：恢复 `parseUsers`，新增 `OPENSSO_PROFILE_URL` 开关

### 3.2 tunnelhub 并发读竞争修复
**问题**: `RegisterTunnel` 和 `BridgeApp` 的两个 goroutine 同时在同一个 `*websocket.Conn` 上调用 `ReadMessage()`，导致 `node.list` 等响应帧被抢读丢失。

**修复**: 引入 `tunnelEntry` 结构，由单一 `tunnelReader` goroutine 独占读取 tunnel → app 方向。

**提交**: `be52d59`

### 3.3 tunnelhub K8s 生产化改造
**新增内容**:
- `/health` HTTP endpoint，供 K8s 启动/就绪/存活探针使用
- Service 层增加 `sessionAffinity: ClientIP`（3h timeout），避免 agentgw 注册在 Pod A，但 agentapp 被负载均衡到 Pod B 导致 `502 agentgw offline`
- 配置完整的 `startupProbe` / `readinessProbe` / `livenessProbe`

**提交**: `072a97d`, `d228ba8`

### 3.4 QR 码远程 token 错误修复
**问题**: `agentgw qr` 和 `printQRCode` 在 `tunnelToken` 为空时回退到 `cfg.Token`（agentgw 本地密码 `9e170cb3...`），导致 Remote QR 扫出来连接失败。

**修复**:
- 新增 `currentTunnelToken` 全局变量，与 `currentTunnelURL` 同步跟踪
- `/config/tunnel` GET 接口同时返回 `tunnelUrl` 和 `tunnelToken`
- `printQRCode` 在拿不到 `tunnelToken` 时直接跳过 Remote QR，避免误导

**提交**: `b161d3c`

### 3.5 agentapp 连接体验优化
- **Android adb 自动填 URL**: `MainActivity.kt` 新增 `getLaunchExtras` MethodChannel，支持 `adb shell am start -e url ... -e token ...`
- **断连不踢回 connections 页**: `app.dart` 的 `redirect` 仅在 `client == null`（从未连接）时跳转，临时 WebSocket 断开时允许用户留在 dashboard
- **扫码失败可编辑**: 扫码后连接失败时，弹出可编辑的连接表单
- **连接失败立即反馈**: `ws_client.dart` `connect()` 初始握手失败时 `rethrow`，UI 立即显示错误

**提交**: `be52d59`

### 3.6 agentd 稳定性修复
- `LoadFromStore` 去重：按 `PID+Provider` 和 `ResumeSessionID+Provider` 去重，避免重复 agent 记录
- `agentList` 去重：按 `resume_session_id` 防御性去重
- `Rename` 持久化：重命名 agent 时同步写入 SQLite
- Claude stream-json 行缓冲：修复 PTY 输出中 JSON 事件被截断的问题

**提交**: `be52d59`

---

## 4. 原型验证结果

| 验证项 | 结果 | 备注 |
|--------|------|------|
| iPad Pro 连接远程 tunnel | 通过 | Flutter debug 模式，`node.list` 正常返回 2 节点 |
| Android 连接远程 tunnel | 通过 | adb intent 自动填 URL，可进入 dashboard |
| Python WebSocket E2E | 通过 | `wss://*.trycloudflare.com/ws/fengming.xie` |
| QR 码格式 | 已修复 | `wss://host/ws/user\|token` 格式正确 |
| Cloudflared 稳定性 | 不稳定 | 免费 quick tunnel 2-3 秒频繁重连，**不可用于生产** |

**结论**: 端到端链路已完全打通，**必须切换到正式域名和基础设施**才能稳定使用。

---

## 5. 正式部署方案：tunnelhub（鲁班/K8s）

### 5.1 Docker 镜像

当前镜像:
```
adas-img.nioint.com/aa-perception/tunnelhub:v0.0.3
```

构建命令（已脚本化）:
```bash
cd tunnelhub/scripts
./build_and_deploy.sh dev
```

或手动：
```bash
cd tunnelhub
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o tunnelhub ./cmd/tunnelhub/
docker buildx build --platform linux/amd64 \
  -t adas-img.nioint.com/aa-perception/tunnelhub:v0.0.3 \
  . --push
```

### 5.2 K8s 部署清单

文件: `tunnelhub/k8s-deployment.yaml`（已更新为 v0.0.3）

核心配置:
- **replicas**: 2
- **sessionAffinity**: `ClientIP`，timeout 10800s（确保 WebSocket 粘附到同一 Pod）
- **探针**: `/health` on `7374`
- **资源**: request 64Mi/50m，limit 256Mi/200m
- **OpenSSO**: 新增 `OPENSSO_PROFILE_URL=https://signin.nio.com/oauth2/profile`
- **USERS Secret**: 改为 `optional: true`，作为 LAN 回退和 emergency 使用

部署命令:
```bash
kubectl create secret generic phonetalk-tunnelhub-secret \
  --from-literal=users="fengming.xie:$(openssl rand -hex 16)" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f tunnelhub/k8s-deployment.yaml
```

### 5.3 域名与 Sentry 配置

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
  - 由 tunnelhub 应用层自行校验 `userID -> token` 映射（OpenSSO 或本地 Secret）
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

### 5.4 OpenSSO 接入策略（v0.0.3 已实现）

**现状**: tunnelhub v0.0.3 已内置 OpenSSO 验证，通过 `OPENSSO_PROFILE_URL` 环境变量启用。

**约束**: OpenSSO 是 OAuth2 浏览器重定向协议，但 tunnelhub 的两类客户端中：
- `agentgw` 已有 `agentgw login` 本地浏览器登录方案
- `agentapp` 仍需要后续开发 Flutter WebView 登录（或先手动粘贴 token）

**最终方案**:
1. **网络层（Sentry / Ingress）**
   - 将 tunnelhub 注册为 Sentry **内部 APP**
   - 通过 Ingress / Gateway 的 **mTLS 或内部 APP 白名单** 限制来源
   - `/health` 保持无认证（K8s 探针需要）

2. **应用层（tunnelhub）**
   - 启用 `OPENSSO_PROFILE_URL`，统一用 OpenSSO profile API 验证 token
   - `agentgw` 注册隧道时携带 `?token=<accessToken>` 或 `Authorization: Bearer <token>`
   - `agentapp` 连接 `/ws/{userId}?token=<accessToken>` 时由 tunnelhub 校验 `profile.UserID == userId`

3. **本地回退（LAN / emergency）**
   - 不设置 `OPENSSO_PROFILE_URL` 时，继续使用 `USERS` Secret

---

## 6. agentgw / agentapp / agentd 发布计划

### 6.1 agentgw 二进制发布
- **macOS ARM64**: `agentgw-macos-arm64`
- **Linux AMD64**: `agentgw-linux`

需要同时打包 `agentd-linux` 二进制（内嵌或同目录），否则一键部署到远程节点会失败。

**agentgw 使用示例（OpenSSO 模式）**:
```bash
# 首次登录（或 token 过期后）
agentgw login

# 启动并注册到正式 tunnelhub
agentgw start \
  --tunnel-url wss://phonetalk-tunnel-dev.nioint.com/ws/fengming.xie
```

### 6.2 agentapp 分发
- **Android APK**: 已构建 78.3MB，需要公司内部分发渠道（如企业微信/飞书/内部应用市场）
- **iOS IPA**: 需要确认 Apple Developer 企业分发证书或 TestFlight 流程
- **Flutter Web**: `agentgw/static/` 已随 agentgw 一起发布，可直接通过 agentgw HTTP 访问

### 6.3 agentd 部署链路验证
- 确认 `agentgw node.deploy` 在正式环境可用
- 确认 SSH 密钥权限正常
- 确认远程节点 `~/.agentd/config.yaml` 生成正确

---

## 7. 辅助脚本清单

| 脚本 | 路径 | 用途 |
|------|------|------|
| `build_and_deploy.sh` | `tunnelhub/scripts/` | 一键构建 Docker 镜像并部署到 K8s |
| `get_sso_token.py` | `tunnelhub/scripts/` | 本地浏览器 OAuth2 登录，自动获取 accessToken 和 profile |
| `debug_sso_playwright.py` | `tunnelhub/scripts/` | Playwright 自动拦截浏览器 network，提取 code 换 token |
| `verify_sso_profile.py` | `tunnelhub/scripts/` | 测试 4 种 token 传递方式调用 OpenSSO profile API |

---

## 8. 参考文档链接

| 文档 | 链接 | 用途 |
|------|------|------|
| Sentry 服务创建与路由流程 | https://nio.feishu.cn/docx/doxcnSrnL1u5IVDPdwHzp7JxPHf | Sentry 服务申请、路由部署、审批流程 |
| 接入 NIO SSO 手册 | https://nio.feishu.cn/wiki/wikcnY2XmUP4el9Mx955hdIdpyg | SSO 域名映射、环境区分、Sentry 申请入口 |
| OpenSSO Guide & FAQ | https://nio.feishu.cn/docs/doccn6I6QMJBia5rY74zSNdU0rS | OAuth2 接入步骤、token 有效期、MFA、FAQ |
| Sentry Client 创建与 Secret 切换 | https://nio.feishu.cn/wiki/IEuUwcrMfiCZRckBDcLcuR41n5g | app_id/app_secret 管理、权限移交、集群同步 |
| Sentry2.0 使用说明 | https://nio.feishu.cn/wiki/Vh7fwRctIiBACmkbyYmcctZvntc | Sentry/MK 网关架构、插件功能、服务创建、签名规范 |

---

## 9. 下一步行动清单（Action Items）

### 本周必须完成
- [ ] 在 Sentry 创建 `AD_PhoneTalk_TunnelHub` 服务
- [ ] 在鲁班平台暴露 `phonetalk-tunnelhub-svc`
- [ ] 申请 `phonetalk-tunnel-dev.nioint.com` 域名
- [ ] 构建并推送 `tunnelhub:v0.0.3` 镜像
- [ ] `kubectl apply -f tunnelhub/k8s-deployment.yaml`
- [ ] 验证 `curl https://phonetalk-tunnel-dev.nioint.com/health` 返回 200
- [ ] 运行 `agentgw login` 获取真实 accessToken
- [ ] 运行 `verify_sso_profile.py` 确认 profile 字段名
- [ ] 端到端验证：agentgw → dev tunnelhub → agentapp (LAN / wss 双模式)

### 下周推进
- [ ] 根据实测 profile JSON 微调 `internal/sso/validator.go` 字段映射（如有必要）
- [ ] 重新构建并发布 agentgw + agentd-linux 二进制
- [ ] 分发 agentapp.apk 到 Android 测试机
- [ ] 输出用户安装手册和运维 on-call 文档

### 待决策
- [ ] 是否需要在 K8s 中开启 HPA 自动扩缩容？
- [ ] 日志采集是否必须切 JSON 格式并接入 Prometheus？

---

## 10. 已知风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| Cloudflared 免费隧道极不稳定 | PoC 阶段已验证 2-3 秒重连 | 已切换到公司内部域名 + K8s |
| K8s 多副本无 sessionAffinity | agentapp 可能被路由到无 tunnel 的 Pod | 已在 Service 配置 ClientIP sticky |
| OpenSSO localhost redirect_uri 未注册 | `agentgw login` 浏览器登录后无法自动回调 | 已提供 Playwright 脚本 + 手动输入 fallback |
| agentapp 尚未实现 Flutter OAuth2 | 手机端连正式域名需手动粘贴 token | 优先保证 agentgw + Web 可用；app 后续迭代 |
| iOS 签名/分发流程长 | 影响正式员工使用 | 优先保证 Android + Web 可用；iOS 走 TestFlight 或企业分发 |

---

*本文档由 2026-04-17 更新，当前版本对应 tunnelhub:v0.0.3。*
