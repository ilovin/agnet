# 反检测与远程连接架构

> 合并文档，原文件：`2026-04-20-anti-detection-grpc-web-reality.md`、`2026-04-21-mobile-remote-connectivity-and-anti-detection.md`
> 日期：2026-04-20 ~ 2026-04-21
> 状态：已实施

---

## 1. 问题背景

### 1.1 原架构问题（WebSocket 长连接）

原架构中 App ↔ tunnelhub ↔ agentgw 之间使用长连接 WebSocket，存在以下问题：

1. **流量特征明显**：长连接 WebSocket 的 Upgrade 握手和持续帧传输是显著的协议指纹，容易被 DPI（深度包检测）识别和阻断
2. **SNI 阻断**：`.xyz` 域名在部分网络环境下被 SNI 过滤直接阻断，无法使用 443 端口
3. **端口特征**：使用非标准端口（8443）增加了被识别为代理流量的风险

### 1.2 移动端问题（TLS 指纹封锁）

手机 App 需要通过 tunnelhub 远程连接本地 agentgw，面临两层网络审查：

1. **运营商 DPI（GFW）**：基于 TLS 指纹过滤，非 Chrome 指纹的 TLS 连接被 RST
2. **企业 NGFW**：基于协议签名 + IP/SNI 黑名单检测反代隧道（如 Tailscale、Cloudflare WARP）

**现象**：手机通过蜂窝网络（4G/5G）连接 `wss://ilovin.xyz` 时，Dart/OkHttp/curl 均收到 `Connection reset by peer`，但手机浏览器可正常访问。

**根因**：运营商 DPI 识别 TLS Client Hello 指纹，非 Chrome 指纹（Dart BoringSSL、OkHttp Conscrypt、curl BoringSSL）被直接 RST。

### 1.3 对比：正常 AI 工具的流量特征

Claude Code、Kimi 等 AI 工具的网络通信模式：
- HTTPS 连接到知名域名（api.anthropic.com、api.moonshot.cn）
- HTTP/2 单次建联，server-streaming 返回
- TLS 握手使用标准 Chrome 指纹
- 流量模式与普通 Web API 调用无异

**目标**：让 tunnelhub 的流量特征与访问 Google 服务无异。

---

## 2. 架构方案：gRPC-Web over HTTP/2 + REALITY

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

## 3. 移动端连接方案

### 3.1 WebView 绕过 TLS 指纹封锁

**方案**：使用 Android WebView（Chromium 引擎）建立 WebSocket 连接，获得与 Chrome 浏览器完全一致的 TLS 指纹。

**实现**：
- `NativeWebSocketPlugin.kt`：WebView 加载内嵌 HTML + JS，通过 `@JavascriptInterface` 桥接
- `native_ws_channel.dart`：Dart 侧 `WebSocketChannel` 适配器
- `ws_connector_io.dart`：Android + wss:// 时优先走 native WebView 通道

**踩坑记录**：
| 问题 | 原因 | 修复 |
|---|---|---|
| `sink.success` 抛异常 | `@JavascriptInterface` 在 JavaBridge 线程，`EventSink` 要求主线程 | `mainHandler.post { sink.success(event) }` |
| RPC 请求发不出去 | `_NativeSink.add()` 走 `StreamChannelController` 空管道，未调 MethodChannel | 重写 `add()` 调用 `invokeMethod('send', ...)` |
| 断连后重连无反应 | Flutter engine 后台 detach 后 `register()` 单例阻止重新注册 | `register()` 先 `cleanup()` 再重建 |
| EventChannel listener 泄漏 | `_NativeSink.close()` 未取消 EventChannel 订阅 | close 时调用 `_sub.cancel()` |

### 3.2 Agent 工作状态检测优化

**现象**：Claude 输出 `Generating...` 纯文本时，App 显示 idle 而非 working。

**根因**：status 检测仅通过 `tool_use` 判断 working，纯文本 assistant 消息一律标为 standby。

**方案**：利用 Claude JSONL 的 `stop_reason` 字段：
- `stop_reason` 为空（仍在 streaming）→ working
- `stop_reason` = `end_turn` → standby
- `tool_use` → working（不变）

### 3.3 QR 码端口修复

**现象**：agentgw `--qr` 输出的远程 URL 包含 `:8443`，但 Caddy 监听 443。

**修复**：`buildRemoteQRURL()` 中 `u.Host` 改为 `u.Hostname()` 去掉端口。

### 3.4 代码块展开折叠

**现象**：多行代码块在手机上显示不完整，无法查看完整代码。

**方案**：超过 8 行的代码块自动折叠，底部显示"展开全部/收起"按钮。

---

## 4. 连接管理优化

### 4.1 懒心跳（替代定时 Ping）

之前：每 30 秒发送一次 WebSocket ping，产生固定间隔的流量特征。

之后：
- 记录 `_lastReceivedAt` 时间戳
- 仅在发起 RPC 调用前检查：若距上次收到数据超过 60 秒，才发送一次 ping
- 无操作时完全静默，无周期性流量特征

### 4.2 指数退避重连

```
初始延迟: 3s
退避策略: delay = min(delay * 2, 60s)
抖动:     delay += random(0, delay/3)
```

避免断线后大量客户端同时重连造成的流量突增。

### 4.3 会话加载分页

Agent 详情页的事件加载从一次性 200 条改为分页：
- 初始加载：30 条（减少首屏等待）
- 滚动加载：到达顶部时触发 `_loadOlderHistory()`，每次 30 条
- 弱网环境下显著改善体验，避免 "无会话" 错误

---

## 5. 待解决问题

### 5.1 远程 APK 在线更新（高优先级）

**现状**：
- agentgw 提供 `/apk` 和 `/apk/version` 端点，本地 WiFi 可正常下载
- 远程通过 tunnelhub 的 HTTPS 下载被 DPI 拦截（Dart HttpClient 非 Chrome TLS 指纹）

**方案**：APK 下载走 gRPC-Web tunnel relay
- tunnelhub 增加 HTTP relay 能力，收到 `/apk` 请求时通过已有 tunnel 转发到 agentgw
- 手机 App 通过 WebSocket 连接（已绕过 DPI）请求 APK 版本信息和下载
- agentgw 加 `apk.download` RPC，分片传输

**备选**：App 侧用 WebView JS fetch API 做 HTTP 请求（复用 Chrome TLS），但大文件流式下载不便。

### 5.2 会话历史缓存（中优先级）

**现状**：App 重连后所有历史记录丢失，需要完全重新加载。

**方案**：基于 LRU 的 session 缓存
- 本地 SQLite 缓存最近 N 个 session 的对话历史
- 重连后先展示缓存，后台增量同步新消息
- 按 session 活跃度淘汰旧缓存

### 5.3 长连接指纹优化（低优先级）

**分析**：当前 agentgw → tunnelhub 使用 gRPC-Web 长流，NGFW 可能通过行为分析识别。

**但实际风险很低**：
- gRPC-Web 是 Google 广泛使用的协议（YouTube、Gmail、Docs），NGFW 不可能封杀
- Tailscale/Cloudflare WARP 被检测的原因是 WireGuard 协议指纹 + 已知 IP/SNI 黑名单，与连接时长无关
- 当前架构（自有域名 + Let's Encrypt + gRPC-Web + Chrome uTLS）已规避所有已知检测手段

**如需进一步优化**：
- URL 路径改为 Google 风格（`/youtubei/v1/...`）
- 随机 5-15 分钟断连重建（模拟页面切换）
- 混入 Google 域名配套请求（fonts、gstatic）
- REALITY 到国内可达 TLS 1.3 大站（百度、QQ）做 SNI 伪装

---

## 6. 部署配置

### 6.1 tunnelhub 服务端

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

### 6.2 agentgw 客户端

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

### 6.3 install.sh 一键安装

install.sh 已更新支持 REALITY：
- 默认 HUB URL 改为 `https://8.146.236.75:443`
- 通过 `AGENTGW_REALITY_PUB/SID/SNI` 环境变量传入密钥
- 自动将 REALITY 参数写入 `runtime.env` 并传递给 agentgw 启动参数
- 远程 QR 码 URL 格式更新为 `wss://{host}/api.v1.AgentService/Stream/{userId}`

---

## 7. 安全考量

- **密钥分发**：REALITY 公钥需通过安全渠道（如 SSH）分发给 agentgw，不应明文传输
- **证书验证**：agentgw 到 tunnelhub 使用 `InsecureSkipVerify`（因为 REALITY 使用自签证书），安全性由 X25519 密钥交换保证
- **回落安全**：未认证连接被代理到真实 Google，不会暴露任何服务信息
- **Token 认证**：gRPC-Web 请求通过 `Authorization: Bearer` 头携带 token，与之前一致

---

## 8. 反检测架构总结

```
agentgw ──(uTLS Chrome 指纹)──> ilovin.xyz:443 ──(Caddy + Let's Encrypt)──> tunnelhub ──(gRPC-Web)──> agentgw local
                                    |
phone app ──(WebView Chrome TLS)──> wss://ilovin.xyz/ws/{userId}
                                    |
NGFW 视角: 用户用 Chrome 访问 ilovin.xyz，gRPC-Web 流量（和 YouTube 一样）
```

**为什么检测不到**：

| 检测手段 | Tailscale/Cloudflare WARP | 本方案 |
|---|---|---|
| 协议指纹 | WireGuard UDP 明文头部（type=1 + index） | HTTPS + HTTP/2，无特征 |
| IP 黑名单 | DERP/WARP 服务器 IP 公开 | 自有 VPS，不在黑名单 |
| SNI 黑名单 | login.tailscale.com / warp.cloudflare.com | 自有域名 ilovin.xyz |
| TLS 指纹 | WireGuard 无 TLS | Chrome uTLS 指纹 |
| 流量模式 | UDP + 固定包大小 | TCP + HTTP/2 多路复用 |

---

## 9. 相关文件

| 文件 | 说明 |
|---|---|
| `agentapp/android/.../NativeWebSocketPlugin.kt` | WebView WebSocket 原生插件 |
| `agentapp/lib/services/native_ws_channel.dart` | Dart 侧 WebSocket 通道适配 |
| `agentapp/lib/services/ws_connector_io.dart` | 平台条件连接（Android 走 native） |
| `agentapp/lib/services/ws_client.dart` | 懒心跳替代定时 ping |
| `agentapp/lib/screens/agent_detail_screen.dart` | 分页加载（30 条/页） |
| `agentapp/lib/screens/settings_screen.dart` | 设置页（APK 更新检查） |
| `agentd/internal/watcher/claude.go` | Claude JSONL 解析 + stop_reason 状态检测 |
| `agentgw/internal/tunnel/client.go` | gRPC-Web tunnel 客户端，指数退避重连 |
| `agentgw/internal/tunnel/reality.go` | REALITY + uTLS 拨号，Chrome 指纹伪装 |
| `agentgw/cmd/agentgw/main.go` | agentgw 入口（QR、APK 端点、tunnel 管理） |
| `tunnelhub/internal/hub/hub.go` | gRPC-Web HTTP/2 streaming |
| `tunnelhub/internal/grpcweb/frame.go` | gRPC-Web 帧编解码 + Stream 类型 |
| `tunnelhub/internal/reality/config.go` | REALITY 密钥管理 + TLS Listener |
| `tunnelhub/deploy.sh` | 端口 443，REALITY 环境变量 |
| `scripts/install.sh` | REALITY 环境变量、gRPC-Web 路径、远程 QR 修复 |
| `scripts/deploy.sh` | restart_agentgw 加载 runtime.env |
