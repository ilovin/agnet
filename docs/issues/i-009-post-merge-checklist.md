---
id: i-009
type: process
priority: medium
status: completed
labels: needs-triage
---

# Add post-merge checklist to prevent dashboard drift

## Parent

Dashboard 状态不一致修复 — 来自会话中发现的 progress.md 与实际 git 状态不同步问题。

## What to build

在 `docs/operations/development-workflow.md` 的 Acceptance Criteria (Definition of Done) 中增加一个强制步骤：当分支合并到 main 后，必须立即同步更新 `docs/management/progress.md` 和任务列表状态。防止未来再次出现 "看板内容和实际会话对不上" 的问题。

CLAUDE.md 作为项目宪法只保留原则性引用，具体操作细节写入 workflow 文档。

## Vertical slice

- **Schema**: 更新 `docs/operations/development-workflow.md` Acceptance Criteria
- **API**: 无
- **UI**: 文档更新
- **Tests**: 无

## Acceptance criteria

- [ ] `docs/operations/development-workflow.md` 的 Definition of Done 新增第 7 条："After merge to main: task status in `progress.md` and the task list are updated immediately"
- [ ] CLAUDE.md 的 Manager Self-Check 保留原则性引用，指向 workflow 文档的具体 checklist
- [ ] 更新后的 checklist 被 Manager 角色在每次交付前执行

## Blocked by

- [i-008 Fix progress.md stale status for merged arch branches](i-008-progress-stale-status.md)
