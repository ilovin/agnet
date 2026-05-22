# R-007: Hermes Agent Provider Support

**需求编号**: R-007
**日期**: 2026-05-21
**状态**: 进行中 (In Progress)

---

## 概述

将 `hermes-agent` 作为新的 Provider 接入 agentd，使其与 `claude`、`opencode` 并列。

### 集成路径确认（2026-05-21）

经 `hermes-explorer` 深度协议探测，Hermes **Gateway HTTP API** 是最优集成方式：
- Hermes 在 `127.0.0.1:8642` 暴露 OpenAI-compatible API
- 支持 SSE streaming 流式响应
- 会话连续性通过 `X-Hermes-Session-Id` header 维持
- 非 loopback 访问需配置 `API_SERVER_HOST=0.0.0.0` + `API_SERVER_KEY`

HTTP 端点：
| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/chat/completions` | POST | OpenAI-compatible chat completions with SSE |
| `/v1/runs` | POST | Async runs with SSE event streams |
| `/v1/capabilities` | GET | Feature list (health check) |

CLI 行为（备用参考）：
- `-z "prompt"` → pure text stdout, exit code 始终 0（不可靠）
- `--continue <sid>` → 真正追加到现有 session
- 数据存储：`~/.hermes/state.db`（SQLite，`sessions` + `messages` 表）

---

## 验收标准

### AC-1: Provider 识别
- agentd 的 scanner 能正确识别 `hermes` 进程
- `agent.create` 支持 `provider: "hermes"`

### AC-2: Gateway 生命周期管理
- agentd 检测到 hermes 时，检查 gateway 是否在本地运行（默认可配置端口）
- gateway 未运行时，agentd 可启动 `hermes gateway run` 并等待就绪
- agentd 通过 HTTP API 与 hermes 交互（非 PTY/stdin）

### AC-3: 对话交互
- 支持通过 JSON-RPC `conversation.send` 向 hermes 发送消息
- 支持 SSE streaming，实时推送 hermes 响应到 EventBuffer
- 支持 `conversation.history` 加载 hermes 历史会话

### AC-4: 会话管理
- 支持 `session.attach` 附加到已存在的 hermes 会话（通过 `X-Hermes-Session-Id`）
- 支持 `agent.restart` 重启 hermes gateway
- 会话通过 header 维持

### AC-5: 状态检测
- 通过 gateway `/v1/capabilities` 或 HTTP availability 检测状态
- 能检测 gateway 进程崩溃并上报

### AC-6: 集成到 App
- App 仪表盘能显示 hermes agent 状态和对话
- 对话视图支持 hermes 的消息类型

---

## 设计决策

### 核心集成模式：HTTP Gateway Client
- agentd 内部实现 `HermesGatewayClient`，用 Go 标准 HTTP client + SSE decoder
- 每次 `conversation.send` 发 `POST /v1/chat/completions`，携带 `X-Hermes-Session-Id`
- SSE stream 解析后 push 到 EventBuffer（类似 claude stream JSON）

### Hermes vs Claude/OpenCode 差异
| 维度 | Claude | OpenCode | Hermes |
|------|--------|----------|--------|
| Process | PTY `claude -p` | PTY `opencode` | Gateway daemon (`hermes gateway`) |
| Communication | stdin/stderr | stdin + SQLite | HTTP localhost |
| Session ID | JSONL path | SQLite ID | `X-Hermes-Session-Id` |
| Streaming | stream-JSON | N/A (async) | SSE |
| History | JSONL 文件 | SQLite 轮询 | `state.db` messages 表 |

### Session 持久化
- hermes session ID 由 agentd 从 gateway response header 获取并持久化到 SQLite store
- 重启 agent 时，恢复 session ID 并在请求中携带

---

## 实现范围

### agentd
- **scanner**: 识别 hermes 进程
- **hermesclient/**: 新增 HTTP gateway client（`chat_completions.go`, `sse.go`, `config.go`）
- **hermesclient/client_test.go**: 单元测试
- **watcher/hermes.go**: HermesWatcher，实现 SessionWatcher 接口，轮询 gateway 状态
- **ws/agent_service.go**: 添加 hermes 的启动命令解析（`hermes gateway run`）
- **ws/handler.go**: `conversation.send` 路由到 hermes gateway client
- **store**: 无需改动，session ID 已存在字段

### agentapp
- Provider 名称映射 "hermes" → UI 图标/标签
- 处理 hermes 的流式消息（SSE 流已按 plain text push）

### agentgw
- 透传，无需修改

---

## 不在范围内

- hermes 在远端机器上的安装部署脚本
- hermes gateway 的外部配置管理（`API_SERVER_KEY`, `API_SERVER_HOST` 需预配置）
- hermes 的 TUI 模式支持
- hermes gateway 的多实例管理（第一版只支持单实例）

---

## 风险

| 风险 | 影响 | 缓解措施 |
|------|------|--------|
| hermes gateway 未预配置 `API_SERVER_KEY` 无法从外部访问 | 高 | 部署时要求用户配置 gateway；若未配置则降级为 PTY stdin 模式 |
| gateway 崩溃后 session ID 丢失 | 中 | session ID 持久化到 agentd store；恢复时传给新 gateway |
| SSE 解析与现有 stream-JSON 冲突 | 低 | 独立 SSE parser；事件归一化为标准 ConversationEvent |
| HTTPT 断线重连 | 低 | 指数退避重试， Agent rest 后重新发现 gateway |
