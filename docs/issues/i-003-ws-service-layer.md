---
id: i-003
type: architecture
priority: critical
status: pending
owner: —
worktree: arch-002-ws-service
---

# WS Handler 抽取 Service 层

## Parent

架构重构批次2 — 来自 `docs/issues/README.md`

## What to build

在 `agentd/internal/ws/handler.go` (2,544 行, 37 handler + 20 package 函数) 和 Manager 之间建立 Service 层，让 handler 只负责 JSON-RPC 帧处理，所有业务逻辑下沉到 Service。

## Vertical slice

- **Schema**: `AgentService` 接口定义（Create, Stop, Restart, List, SendMessage, GetHistory 等）
- **API**: Handler 透调到 Service，Service 调用 Manager
- **UI**: 无（纯后端重构）
- **Tests**: Service 层独立单元测试（mock Manager）

## Acceptance criteria

- [ ] `AgentService` 接口定义
- [ ] `agent_service.go` 实现所有业务逻辑（当前分散在 handler 的 20 个 package 函数）
- [ ] `handler.go` 只保留：JSON-RPC 帧解析、dispatch table、auth、错误包装、广播
- [ ] Handler 的 package 函数全部消失，或移至 service
- [ ] Service 层单元测试覆盖所有方法（mock Manager）
- [ ] `./scripts/test.sh unit` 中 agentd ws 模块 PASS
- [ ] `cd agentd && go test ./internal/ws/` 开发阶段可用
- [ ] `./scripts/test.sh unit` 全模块无回归

## Blocked by

- [i-002 Manager 拆分为核心模块](./i-002-manager-split.md) — Service 层依赖 Manager 接口稳定

## Notes

- Handler 当前泄漏的业务逻辑：`resolveLaunch` (Claude `-p` flag, `--permission-mode`), `providerIDFromConfig`, `findExecutable`, `claudeDiscover`, `opencodeDiscover`
- 目标：handler 只做 "收到请求 → 解包参数 → 调用 Service → 打包响应 → 发送"，不做任何业务决策
