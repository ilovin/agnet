---
id: i-008
type: bug
priority: high
status: completed
labels: needs-triage
---

# Fix progress.md stale status for merged arch branches

## Parent

Dashboard 状态不一致修复 — 来自会话中发现的 progress.md 与实际 git 状态不同步问题。

## What to build

将 `docs/management/progress.md` 中的任务状态与实际 git 合并状态对齐。当前 ARCH-001、ARCH-004、ARCH-007 已实际合并到 main，但看板仍标记为 "Completed (未合并)"；ARCH-002/003 仍标记为 Blocked（依赖 ARCH-001），实际依赖已解除。

## Vertical slice

- **Schema**: 无（纯文档修复）
- **API**: 无
- **UI**: 更新 progress.md 任务看板状态
- **Tests**: 无

## Acceptance criteria

- [ ] ARCH-001 状态从 "Completed (未合并)" 改为 "已合并"，commit hash 记录为 `0d8494b`
- [ ] ARCH-004 状态从 "Completed (未合并)" 改为 "已合并"，commit hash 记录为 `b51757e`
- [ ] ARCH-007 状态从 "Completed (未合并)" 改为 "已合并"，commit hash 记录为 `09e92f4`
- [ ] ARCH-002 从 Blocked 改为 Ready（ARCH-001 依赖已解除）
- [ ] ARCH-003 从 Blocked 改为 Ready（ARCH-001 依赖已解除）
- [ ] In Progress 列移除 ARCH-001、ARCH-004、ARCH-007、TEST-007
- [ ] 整体健康状态更新为反映当前实际进展

## Blocked by

None — can start immediately.
