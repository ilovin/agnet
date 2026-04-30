# 架构重构 PRD

## Problem Statement

Agent Manager (phone-talk) 项目经过快速迭代后，核心模块出现严重的架构债务：

1. **agentd Manager 是 God Object** (2,348 行, 44 方法) — 混合进程生命周期、事件持久化、stream-JSON 解析、PTY 权限检测、provider 会话发现等 7 种完全不同的职责。
2. **WS Handler 是浅层 catch-all** (2,544 行) — JSON-RPC 帧处理、认证、业务逻辑、事件广播、provider 切换全混在一起，没有 Service 层。
3. **SessionWatcher 是假 seam** — 接口只有 3 个方法，但 Manager 立即 downcast 调用类型特定方法。
4. **Scanner 不可测试** — 紧耦合 `/proc`、`lsof`、`tmux`、Claude 文件系统布局，无法在没有真实 Claude 安装的情况下测试。
5. **Flutter Screens 深度错位** — dashboard (3,384 行) 和 agent detail (5,567 行) 混合数据获取、状态管理、业务逻辑、UI 渲染。
6. **JSON-RPC 手动构造** — 57 处 `map[string]any`，22/26 处 `client.call('method', {...})`，无类型安全。
7. **NodeManager 混合职责** — SSH 隧道 + WS 代理 + 节点元数据。

这些债务导致：
- 改一处动全身，regression 风险高
- 单元测试难以编写（依赖真实环境）
- 新开发者 onboarding 成本高
- AI agent 也难以准确导航和修改代码

## Solution

通过 7 个垂直切片（tracer bullet）将浅层模块 deepen，核心原则：

1. **单一职责**：每个模块只做一件事
2. **接口先行**：先定义接口，再实现，最后替换
3. **TDD + 脚本验收**：每个切片先写测试（red），再实现（green），最终用 `./scripts/test.sh` 验收
4. **向后兼容**：公共 API 不变，内部重构

## User Stories

1. 作为后端开发者，我希望 Manager 只负责协调而不包含业务逻辑，这样改事件持久化不会影响到进程管理。
2. 作为后端开发者，我希望 Scanner 可以在纯内存环境下测试，这样 CI 无需安装 Claude 也能跑通测试。
3. 作为后端开发者，我希望 SessionWatcher 接口覆盖所有调用点，这样新增 provider 时无需修改 Manager。
4. 作为后端开发者，我希望 WS Handler 只负责 JSON-RPC 帧处理，这样改业务逻辑不会影响到 WebSocket 连接管理。
5. 作为前端开发者，我希望 Dashboard 和 Agent Detail 屏幕只负责 UI 渲染，这样改业务逻辑不会导致 widget 重绘错误。
6. 作为前端开发者，我希望 RPC 调用是类型安全的，这样编译器能在构建时发现参数错误而不是在运行时崩溃。
7. 作为网关开发者，我希望 NodeManager 的隧道、代理、元数据职责分离，这样改 SSH 重连逻辑不会影响到 WS 事件转发。
8. 作为代码审查者，我希望每个重构切片都有独立测试覆盖，这样我可以安全地批准 PR 而不需要手动验证所有路径。
9. 作为测试维护者，我希望所有测试最终通过 `./scripts/test.sh` 运行，这样本地验证和 CI 使用同一套入口。
10. 作为新加入的开发者，我希望 5 分钟内理解模块边界，这样我可以快速定位需要修改的文件。

## Implementation Decisions

### 1. Manager 拆分 (i-002)

- **ProcessManager**: Create/Restart/Stop/Remove, PTY lifecycle, cmd/args tracking
- **EventManager**: appendAndPersistEvent, RecordConversationEvent, event query (LastPersistedSeq, LoadPersistedEvents*)
- **StreamParser**: readStreamJSONOutput, tryParseStreamJSON, handleStreamJSONEvent
- **PermissionResolver**: readPTYForPermissionPrompts, ANSI parsing, permission detection

**接口稳定性**: Manager 公共接口不变（Attach, Create, Restart, Stop, Remove, AgentList, ConversationHistory 等签名不变）。

**提取顺序**: EventManager → ProcessManager → StreamParser → PermissionResolver。每提取一个就运行 `./scripts/test.sh unit`。

### 2. WS Service 层 (i-003)

- 在 handler 和 Manager 之间创建 `AgentService` 接口
- Service 包含所有业务逻辑（resolveLaunch, findExecutable, providerIDFromConfig, claudeDiscover, opencodeDiscover 等）
- Handler 只保留：JSON-RPC 帧解析、dispatch table、auth、错误包装、广播

**阻塞**: 依赖 i-002 Manager 拆分完成（Service 需要稳定的 Manager 接口）。

### 3. SessionWatcher 真 seam (i-004)

- 扩展 `SessionWatcher` 接口，加入 `SetWorkDir`, `SetPID`, `SetTmuxTarget`, `OnSessionSwitch`
- OpenCodeDBWatcher 对这些新方法实现为 no-op
- 消除 Manager 中所有 concrete type 构造和 downcast

**阻塞**: 依赖 i-002 Manager 拆分完成。

### 4. Scanner 可测试化 (i-001)

- 定义 `FileSystem` 接口：`ReadFile`, `ReadDir`, `Readlink`, `Stat`, `Exec`
- `RealFileSystem` 适配器（默认，用 `os` 包）
- `MemFileSystem` 适配器（测试用，内存文件系统）
- `Scan()` 保持签名不变，新增 `ScanWithFS(fs FileSystem)` 供测试

### 5. NodeManager 拆分 (i-005)

- **NodeRegistry**: 节点元数据（Add, Remove, Rename, List, Get, persist/load）
- **TunnelManager**: SSH 连接生命周期（Connect, Disconnect, health check, port forwarding）
- **ProxyManager**: WebSocket 代理生命周期（SetProxy, GetProxy, handle disconnect, event forwarding）

### 6. Flutter Service 提取 (i-006)

- **DashboardService**: refreshAllNodes, discoverNodes, restartGateway, loadAgentPreview, fetchNodeHealth
- **AgentDetailService**: sendMessage, switchProvider, switchModel, resolvePermissionPrompt, pollNewEvents, fetchHistory
- Service 是纯 Dart 类，通过 constructor 注入 WS client，不持有 BuildContext

**目标**: dashboard_screen < 2,400 行, agent_detail_screen < 3,900 行。

### 7. JSON-RPC 类型安全 (i-007)

- Go: 为每个 RPC 方法定义 Request/Response struct
- Dart: 为每个 RPC 定义 request/response 类
- 两阶段实现：v1 手写 structs → v2 代码生成

**阻塞**: 建议等 i-003 WS Service 层完成后再做（Service 层直接用 typed struct）。

## Testing Decisions

### 什么是好测试

- **只测外部行为，不测实现细节**：测试模块的公共接口，不测试私有函数
- **快速**：单元测试应在 1 秒内完成
- **确定性**：不依赖外部系统（网络、文件系统、真实进程）
- **可维护**：测试代码应比生产代码更简单

### 测试模块

| 切片 | 测试文件 | 测试类型 | 验收命令 |
|---|---|---|---|
| i-001 Scanner | `scanner_test.go` | 单元测试 (MemFileSystem) | `./scripts/test.sh unit` |
| i-002 Manager | `*_test.go` (每个子模块) | 单元测试 | `./scripts/test.sh unit` |
| i-003 WS Service | `agent_service_test.go` | 单元测试 (mock Manager) | `./scripts/test.sh unit` |
| i-004 Watcher | `watcher_test.go` | 单元测试 (多 provider) | `./scripts/test.sh unit` |
| i-005 NodeManager | `*_test.go` (每个子模块) | 单元测试 (mock SSH/WS) | `./scripts/test.sh unit` |
| i-006 Flutter | `*_test.dart` (Service) | 单元测试 (mock client) | `./scripts/test.sh flutter` |
| i-007 JSON-RPC | 编译时类型检查 | 编译测试 | `./scripts/build.sh go` |

### 现有参考

- `agentd/internal/agent/manager_test.go`: Manager 现有单元测试模式
- `agentd/internal/ws/handler_integration_test.go`: 集成测试模式（新建）
- `agentapp/test/models_test.dart`: Flutter 测试模式
- `agentd/internal/scanner/scanner_test.go`: Scanner 现有测试（待扩展）

## Out of Scope

1. **功能变更**：纯重构，不添加新功能，不修改 UI 外观
2. **数据库 schema 变更**：SQLite 表结构不变
3. **API 协议变更**：JSON-RPC 2.0 协议不变，只加类型封装
4. **部署流程变更**：scripts/build.sh、scripts/deploy.sh 不变
5. **性能优化**：当前轮次不追求性能提升，先保证正确性
6. **并发模型重构**：goroutine 生命周期不变
7. **OpenCode/Codex/Gemini CLI 新增 provider**：当前只重构现有代码

## Further Notes

### 批次安排

- **批次 1** (并行): i-001 Scanner, i-002 Manager, i-005 NodeManager, i-006 Flutter
- **批次 2** (依赖批次 1): i-003 WS Service, i-004 Watcher
- **批次 3** (依赖批次 2): i-007 JSON-RPC

### 工作流

每个切片的开发流程：
1. **Review** → 独立 review agent 评审现有代码，输出 review 报告
2. **TDD** → dev agent 先写测试（red），再实现（green）
3. **脚本验收** → `./scripts/test.sh unit` / `flutter` 全部 PASS
4. **Code Review** → review agent 验证重构质量
5. **合并** → 合并到 main，更新 issue 状态

### 风险

- **回归风险**：Manager 是核心模块，拆分过程中容易破坏 agent 生命周期
- **测试不足**：当前 Scanner 几乎没有测试，MemFileSystem 可能无法覆盖所有边界
- **时间成本**：7 个切片全完成预计需要多个 sprint
- **并发冲突**：4 个并行 worktree 同时修改可能产生 merge conflict

### Worktree 分配

| 切片 | Worktree | 分支 |
|---|---|---|
| i-001 | `.worktrees/arch-004-scanner-fs` | `arch-004-scanner-fs` |
| i-002 | `.worktrees/arch-001-manager-split` | `arch-001-manager-split` |
| i-003 | `.worktrees/arch-002-ws-service` | `arch-002-ws-service` |
| i-004 | `.worktrees/arch-003-watcher-seam` | `arch-003-watcher-seam` |
| i-005 | `.worktrees/arch-007-node-manager` | `arch-007-node-manager` |
| i-006 | `.worktrees/arch-005-flutter-screens` | `arch-005-flutter-screens` |
| i-007 | `.worktrees/arch-006-jsonrpc-types` | `arch-006-jsonrpc-types` |
