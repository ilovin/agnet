---
id: i-010
type: tooling
priority: low
status: open
labels: needs-triage
---

# Add progress-git consistency validator script

## Parent

Dashboard 状态不一致修复 — 来自会话中发现的 progress.md 与实际 git 状态不同步问题。

## What to build

创建一个可运行的校验脚本，自动对比 `docs/management/progress.md` 中记录的分支/任务状态与 git 仓库的实际状态（哪些分支已合并到 main），输出不一致项的 diff。脚本可被 CI 或手动运行，作为预防看板漂移的防线。

## Vertical slice

- **Schema**: 脚本输入为 progress.md，输出为不一致报告
- **API**: `git branch --merged main` 或 `git log --grep` 扫描
- **UI**: 命令行输出，带颜色区分 PASS/WARN/FAIL
- **Tests**: 脚本自身至少包含 1 个自测用例（mock progress.md + 已知 git 状态）

## Acceptance criteria

- [ ] 脚本路径为 `scripts/validate-progress.sh`
- [ ] 脚本解析 progress.md 中的 "Completed (未合并)" / "已合并" / "In Progress" 状态
- [ ] 脚本通过 `git branch --merged main` 验证分支是否真实合并
- [ ] 发现不一致时输出具体差异（如：progress.md 说 ARCH-001 未合并，但 git 显示已合并）
- [ ] 返回 exit code 0（一致）或 1（不一致），可被 CI 使用
- [ ] 脚本包含 `--help` 和简短使用说明

## Blocked by

- [i-009 Add post-merge checklist to prevent dashboard drift](i-009-post-merge-checklist.md)
