# ARCH-002 WS Handler Service层 — 实施设计

## 问题

`agentd/internal/ws/handler.go` 当前约 2,596 行，混合了：
- JSON-RPC 帧处理与 dispatch（应保留在 handler）
- 业务逻辑：启动解析、provider 发现、消息路由（应提取到 Service）
- 文件系统操作和配置解析（应提取到 Service）

## 目标

在 handler 和 Manager 之间创建 `AgentService` 层：
- **Handler**：JSON-RPC 帧解析、dispatch table、auth、错误包装、事件广播
- **AgentService**：所有业务逻辑和文件系统操作

## 提取范围

### 第一批：纯函数/无状态方法（可立即提取）

| 方法 | 当前位置 | 说明 | 依赖 |
|---|---|---|---|
| `findExecutable` | handler.go:292 | 搜索 PATH 和 /home/*/.local/bin | 无 |
| `resolveLaunch` | handler.go:340 | 解析 provider 启动命令和参数 | findExecutable |
| `currentPermissionMode` | handler.go:317 | 从 args 提取 --permission-mode | 无 |
| `currentOpenCodeModel` | handler.go:330 | 从 args 提取 -m model | 无 |
| `findClaudeSettings` | handler.go:1515 | 查找 ~/.claude/settings.json | 无 |
| `providerIDFromConfig` | handler.go:1533 | 从配置 JSON 匹配 provider | 无 |

### 第二批：需要 Manager/Server 交互的方法（后续提取）

| 方法 | 当前位置 | 说明 | 依赖 |
|---|---|---|---|
| `opencodeDiscover` | handler.go:1415 | 扫描文件系统发现 opencode sessions | 无（纯文件系统扫描）|
| `claudeDiscover` | handler.go:1508 | 扫描文件系统发现 claude sessions | 无（纯文件系统扫描）|
| `conversationSend` | handler.go:757 | 消息发送路由（tmux/pipe/fresh/pty）| Manager, Agent |

### 不提取（保留在 handler）

- `loop()` - WebSocket 连接管理
- `dispatch table` - JSON-RPC 方法路由
- `auth` - 认证逻辑
- `broadcast` - 事件广播
- `providerSwitch` - provider 切换（涉及 DB 和并发锁）
- `deriveProviderSnapshot` - provider 状态派生（紧密耦合 handler 缓存）

## 接口设计

```go
// agentd/internal/ws/agent_service.go

package ws

// AgentService contains business logic extracted from the WebSocket handler.
type AgentService interface {
    // Launch resolution
    FindExecutable(name string) string
    ResolveLaunch(provider, cmd string, args []string, sessionID, model, permissionMode string) (resolvedProvider, resolvedCmd string, resolvedArgs, env []string)
    CurrentPermissionMode(args []string) string
    CurrentOpenCodeModel(args []string) string
    
    // Provider discovery
    FindClaudeSettings() string
    ProviderIDFromConfig(configJSON string, runtimeEnv map[string]any, runtimeModel string) string
    
    // Session discovery (can be added in follow-up)
    // OpenCodeDiscover() ([]sessionInfo, error)
    // ClaudeDiscover() ([]sessionInfo, error)
}

type agentService struct {
    // 无状态，纯函数集合
}

func NewAgentService() AgentService {
    return &agentService{}
}
```

## 重构步骤

### Step 1: 创建 Service 文件

创建 `agentd/internal/ws/agent_service.go`，将第一批纯函数移入：
- `findExecutable` → `FindExecutable`
- `resolveLaunch` → `ResolveLaunch`
- `currentPermissionMode` → `CurrentPermissionMode`
- `currentOpenCodeModel` → `CurrentOpenCodeModel`
- `findClaudeSettings` → `FindClaudeSettings`
- `providerIDFromConfig` → `ProviderIDFromConfig`

### Step 2: 修改 handler 构造函数

```go
type handler struct {
    server  *Server
    conn    *websocket.Conn
    self    *client
    service AgentService  // 新增
}
```

在创建 handler 时注入 service：
```go
func (s *Server) newHandler(conn *websocket.Conn) *handler {
    return &handler{
        server:  s,
        conn:    conn,
        service: NewAgentService(),
    }
}
```

### Step 3: 替换 handler 中的调用

将所有 `findExecutable(...)` 调用替换为 `h.service.FindExecutable(...)`  
将所有 `resolveLaunch(...)` 调用替换为 `h.service.ResolveLaunch(...)`  
以此类推。

### Step 4: 测试

1. 编译：`go build ./internal/ws/...`
2. 运行 `go test ./internal/ws/...`
3. 运行 `./scripts/test.sh unit`

## 工作量估算

- 创建 agent_service.go：~150 行（移动已有代码）
- 修改 handler.go：~20 处调用点替换
- 新增测试：agent_service_test.go（测试纯函数边界情况）
- 总计：约 200 行代码变更

## 风险

- **conversationSend 提取风险高**：涉及多种发送路径（tmux/pipe/fresh/pty/opencode），且与 Manager 和 Agent 状态紧密耦合。建议先提取纯函数，conversationSend 保留在 handler 中或作为第二批任务。
- **Provider 切换逻辑复杂**：`providerSwitch` 涉及 SQLite DB 操作和并发锁，暂不提取。

## 依赖

- ARCH-001 Manager 拆分已完成（manager.go 接口稳定）
- 与 ARCH-003 Watcher seam 无冲突（修改不同文件）
