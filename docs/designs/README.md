# 设计文档

架构与设计决策的持续演进文档。

## 文档列表

| 文件 | 主题 |
|---|---|
| `system-overview.md` | Agent Manager 系统整体架构 |
| `provider-state-machine.md` | Provider 会话状态机设计 |
| `provider-shared-state.md` | Provider CC 切换共享状态设计 |
| `anti-detection-connectivity.md` | 反检测与远程连接架构（合并文档） |

## 维护规则

- 设计文档**不归档**，随系统演进持续更新
- 同一主题多次迭代时**覆盖原文件**，历史版本通过 git 追溯
- 新增设计文档直接在此目录下创建
