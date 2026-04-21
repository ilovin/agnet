# 反检测架构升级：gRPC-Web + REALITY

**日期**：2026-04-20
**状态**：已实施
**关联**：`2026-03-27-agent-manager-design.md` 第 2 节（网络拓扑）、第 6 节（认证与安全）

---

## 1. 问题

原架构中 App ↔ tunnelhub ↔ agentgw 之间使用长连接 WebSocket，存在以下问题：

1. **流量特征明显**：长连接 WebSocket 的 Upgrade 握手和持续帧传输是显著的协议指纹，容易被 DPI（深度包检测）识别和阻断
2. **SNI 阻断**：`.xyz` 域名在部分网络环境下被 SNI 过滤直接阻断，无法使用 443 端口
3. **端口特征**：使用非标准端口（8443）增加了被识别为代理流量的风险

### 对比：正常 AI 工具的流量特征

Claude Code、Kimi 等 AI 工具的网络通信模式：
- HTTPS 连接到知名域名（api.anthropic.com、api.moonshot.cn）
- HTTP/2 单次建联，server-streaming 返回
- TLS 握手使用标准 Chrome 指纹
- 流量模式与普通 Web API 调用无异

**目标**：让 tunnelhub 的流量特征与访问 Google 服务无异。

---

## 2. 方案：gRPC-Web over HTTP/2 + REALITY

### 2.1 架构变更

```
之前：
  App ──WebSocket──► tunnelhub:8443 ──WebSocket──► agentgw
                     (长连接，特征明显)

之后：
  App ──gRPC-Web/HTTP2──► tunnelhub:443 ──gRPC-Web/HTTP2──► agentgw
                          (REALITY TLS)
                          SNI: www.google.com
                          指纹: Chrome 125
```

### 2.2 三层伪装

| 层级 | 技术 | 效果 |
|------|------|------|
| TLS 层 | REALITY 协议 | SNI 显示为 `www.google.com`，未认证连接被透明代理到真实 Google |
| TLS 指纹 | uTLS (HelloChrome_Auto) | TLS ClientHello 与 Chrome 125 完全一致 |
| 应用层 | gRPC-Web over HTTP/2 | Content-Type: `application/grpc-web+proto`，与 Google 服务流量一致 |

### 2.3 REALITY 协议工作原理

```
客户端 (agentgw)                    服务端 (tunnelhub:443)              真实站点 (www.google.com:443)
     │                                    │                                    │
     │── TLS ClientHello ────────────────►│                                    │
     │   SNI: www.google.com              │                                    │
     │   uTLS Chrome 指纹                 │                                    │
     │                                    │── 验证 X25519 密钥 ──►             │
     │                                    │   认证通过？                        │
     │                                    │                                    │
     │   [认证通过]                        │                                    │
     │◄── ServerHello (自签证书) ─────────│                                    │
     │── gRPC-Web 数据流 ────────────────►│                                    │
     │                                    │                                    │
     │   [认证失败/未知客户端]              │                                    │
     │                                    │── 透明代理 ──────────────────────►│
     │◄── Google 真实响应 ────────────────│◄─────────────────────────────────│
```

关键特性：
- **X25519 密钥交换**：服务端生成密钥对，客户端持有公钥，嵌入 TLS ClientHello 中验证身份
- **透明回落**：未持有正确密钥的连接（包括主动探测）被透明代理到 `www.google.com`，返回真实 Google 页面
- **零特征**：从外部观察，所有连接都是标准的 HTTPS 到 Google

### 2.4 gRPC-Web 帧格式

```
┌──────────┬──────────────┬─────────────┐
│ Flag (1B)│ Length (4B)   │ Payload     │
│ 0x00=数据│ BigEndian     │ JSON-RPC    │
│ 0x80=尾帧│              │ 消息体       │
└──────────┴──────────────┴─────────────┘
```

- 数据帧 (0x00)：携带 JSON-RPC 消息
- 尾帧 (0x80)：标记流结束（gRPC trailers）

### 2.5 通信模式

**agentgw → tunnelhub（注册 + 双向流）：**

```
POST /api.v1.TunnelService/Register
Content-Type: application/grpc-web+proto
Authorization: Bearer <token>

请求体 (io.Pipe): agentgw→hub 的 gRPC-Web 帧流
响应体 (streaming): hub→agentgw 的 gRPC-Web 帧流
```

使用 HTTP/2 的 server-streaming 实现双向通信：
- 请求体通过 `io.Pipe` 持续写入（local→hub 方向）
- 响应体持续读取（hub→local 方向）

**App → tunnelhub（连接 + 双向流）：**

```
POST /api.v1.AgentService/Stream/{userId}
Content-Type: application/grpc-web+proto
Authorization: Bearer <token>
```

同样使用 server-streaming 模式。

---

## 3. 连接管理优化

### 3.1 懒心跳（替代定时 Ping）

之前：每 30 秒发送一次 WebSocket ping，产生固定间隔的流量特征。

之后：
- 记录 `_lastReceivedAt` 时间戳
- 仅在发起 RPC 调用前检查：若距上次收到数据超过 60 秒，才发送一次 ping
- 无操作时完全静默，无周期性流量特征

### 3.2 指数退避重连

```
初始延迟: 3s
退避策略: delay = min(delay * 2, 60s)
抖动:     delay += random(0, delay/3)
```

避免断线后大量客户端同时重连造成的流量突增。

### 3.3 会话加载分页

Agent 详情页的事件加载从一次性 200 条改为分页：
- 初始加载：30 条（减少首屏等待）
- 滚动加载：到达顶部时触发 `_loadOlderHistory()`，每次 30 条
- 弱网环境下显著改善体验，避免 "无会话" 错误

---

## 4. 部署配置

### 4.1 tunnelhub 服务端

```bash
# 监听 443 端口，REALITY 自动处理 TLS
tunnelhub --port 443 \
  --reality-dest www.google.com:443 \
  --reality-sni www.google.com
# 首次启动自动生成 X25519 密钥对，持久化到 reality_keys.json
```

密钥文件 `reality_keys.json`：
```json
{
  "private_key": "<base64 X25519 私钥>",
  "public_key": "<base64 X25519 公钥>",
  "short_id": "<hex 短 ID>"
}
```

### 4.2 agentgw 客户端

```bash
# 通过环境变量或命令行参数配置
agentgw start --hub https://8.146.236.75:443 \
  --reality-pub <公钥base64> \
  --reality-sni www.google.com \
  --reality-sid <短ID hex> \
  --qr
```

环境变量（写入 `~/.agentgw/runtime.env`）：
```
AGENTGW_HUB=https://8.146.236.75:443
AGENTGW_REALITY_PUB=<公钥>
AGENTGW_REALITY_SID=<短ID>
AGENTGW_REALITY_SNI=www.google.com
```

### 4.3 install.sh 一键安装

install.sh 已更新支持 REALITY：
- 默认 HUB URL 改为 `https://8.146.236.75:443`
- 通过 `AGENTGW_REALITY_PUB/SID/SNI` 环境变量传入密钥
- 自动将 REALITY 参数写入 `runtime.env` 并传递给 agentgw 启动参数
- 远程 QR 码 URL 格式更新为 `wss://{host}/api.v1.AgentService/Stream/{userId}`

---

## 5. 变更文件清单

| 文件 | 变更 |
|------|------|
| `agentgw/internal/tunnel/client.go` | WebSocket → gRPC-Web HTTP/2 streaming，指数退避重连，Chrome UA |
| `agentgw/internal/tunnel/reality.go` | 新增：REALITY + uTLS 拨号，Chrome 指纹伪装 |
| `agentgw/cmd/agentgw/main.go` | 新增 `--reality-*` 参数，login/logout 使用 `AGENTGW_HUB` |
| `tunnelhub/internal/hub/hub.go` | WebSocket → gRPC-Web HTTP/2 streaming |
| `tunnelhub/internal/grpcweb/frame.go` | 新增：gRPC-Web 帧编解码 + Stream 类型 |
| `tunnelhub/internal/reality/config.go` | 新增：REALITY 密钥管理 + TLS Listener |
| `tunnelhub/deploy.sh` | 端口 443，REALITY 环境变量 |
| `agentapp/lib/services/ws_client.dart` | 懒心跳替代定时 ping |
| `agentapp/lib/screens/agent_detail_screen.dart` | 分页加载（30 条/页） |
| `scripts/install.sh` | REALITY 环境变量、gRPC-Web 路径、远程 QR 修复 |
| `scripts/deploy.sh` | restart_agentgw 加载 runtime.env |

---

## 6. 安全考量

- **密钥分发**：REALITY 公钥需通过安全渠道（如 SSH）分发给 agentgw，不应明文传输
- **证书验证**：agentgw 到 tunnelhub 使用 `InsecureSkipVerify`（因为 REALITY 使用自签证书），安全性由 X25519 密钥交换保证
- **回落安全**：未认证连接被代理到真实 Google，不会暴露任何服务信息
- **Token 认证**：gRPC-Web 请求通过 `Authorization: Bearer` 头携带 token，与之前一致
