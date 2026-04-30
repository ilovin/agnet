---
id: i-002
type: architecture
priority: critical
status: in-progress
owner: dev agent
worktree: arch-001-manager-split
---

# Manager 拆分为核心模块

## Parent

架构重构批次1 — 来自 `docs/issues/README.md`

## What to build

将 `agentd/internal/agent/manager.go` (2,348 行, 44 方法) 这个 god object 拆分为 4 个专注模块，每个模块有清晰接口和独立测试。

## Vertical slice

- **Schema**: 4 个新模块的接口定义
- **API**: Manager 保留稳定公共 API，内部委托给子模块
- **UI**: 无（纯后端重构）
- **Tests**: 每个新模块独立单元测试

## Module split

1. **ProcessManager** — agent Create/Restart/Stop/Remove, PTY lifecycle, cmd/args tracking
2. **EventManager** — appendAndPersistEvent, RecordConversationEvent, event query (LastPersistedSeq, LoadPersistedEvents*)
3. **StreamParser** — readStreamJSONOutput, tryParseStreamJSON, handleStreamJSONEvent
4. **PermissionResolver** — readPTYForPermissionPrompts, ANSI parsing, permission detection, auto-resolution

## Acceptance criteria

- [ ] 4 个新文件创建：`process_manager.go`, `event_manager.go`, `stream_parser.go`, `permission_resolver.go`
- [ ] 每个模块有独立单元测试文件，覆盖率 > 60%
- [ ] Manager 公共接口不变：`Attach`, `Create`, `Restart`, `Stop`, `Remove`, `AgentList`, `ConversationHistory` 等签名不变
- [ ] `./scripts/test.sh unit` 中 agentd 模块 PASS
- [ ] `cd agentd && go test ./internal/agent/` 开发阶段可用
- [ ] `./scripts/test.sh unit` 全模块无回归
- [ ] `./scripts/build.sh agentd` 编译成功

## Blocked by

None — can start immediately.

## Notes

- Manager 当前混合：进程生命周期 + 事件持久化 + stream 解析 + 权限检测 + provider 会话发现 + 状态推导 + WS 广播
- 提取顺序建议：EventManager → ProcessManager → StreamParser → PermissionResolver
- 每提取一个模块就运行测试， incremental 推进
