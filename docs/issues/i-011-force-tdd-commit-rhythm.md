---
id: i-011
type: process
priority: high
status: completed
labels: needs-triage
---

# Enforce TDD commit rhythm for architecture refactors

## Parent

Dashboard 状态不一致修复 — 来自 ARCH-001 合并过程中发现的 TDD 流程违规问题。

## What to build

在 `docs/operations/development-workflow.md` 的 Development Workflow MUST 中明确要求：所有非平凡修改（尤其是架构重构）必须遵循 TDD 提交节奏，即 **测试 commit（红）→ 实现 commit（绿）→ 清理/重构 commit**，禁止单一大 commit 混合测试与实现。

CLAUDE.md 作为项目宪法只保留原则性声明（"Follow TDD"），具体操作节奏写入 workflow 文档。

## Vertical slice

- **Schema**: 更新 `docs/operations/development-workflow.md` Development Workflow MUST
- **API**: 无
- **UI**: 文档更新
- **Tests**: 无

## Acceptance criteria

- [ ] `docs/operations/development-workflow.md` 的 Development Workflow MUST 中，TDD 条目扩展为包含提交节奏要求
- [ ] 明确禁止"单一大 commit 混合测试与实现"的做法
- [ ] 节奏要求适用于后续所有架构重构（ARCH-002/003/005/006 等）
- [ ] CLAUDE.md 的 TDD 引用保持简洁，指向 workflow 文档的具体要求

## Blocked by

- [i-008 Fix progress.md stale status for merged arch branches](i-008-progress-stale-status.md)
