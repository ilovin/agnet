---
id: i-006
type: architecture
priority: high
status: in-progress
owner: dev agent
worktree: arch-005-flutter-screens
---

# Flutter Service 提取

## Parent

架构重构批次1 — 来自 `docs/issues/README.md`

## What to build

将 `dashboard_screen.dart` (3,384 行) 和 `agent_detail_screen.dart` (5,567 行) 中的业务逻辑提取到 Service 层，屏幕只保留 UI 渲染和事件委托。

## Vertical slice

- **Schema**: `DashboardService` 和 `AgentDetailService` 类定义
- **API**: Service 方法对应原屏幕中的 RPC 调用和业务逻辑
- **UI**: 屏幕变瘦，只负责 widget 树构建和手势响应
- **Tests**: Service 单元测试（mock WS client）

## Service 职责

### DashboardService
- `refreshAllNodes()`
- `discoverNodes()`
- `restartGateway()`
- `loadAgentPreview()`
- `fetchNodeHealth()`

### AgentDetailService
- `sendMessage(agentId, text)`
- `switchProvider(agentId, providerId)`
- `switchModel(agentId, modelId)`
- `resolvePermissionPrompt(agentId, decision)`
- `pollNewEvents(agentId)`
- `fetchHistory(agentId, cursor)`

## Acceptance criteria

- [ ] `lib/services/dashboard_service.dart` 创建
- [ ] `lib/services/agent_detail_service.dart` 创建
- [ ] 两个 Service 都是纯 Dart 类（不继承 Widget），通过 constructor 注入 WS client
- [ ] 屏幕代码行数减少 > 30%（目标: dashboard < 2,400 行, detail < 3,900 行）
- [ ] Service 单元测试覆盖所有公共方法（mock client）
- [ ] `./scripts/test.sh flutter` 全部 PASS（114 tests）
- [ ] `cd agentapp && flutter test` 开发阶段可用
- [ ] `./scripts/test.sh flutter` 无新增失败
- [ ] `cd agentapp && flutter analyze` 无新增错误
- [ ] UI 行为无变化（纯重构）

## Blocked by

None — can start immediately.

## Notes

- 使用现有 Provider 模式注入 Service
- Service 不应持有 BuildContext — 所有结果通过 callback 或 Future 返回
- 提取顺序：先 AgentDetailService（业务逻辑更集中），再 DashboardService
