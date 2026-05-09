---
id: R-001
status: Completed
date: 2026-05-08
priority: High
source: architecture
---

# R-001: Agent Core — Daemon & Gateway Foundation

## 1. Overview

This requirement captures the foundational agent management capabilities implemented across `agentd` (remote daemon) and `agentgw` (gateway). It serves as the baseline architecture reference for all subsequent features.

**Scope**: Core agent lifecycle, process management, provider abstraction, event streaming, and gateway aggregation.  
**Status**: Implemented and operational in production.  
**Version**: agentd v0.1.0 / agentgw v0.1.0

---

## 2. Component Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        agentgw (Gateway)                     │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │ NodeManager │  │  Deployer   │  │  WebSocket Server   │  │
│  │  (SSH+WS)   │  │ (SCP agentd)│  │    (:7374)          │  │
│  └──────┬──────┘  └─────────────┘  └─────────────────────┘  │
└─────────┼────────────────────────────────────────────────────┘
          │ SSH Tunnel + WebSocket
┌─────────┼────────────────────────────────────────────────────┐
│  ┌──────┴──────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │  agentd     │  │  AgentManager│  │  WebSocket Server   │  │
│  │  (:7373)    │  │  (Lifecycle) │  │    (:7373)          │  │
│  └─────────────┘  └──────┬──────┘  └─────────────────────┘  │
│                          │                                   │
│  ┌─────────────┐  ┌──────┴──────┐  ┌─────────────────────┐  │
│  │ProcessManager│  │EventManager │  │   StreamParser      │  │
│  │ (PTY/spawn)  │  │(Persistence)│  │ (JSONL/stream-json) │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
│                                                             │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
│  │   Scanner   │  │   Watcher   │  │PermissionResolver   │  │
│  │(proc table) │  │(session file)│  │  (bypass/detect)   │  │
│  └─────────────┘  └─────────────┘  └─────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

---

## 3. agentd — Remote Daemon

### 3.1 Process Model

- **Single static binary**, zero runtime dependencies, compiled for Linux amd64 & Darwin arm64
- **SQLite persistence** (`~/.agentd/data/agents.db`) for agent metadata and conversation events
- **In-memory EventBuffer** per agent (circular buffer, 10,000 events, monotonic seq)
- **HTTP server** on `127.0.0.1:7373` with WebSocket upgrade endpoint `/ws`
- **Auto-attach** at startup: discovers running Claude/OpenCode processes via `/proc` or `lsof`
- **Periodic scan**: background goroutine re-attaches to newly spawned processes

### 3.2 Agent State Machine

```
Created → Starting → Idle (Standby) ⇄ Working → Stopped
                                              ↓
                                           Crashed → (manual restart)
```

**Status definitions**:
| Status | Meaning |
|--------|---------|
| `created` | Agent record created, process not yet spawned |
| `starting` | PTY process spawning in progress |
| `idle` | Process running, awaiting user input (standby) |
| `working` | Processing a request (detected via watcher) |
| `stopped` | Process terminated (PID=0 or dead) |
| `crashed` | Process exited unexpectedly |

### 3.3 Provider Abstraction

Each provider implements `Provider` interface:

```go
type Provider interface {
    Command(workDir string, resumeSessionID string) (cmd string, args []string)
    SessionFilePath(workDir string, sessionID string) string
}
```

**Supported providers**:

| Provider | Command | Session Detection | Resume Flag |
|----------|---------|-------------------|-------------|
| Claude Code | `claude --dangerously-skip-permissions` | JSONL file watcher (`~/.claude/projects/<cwd>/*.jsonl`) | `--resume <id>` |
| OpenCode | `opencode` | HTTP API polling + tmux pane | `-s <id>` |

### 3.4 Core Subsystems

#### 3.4.1 ProcessManager (`internal/agent/process_manager.go`)
- Spawns agent processes via PTY (using `go-pty`)
- Handles both pipe mode (`claude -p`) and interactive mode
- Manages process lifecycle: start, stop (SIGTERM → SIGKILL), restart
- Reads PTY output and feeds to StreamParser
- Session discovery pipeline: PID → task fd → candidate JSONL files → time-based filtering → content matching

#### 3.4.2 EventManager (`internal/agent/event_manager.go`)
- Appends events to agent's in-memory buffer with auto-increment seq
- Persists conversation events to SQLite (`conversation_events` table)
- Supports `updateOrAppend` for streaming message deduplication via `msg_id`
- Loads persisted events on demand (latest N or since seq)

#### 3.4.3 StreamParser (`internal/agent/stream_parser.go`)
- Parses JSONL and stream-json output from Claude Code
- Recognized event types: `init`, `message`, `user`, `assistant`, `tool_use`, `tool_result`, `result`, `permission_prompt`, `control_request`, `stream_event`, `system`, `message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_stop`
- Extracts tool result summaries for UI display

#### 3.4.4 PermissionResolver (`internal/agent/permission_resolver.go`)
- Detects permission prompts in PTY output
- Supports bypass mode (`--dangerously-skip-permissions`) for Claude Code
- Tracks permission prompt state to prevent duplicate handling

#### 3.4.5 Scanner (`internal/scanner/`)
- Cross-platform process discovery (Linux via `/proc`, Darwin via `lsof`/`ps`)
- Identifies Claude Code and OpenCode processes by command line
- Returns `ProcessInfo` with PID, command, working directory

#### 3.4.6 Watcher (`internal/watcher/`)
- **Claude watcher**: File system watcher on JSONL session files
- **OpenCode watcher**: HTTP API polling for conversation state
- Detects `working` vs `standby` state transitions
- Provides real-time conversation event streaming

### 3.5 WebSocket API (JSON-RPC 2.0)

**Agent lifecycle**:
| Method | Params | Description |
|--------|--------|-------------|
| `agent.list` | — | List all managed agents with status |
| `agent.create` | `{provider, cwd, name, env}` | Create and start new agent |
| `agent.stop` | `{agentId}` | Stop agent process |
| `agent.restart` | `{agentId}` | Restart agent with original params |
| `agent.scan` | — | Trigger manual process scan |
| `agent.attach` | `{pid, provider}` | Attach to existing process |

**Session management**:
| Method | Params | Description |
|--------|--------|-------------|
| `session.list` | `{agentId}` | List sessions for agent |
| `session.create` | `{agentId, name}` | Create new session |
| `session.switch` | `{agentId, sessionId}` | Switch to existing session |

**Conversation**:
| Method | Params | Description |
|--------|--------|-------------|
| `conversation.send` | `{agentId, message}` | Send message to agent |
| `conversation.history` | `{agentId, cursor, limit}` | Get paginated history |
| `conversation.clear` | `{agentId}` | Clear conversation state |

**Server Push events**:
| Event | Payload |
|-------|---------|
| `agent.status_changed` | `{agentId, status, oldStatus}` |
| `conversation.message` | `{agentId, message, role, seq}` |
| `conversation.thinking` | `{agentId, thinking}` |
| `client.connected` | `{clientId, time}` |
| `client.disconnected` | `{clientId, time}` |

### 3.6 HTTP Endpoints

| Endpoint | Auth | Description |
|----------|------|-------------|
| `GET /status` | None | Daemon status: version, uptime, sessions, connections |
| `GET /debug/stacks` | None | Full goroutine stack dump |
| `GET /ws` | Bearer token | WebSocket upgrade |

---

## 4. agentgw — Gateway

### 4.1 Process Model

- **Single static binary** with `go:embed` for agentd binary
- **YAML/JSON config** at `~/.agentgw/config.json`
- **HTTP server** on `0.0.0.0:7374` with WebSocket endpoint `/ws`
- **Static file serving** for Flutter Web portal (`static/` directory)
- **Hot restart** support: inherits listener FD for zero-downtime restart

### 4.2 Node Management

**Node states**:
| Status | Meaning |
|--------|---------|
| `disconnected` | No active WebSocket to agentd |
| `connecting` | SSH tunnel establishment in progress |
| `connected` | WebSocket active, bidirectional communication |
| `deploying` | agentd binary upload in progress |
| `error` | Connection failed or node unreachable |

**Node configuration**:
```json
{
  "id": "node-uuid",
  "name": "remote-server-1",
  "host": "192.168.1.100",
  "port": 7373,
  "sshPort": 22,
  "sshKeyPath": "~/.ssh/id_rsa",
  "sshAlias": "server1",
  "token": "shared-secret"
}
```

### 4.3 Core Subsystems

#### 4.3.1 NodeRegistry (`internal/node/registry.go`)
- Persists node configuration to JSON/YAML
- Loads nodes at startup with deduplication by host/alias
- Supports both static config and dynamic node additions

#### 4.3.2 TunnelManager (`internal/node/tunnel_manager.go`)
- Establishes SSH tunnels to remote nodes using `golang.org/x/crypto/ssh`
- Maintains persistent WebSocket connections through tunnels
- Automatic reconnection with exponential backoff
- Health check polling (default 30s interval)

#### 4.3.3 ProxyManager (`internal/node/proxy_manager.go`)
- Forwards App requests to appropriate agentd via node routing
- Injects `nodeId` into agentd events before broadcasting to App clients
- Aggregates events from multiple nodes into unified namespace

#### 4.3.4 Deployer (`internal/deployer/deployer.go`)
- One-click agentd deployment to remote nodes
- Version hash comparison to avoid redundant uploads
- Atomic binary replacement: upload `.new` → `pkill` → `mv` → restart
- Embedded agentd binary via `go:embed`

### 4.4 WebSocket API (JSON-RPC 2.0)

**Node management**:
| Method | Params | Description |
|--------|--------|-------------|
| `node.list` | — | List all nodes with status |
| `node.add` | `{host, port, sshKey, name}` | Add new node |
| `node.deploy` | `{nodeId}` | Deploy/update agentd on node |
| `node.remove` | `{nodeId}` | Remove node from config |

**Agent operations** (proxied to agentd with `nodeId`):
| Method | Params |
|--------|--------|
| `agent.list` | `{nodeId}` |
| `agent.create` | `{nodeId, provider, cwd, name}` |
| `agent.stop` | `{nodeId, agentId}` |
| `agent.restart` | `{nodeId, agentId}` |

**Conversation** (proxied to agentd):
| Method | Params |
|--------|--------|
| `conversation.send` | `{nodeId, agentId, message}` |
| `conversation.history` | `{nodeId, agentId, cursor}` |

### 4.5 HTTP Endpoints

| Endpoint | Auth | Description |
|----------|------|-------------|
| `GET /status` | Bearer token | Gateway status: nodes, connections, tunnel state |
| `GET /config/tunnel` | Bearer token | Get current tunnel configuration |
| `POST /config/tunnel` | Bearer token | Update tunnel URL/token |
| `GET /qr.png?type=local` | None | QR code for local WebSocket connection |
| `GET /qr.png?type=remote` | None | QR code for remote tunnel connection |
| `GET /apk` | Bearer token | Download latest agentapp APK |
| `GET /apk/version` | Bearer token | APK version metadata |
| `GET /v1/release/latest` | Bearer token | Proxy to release manifest |

### 4.6 Tunnel Hub Integration (REALITY)

- **tunnelhub**: Public server on port 443 with REALITY TLS
- **Protocol**: gRPC-Web over HTTP/2, disguised as Google traffic (SNI: `www.google.com`)
- **Authentication**: X25519 key pair, short ID
- **Auto-configuration**: `--hub` flag constructs tunnel URL automatically
- **QR code generation**: Supports both local (`ws://`) and remote (`wss://`) connection strings

---

## 5. Data Persistence

| Data | Storage | Location | Format |
|------|---------|----------|--------|
| Agent metadata | SQLite | `~/.agentd/data/agents.db` | SQL |
| Conversation events | SQLite | `~/.agentd/data/agents.db` | JSON blobs |
| Event buffer | Memory | agentd process | Circular buffer |
| Node config | JSON/YAML | `~/.agentgw/config.json` | JSON |
| Tunnel auth | JSON | `~/.agentgw/local_auth.json` | JSON |
| OAuth tokens | JSON | `~/.agentgw/oauth.json` | JSON |

---

## 6. Authentication & Security

| Layer | Mechanism |
|-------|-----------|
| App → agentgw | Static Bearer token (config.json) |
| agentgw → agentd | Static Bearer token (shared secret) |
| agentgw → SSH | Local SSH keys (`~/.ssh/id_rsa`) |
| App → tunnelhub | REALITY X25519 + short ID |
| Transport (local) | WebSocket (`ws://`) on LAN |
| Transport (remote) | gRPC-Web over REALITY TLS (`wss://`) |

---

## 7. Key Design Decisions

1. **agentd as static binary**: Zero dependencies, easy SCP deployment to remote machines
2. **EventBuffer circular buffer**: O(1) append via head/count indices, no array shifting
3. **PTY kill order**: Kill process first, then close ptmx fd (avoids SIGHUP zombies)
4. **Agent struct stores cmd/args**: Enables `agent.restart` to reuse original launch parameters
5. **NodeManager.LoadAll()**: Batch loads persisted nodes at startup to avoid N redundant file writes
6. **agentgw event forwarding**: Injects `nodeId` into agentd push events before broadcasting to App
7. **Session discovery pipeline**: PID → task fd → all JSONL candidates → time filter → content match → fallback to most recent
8. **Hot restart**: Inherits TCP listener FD via `AGENTGW_INHERIT_FD` environment variable

---

## 8. Testing Strategy

| Component | Test Type | Coverage |
|-----------|-----------|----------|
| agentd ws handler | Unit + Integration | 33/33 tests pass |
| agentd manager | Unit | ProcessManager, EventManager, StreamParser |
| agentd scanner | Unit | FS abstraction with real/mem adapters |
| agentgw node | Unit | Registry, TunnelManager, ProxyManager |
| agentgw deployer | Unit | TDD with mock SSH |
| Integration | End-to-end | Attach → load history → agent.list |

---

## 9. Acceptance Criteria

- [x] Agent lifecycle: create, start, stop, restart, attach
- [x] Multi-provider support: Claude Code, OpenCode
- [x] Real-time conversation streaming via WebSocket
- [x] Event persistence and replay on reconnection
- [x] Process auto-discovery and attachment
- [x] Gateway node management: add, remove, deploy
- [x] SSH tunnel with automatic reconnection
- [x] REALITY tunnel for anti-detection remote access
- [x] QR code generation for local and remote connections
- [x] Static token authentication
- [x] Hot restart without dropping connections
- [x] Cross-platform: Linux amd64, Darwin arm64

---

## 10. Related Documents

| Document | Purpose |
|----------|---------|
| `designs/system-overview.md` | Full system architecture with network topology |
| `designs/provider-state-machine.md` | Provider session state machine details |
| `designs/provider-shared-state.md` | Provider CC switching shared state design |
| `designs/anti-detection-connectivity.md` | REALITY tunnel and network architecture |
| `operations/deployment.md` | Deployment procedures |
| `operations/testing.md` | Testing strategy and procedures |
