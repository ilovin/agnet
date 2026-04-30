# Agent Manager 系统设计文档

**日期**：2026-03-27
**状态**：草稿

---

## 1. 概述

Agent Manager 是一个多 AI Agent 远程管理系统，支持通过手机 App 或本机对多台远端机器上运行的 AI Agent（Claude Code、OpenCode 等）进行监控、对话和启停控制。

### 核心价值

- **统一视图**：多台远端机器、多个 Agent，一个界面全览
- **对话管理**：直接在手机上与远端 Agent 对话，查看历史
- **监控控制**：实时看到 Agent 状态，随时启停
- **易于部署**：Gateway 一键将 agentd 部署到远端机器

### 不在范围内

- Agent 审批机制（当前使用 bypass 模式）
- PTY 终端级别的完整输出展示

---

## 2. 网络拓扑

> **注意**：公网穿透已通过 tunnelhub + REALITY 实现，详见 `2026-04-20-anti-detection-grpc-web-reality.md`

```
手机 App ──gRPC-Web/HTTP2──► tunnelhub:443 ──gRPC-Web/HTTP2──► 本机 Gateway ──SSH Tunnel + WS──► 远端 agentd
(Flutter)                    (REALITY TLS)                     (agentgw, Go)
                             SNI: www.google.com
```

### 网络可达性

- 本机与远端：通过公网 SSH 互通
- 手机与本机（局域网）：直连 `ws://<IP>:7374/ws`
- 手机与本机（跨网络）：通过 tunnelhub 中转，gRPC-Web over REALITY（伪装为 Google 流量）
- 手机与远端（可选）：远端也加入 Tailscale 时，App 可绕过 Gateway 直连

### 三个独立组件

| 组件 | 部署位置 | 默认端口 | 职责 |
|------|---------|---------|------|
| `agentd` | 每台远端机器 | 7373 | 管理本机 Agent 进程，暴露 WS API |
| `agentgw` | 本机 | 7374 | 聚合多台 agentd，为 App 提供统一接口，部署远端 agentd |
| `agentapp` | 手机 | - | UI：监控仪表盘、对话视图、启停控制 |
| `tunnelhub` | 公网服务器 | 443 | 中转 App ↔ agentgw 流量，REALITY TLS 伪装 |

---

## 3. 远端 Daemon（agentd）

### 3.1 设计原则

参考 OpenCove 的设计思想（多 Provider 抽象、JSONL 文件监听、Agent 状态机），用 Go 从头实现为独立 daemon，编译成单二进制，无运行时依赖，易于部署到远端。

### 3.2 进程模型

```
agentd (Go 进程，常驻)
├── WebSocket Server (:7373)
├── AgentManager
│   ├── Agent "claude-1"
│   │   ├── PTY 子进程 (go-pty)
│   │   ├── ConversationParser (JSONL 监听 / HTTP 轮询)
│   │   ├── TurnStateWatcher (working / standby 检测)
│   │   └── EventBuffer (最近 N 条事件，用于断线重放)
│   └── Agent "opencode-1"
│       └── ...
└── AuthHandler (静态 token 校验)
```

### 3.3 Agent 状态机

```
Created → Starting → Idle (Standby) ⇄ Working → Stopped
                                              ↓
                                           Crashed → (自动或手动重启)
```

### 3.4 多 Provider 抽象

参考 OpenCove 的 `AgentProviderId`，每个 Provider 实现统一接口：

| Provider | 启动命令 | 会话状态检测 | Resume 方式 |
|----------|---------|------------|------------|
| Claude Code | `claude` | 监听 `~/.claude/projects/<cwd>/*.jsonl` | `claude --resume <id>` |
| OpenCode | `opencode` | 轮询 localhost HTTP API | `opencode --session <id>` |
| Codex | `codex` | 监听 JSONL 文件 | `codex resume <id>` |
| Gemini CLI | `gemini` | 无（fallback） | `gemini --resume <id>` |

### 3.5 对话解析策略

- **Claude Code / Codex**：监听 JSONL 文件，增量读取，解析 `type=assistant` 行提取对话内容和状态
- **OpenCode**：轮询 localhost HTTP API（OpenCode 自带）
- **通用 fallback**：PTY 原始输出（至少能显示，不一定结构化）

### 3.6 WebSocket API（JSON-RPC 2.0）

```
// Agent 生命周期
agent.list                          → 所有 agent 及状态
agent.create  {provider, cwd, name} → 启动新 agent
agent.stop    {agentId}             → 停止 agent
agent.restart {agentId}             → 重启 agent

// 对话
conversation.send    {agentId, message} → 发送消息
conversation.history {agentId, cursor}  → 获取历史（分页）

// Server Push 事件
event: agent.status_changed   {agentId, status}
event: conversation.message   {agentId, message, role}  ← 流式
event: conversation.thinking  {agentId}
```

### 3.7 EventBuffer

每个 Agent 维护一个内存 EventBuffer（单调递增 seq，最多 10000 条），用于客户端断线重连后的事件重放。重连时客户端提供 `lastSeq`，daemon 补发缺失事件。

---

## 4. 本机 Gateway（agentgw）

### 4.1 进程模型

```
agentgw (Go 进程，常驻)
├── WebSocket Server (:7374)         ← App 连接入口
├── NodeManager
│   ├── Node {id, host, port, sshKey}
│   │   ├── SSH Tunnel (golang.org/x/crypto/ssh)
│   │   ├── WS Client → agentd:7373 (through tunnel)
│   │   └── EventBuffer (聚合层断线重放)
│   └── Node ...
├── Deployer                         ← 一键部署 agentd
└── AuthHandler (静态 token 校验)
```

### 4.2 agentd 一键部署流程

agentd 二进制通过 `go:embed` 内嵌到 agentgw 二进制中：

```
App 发起 node.deploy 请求
    ↓
Gateway SSH 连接远端
    ↓
检查远端 agentd 版本 hash
    ↓ (不匹配或不存在)
SCP 上传内嵌的 agentd 二进制到 ~/.agentd/agentd
    ↓
SSH 执行: ./agentd start --daemon
    ↓
建立 SSH Tunnel → WS 连接到 agentd:7373
    ↓
返回 App: node 上线
```

### 4.3 对 App 暴露的 API（JSON-RPC 2.0）

Gateway 将多节点打平为统一命名空间：

```
// 节点管理
node.list                              → 所有节点及连接状态
node.add    {host, port, sshKey, name} → 添加节点
node.deploy {nodeId}                   → 部署/更新 agentd
node.remove {nodeId}                   → 移除节点

// Agent 操作（透传到对应节点 agentd）
agent.list    {nodeId}
agent.create  {nodeId, provider, cwd, name}
agent.stop    {nodeId, agentId}
agent.restart {nodeId, agentId}

// 对话（透传）
conversation.send    {nodeId, agentId, message}
conversation.history {nodeId, agentId, cursor}

// Server Push 事件（聚合所有节点）
event: node.status_changed    {nodeId, status}
event: agent.status_changed   {nodeId, agentId, status}
event: conversation.message   {nodeId, agentId, message, role}
event: conversation.thinking  {nodeId, agentId}
```

### 4.4 连接模式

App 可通过三种方式连接：

1. **Gateway 局域网模式**：`ws://<本机 IP>:7374/ws`（同局域网直连，所有功能可用）
2. **Gateway 远程模式**：`wss://<tunnelhub>:443/api.v1.AgentService/Stream/{userId}`（通过 tunnelhub 中转，REALITY 伪装）
3. **直连模式**：`ws://<远端 Tailscale IP>:7373`（远端也加入 Tailscale 时，绕过 Gateway）

---

## 5. 手机 App（agentapp）

### 5.1 技术栈

**Flutter**（推荐）：自绘引擎保证跨平台一致性，UI 简单（列表+对话），单二进制部署省心。如熟悉 JS/TS 可选 React Native，功能等价。

### 5.2 页面结构

```
App
├── ConnectionsScreen          ← 管理 Gateway / 直连节点
│   └── AddConnectionSheet    ← 添加新连接（host/port/token）
├── DashboardScreen           ← 所有节点所有 Agent 汇总
│   ├── NodeCard × N          ← 每台机器一张卡片
│   │   └── AgentRow × M      ← 每个 Agent（状态徽章 + 简要信息）
│   └── GlobalStatusBar       ← 汇总：N 个 Agent 运行中
├── AgentDetailScreen         ← 单个 Agent 详情
│   ├── StatusBadge           ← working / standby / stopped / crashed
│   ├── ConversationView      ← 对话历史，Markdown 渲染
│   ├── InputBar              ← 发送消息
│   └── ControlBar            ← 启动 / 停止 / 重启
└── SettingsScreen            ← Token 管理、连接配置
```

### 5.3 仪表盘 UI 示意

```
┌─────────────────────────────┐
│  🟢 remote1  (已连接)       │
│  ├ 🔵 claude-1   Working…  │
│  ├ 🟡 opencode-1  Standby  │
│  └ [+ 新建 Agent]           │
├─────────────────────────────┤
│  🟢 remote2  (已连接)       │
│  ├ 🔵 claude-1   Working…  │
│  └ [+ 新建 Agent]           │
├─────────────────────────────┤
│  🔴 remote3  (重连中…)      │
└─────────────────────────────┘
```

### 5.4 状态徽章定义

| 徽章 | 含义 |
|------|------|
| 🔵 Working | Agent 正在处理请求 |
| 🟡 Standby | Agent 就绪，等待输入 |
| ⚫ Stopped | Agent 已停止 |
| 🔴 Crashed | Agent 异常退出 |
| 🔄 Starting | Agent 启动中 |

### 5.5 断线重连

- 检测到 WebSocket 断开后，自动指数退避重连
- 重连成功后携带 `lastSeq` 请求事件重放
- 重连期间显示 overlay 提示，不阻塞已有内容浏览

---

## 6. 认证与安全

- **传输安全**：agentgw ↔ agentd 通过 SSH Tunnel 加密；App ↔ tunnelhub ↔ agentgw 通过 REALITY + gRPC-Web over HTTP/2 加密（伪装为 Google 流量，详见 `designs/anti-detection-connectivity.md`）
- **认证**：静态 token（在 agentd/agentgw 配置文件中设置，App 连接时通过 `Authorization: Bearer` 头携带）
- **SSH 密钥**：Gateway 使用本机 SSH 密钥连接远端，密钥存储在本机，不传输到 App
- **REALITY 密钥**：X25519 密钥对由 tunnelhub 自动生成，公钥需安全分发给 agentgw

---

## 7. 数据持久化

| 数据 | 存储位置 | 方式 |
|------|---------|------|
| 节点配置（host/key/name）| agentgw | YAML 配置文件 |
| Agent 元数据（id/provider/cwd）| agentd | SQLite |
| 对话历史 | agentd | 读取 agent 原始文件（JSONL 等），不另存 |
| EventBuffer | agentd | 内存（重启后从 agent 文件重建） |
| App 连接配置 | agentapp | Flutter SharedPreferences / RN AsyncStorage |

---

## 8. 技术选型汇总

| 组件 | 语言/框架 | 关键依赖 |
|------|---------|---------|
| agentd | Go | `go-pty`、`gorilla/websocket`、`mattn/go-sqlite3` |
| agentgw | Go | `golang.org/x/crypto/ssh`、`gorilla/websocket`、`refraction-networking/utls`、`go:embed` |
| agentapp | Flutter (Dart) | `web_socket_channel`、`flutter_markdown`、`provider` 或 `riverpod` |
| 网络层 | Tailscale | 手机与本机之间的 P2P VPN |

---

## 9. 参考项目

| 项目 | 参考价值 |
|------|---------|
| [OpenCAN](https://github.com/TennyZhuang/opencan) | SSH 传输层设计、daemon 部署机制、EventBuffer replay、断线重连 |
| [OpenCove](https://github.com/DeadWaveWave/opencove) | 多 Provider 抽象、Agent 状态机、JSONL 解析、Turn State Watcher 设计 |

---

## 10. 实现优先级（建议顺序）

1. **agentd MVP**：Claude Code provider + JSONL 解析 + WebSocket API + agent 启停
2. **agentgw MVP**：单节点 SSH Tunnel + 透传 agentd API + App 可连接
3. **agentapp MVP**：Dashboard + AgentDetail + ConversationView（Flutter）
4. **agentgw 一键部署**：Deployer + agentd 内嵌
5. **多 Provider**：OpenCode、Codex、Gemini CLI 支持
6. **直连模式**：App 直连远端 agentd（Tailscale）

---

## 11. 关键设计决策

- **agentd** is a single static binary with zero runtime dependencies — easy to SCP to remote machines
- **EventBuffer** uses a circular buffer (head/count indices) for O(1) append, not array shifting
- **PTY Kill order**: kill process first, then close ptmx fd (avoids SIGHUP zombies)
- **Agent struct stores cmd/args** so `agent.restart` reuses original launch parameters
- **agentgw NodeManager.LoadAll()** loads persisted nodes in batch at startup (avoids N redundant file writes)
- **agentgw event forwarding**: agentd push events get `nodeId` injected before broadcast to App clients

---

## 12. 会话发现流水线

PID-to-session mapping uses a multi-stage pipeline (scanner + watcher share the same logic):

1. **Task fd discovery** — check which `~/.claude/tasks/<sessionID>` dirs the PID has open (via `/proc` or `lsof`). If exactly one, use it directly.
2. **Candidate list** — always list ALL `.jsonl` files from the project dir, then merge in any task fd sessions not already present. Never use task fd as the exclusive candidate set — the current session may have no task dir yet.
3. **Time-based filtering** — if tmux pane activity is available, filter candidates by time proximity. Otherwise sort by lastActivity descending.
4. **Content matching** — capture tmux pane text, extract fingerprints from JSONL files, pick the best match by substring hit count.
5. **Fallback** — most recently active candidate wins.

Key invariant: the PID mapping file (`sessions/<pid>.json`) is NOT authoritative — it goes stale after `/clear` or `--resume`. Never trust it as the sole source.
