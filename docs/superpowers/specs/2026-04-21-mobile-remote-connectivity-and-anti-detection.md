# 移动端远程连接 & 反检测方案

> 日期：2026-04-21
> 状态：已实现核心功能，部分优化待排期

## 1. 问题背景

手机 App 需要通过 tunnelhub 远程连接本地 agentgw，面临两层网络审查：

1. **运营商 DPI（GFW）**：基于 TLS 指纹过滤，非 Chrome 指纹的 TLS 连接被 RST
2. **企业 NGFW**：基于协议签名 + IP/SNI 黑名单检测反代隧道（如 Tailscale、Cloudflare WARP）

## 2. 已解决问题

### 2.1 运营商 TLS 指纹封锁

**现象**：手机通过蜂窝网络（4G/5G）连接 `wss://ilovin.xyz` 时，Dart/OkHttp/curl 均收到 `Connection reset by peer`，但手机浏览器可正常访问。

**根因**：运营商 DPI 识别 TLS Client Hello 指纹，非 Chrome 指纹（Dart BoringSSL、OkHttp Conscrypt、curl BoringSSL）被直接 RST。

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

### 2.2 Agent 工作状态误判

**现象**：Claude 输出 `Generating...` 纯文本时，App 显示 idle 而非 working。

**根因**：status 检测仅通过 `tool_use` 判断 working，纯文本 assistant 消息一律标为 standby。

**方案**：利用 Claude JSONL 的 `stop_reason` 字段：
- `stop_reason` 为空（仍在 streaming）→ working
- `stop_reason` = `end_turn` → standby
- `tool_use` → working（不变）

### 2.3 QR 码显示错误端口

**现象**：agentgw `--qr` 输出的远程 URL 包含 `:8443`，但 Caddy 监听 443。

**修复**：`buildRemoteQRURL()` 中 `u.Host` 改为 `u.Hostname()` 去掉端口。

### 2.4 代码块展开折叠

**现象**：多行代码块在手机上显示不完整，无法查看完整代码。

**方案**：超过 8 行的代码块自动折叠，底部显示"展开全部/收起"按钮。

## 3. 待解决问题

### 3.1 远程 APK 在线更新（高优先级）

**现状**：
- agentgw 提供 `/apk` 和 `/apk/version` 端点，本地 WiFi 可正常下载
- 远程通过 tunnelhub 的 HTTPS 下载被 DPI 拦截（Dart HttpClient 非 Chrome TLS 指纹）

**方案**：APK 下载走 gRPC-Web tunnel relay
- tunnelhub 增加 HTTP relay 能力，收到 `/apk` 请求时通过已有 tunnel 转发到 agentgw
- 手机 App 通过 WebSocket 连接（已绕过 DPI）请求 APK 版本信息和下载
- agentgw 加 `apk.download` RPC，分片传输

**备选**：App 侧用 WebView JS fetch API 做 HTTP 请求（复用 Chrome TLS），但大文件流式下载不便。

### 3.2 会话历史缓存（中优先级）

**现状**：App 重连后所有历史记录丢失，需要完全重新加载。

**方案**：基于 LRU 的 session 缓存
- 本地 SQLite 缓存最近 N 个 session 的对话历史
- 重连后先展示缓存，后台增量同步新消息
- 按 session 活跃度淘汰旧缓存

### 3.3 长连接指纹优化（低优先级）

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

## 4. 反检测架构总结

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

## 5. 相关文件

| 文件 | 说明 |
|---|---|
| `agentapp/android/.../NativeWebSocketPlugin.kt` | WebView WebSocket 原生插件 |
| `agentapp/lib/services/native_ws_channel.dart` | Dart 侧 WebSocket 通道适配 |
| `agentapp/lib/services/ws_connector_io.dart` | 平台条件连接（Android 走 native） |
| `agentd/internal/watcher/claude.go` | Claude JSONL 解析 + stop_reason 状态检测 |
| `agentgw/internal/tunnel/client.go` | gRPC-Web tunnel 客户端 |
| `agentgw/cmd/agentgw/main.go` | agentgw 入口（QR、APK 端点、tunnel 管理） |
| `agentapp/lib/screens/settings_screen.dart` | 设置页（APK 更新检查） |
