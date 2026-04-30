# 文档导航

本文档中心提供 Agent Manager (phone-talk) 项目所有文档的索引与导航。

## 快速定位（按任务类型）

| 我要... | 去读... |
|---|---|
| 了解当前在做什么 | `management/progress.md` |
| 查看某个需求的详情 | `requirements/r-XXX-*.md`（活跃）或 `archive/YYYY-MM/r-XXX-*.md`（已完成） |
| 了解系统架构 | `designs/system-overview.md` |
| 查看组件设计 | `designs/provider-state-machine.md`、`designs/provider-shared-state.md` |
| 了解反检测/远程连接 | `designs/anti-detection-connectivity.md` |
| 执行部署 | `operations/deployment.md` |
| 查看发布计划 | `operations/production-launch.md` |
| 查看测试策略 | `operations/testing.md` |
| 查看开发/交付工作流（完整版） | `operations/development-workflow.md` |
| 开始开发一个功能 | 先读 `management/progress.md` 找任务，再读对应 `designs/` 和 `plans/` |

## 目录职责

- `requirements/`：活跃需求库，每个需求独立文件，命名 `r-XXX-*.md`
- `designs/`：架构与设计文档，持续演进，不归档
- `plans/`：实施计划，完成后归档
- `operations/`：部署、运维、测试文档
- `management/`：项目进度看板（`progress.md`）
- `archive/`：已完成/废弃文档，严格只读，按 `YYYY-MM/` 存放

## 文档生命周期

- **需求完成**：从 `requirements/` 移入 `archive/YYYY-MM/`
- **计划完成**：从 `plans/` 移入 `archive/YYYY-MM/`
- **设计文档**：持续更新，覆盖原文件，不归档

## 与 CLAUDE.md 的关系

- `CLAUDE.md`：项目宪法，包含不可违反的强制规则（TDD、Manager 模式、构建命令等）
- `docs/`：信息文档，提供背景知识、设计细节、操作指南
