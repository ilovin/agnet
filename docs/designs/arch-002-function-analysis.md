# ARCH-002 WS Service层 — 函数提取分析

## 分析时间
2026-05-08

## 提取候选函数

### 第一批：纯函数/无状态（立即提取）

| 函数 | 当前位置 | 签名 | 依赖 |
|------|----------|------|------|
| findExecutable | handler.go:292 | `func findExecutable(name string) string` | 无 |
| resolveLaunch | handler.go:340 | `func resolveLaunch(provider, cmd string, args []string, sessionID, model, permissionMode string) (string, string, []string, []string)` | findExecutable |
| currentPermissionMode | handler.go:317 | `func currentPermissionMode(args []string) string` | 无 |
| currentOpenCodeModel | handler.go:330 | `func currentOpenCodeModel(args []string) string` | 无 |
| findClaudeSettings | handler.go:1515 | `func findClaudeSettings() string` | 无 |
| providerIDFromConfig | handler.go:1533 | `func providerIDFromConfig(configJSON string, runtimeEnv map[string]any, runtimeModel string) string` | 无 |
| runtimeProviderFromRows | handler.go:1569 | `func runtimeProviderFromRows(rows []map[string]any) (string, string)` | findClaudeSettings, providerIDFromConfig |
| currentProviderFromRows | handler.go:1607 | `func currentProviderFromRows(rows []map[string]any) (string, string)` | 无 |

### 第二批：需要文件系统扫描（可提取）

| 函数 | 当前位置 | 签名 | 依赖 |
|------|----------|------|------|
| opencodeDiscover | handler.go:1415 | `func (h *handler) opencodeDiscover(req RPCRequest) RPCResponse` | 无（纯文件系统扫描）|
| claudeDiscover | handler.go:1508 | `func (h *handler) claudeDiscover(req RPCRequest) RPCResponse` | 无（纯文件系统扫描）|

### 第三批：保留在 Handler（紧密耦合）

| 函数 | 原因 |
|------|------|
| loop, dispatch | WebSocket 连接管理核心 |
| agentList, agentCreate, agentStop 等 | 直接依赖 h.server.manager |
| conversationSend | 多路径路由，依赖 Manager + Agent 状态 |
| providerSwitch | 涉及 SQLite DB + 并发锁 |
| deriveProviderSnapshot | 依赖 handler 缓存机制 |
| statusChangedParams | 依赖 h.server.manager 获取 agent 状态 |

## 实施建议

### 接口设计

```go
type AgentService interface {
    // Launch utilities
    FindExecutable(name string) string
    ResolveLaunch(provider, cmd string, args []string, sessionID, model, permissionMode string) (resolvedProvider, resolvedCmd string, resolvedArgs, env []string)
    CurrentPermissionMode(args []string) string
    CurrentOpenCodeModel(args []string) string
    
    // Provider configuration
    FindClaudeSettings() string
    ProviderIDFromConfig(configJSON string, runtimeEnv map[string]any, runtimeModel string) string
    RuntimeProviderFromRows(rows []map[string]any) (string, string)
    CurrentProviderFromRows(rows []map[string]any) (string, string)
}
```

### 工作量
- 第一批：约 200 行代码迁移
- 第二批：约 100 行代码迁移（需要将 handler 方法改为纯函数）
- 测试：约 150 行新测试代码

### 风险
- conversationSend 提取风险高（多路径路由）
- provider 相关函数依赖 handler 的缓存机制，需要重构缓存位置
