# R-013 Dashboard 与会话内容显示不是最新的

## 问题描述
用户反馈 Dashboard 上显示的节点/agent 状态，以及会话中的消息内容，都不是最新的。存在明显的数据延迟或旧数据覆盖新数据的现象。

## 根因分析（初步）

Dashboard 采用**双轨更新机制**：
1. **事件驱动**：WebSocket push 事件 (`node.status_changed`, `agent.status_changed`, `conversation.message` 等) 实时更新前端状态
2. **定时轮询**：每 10 秒的 `_refreshTimer` 调用 `_refreshAllNodes()`，通过 RPC 重新拉取全量数据

当前 `nodesProvider.loadNodes()` 和 `loadAgents()` 是**直接替换**（replace）模式，而非**智能合并**（merge）。这导致：

- 事件 A 更新了某 agent 状态 → 定时轮询 B 到达 → RPC 返回的旧数据覆盖事件 A 的新状态
- `_prefetchVisibleAgentPreviews()` 调用 `conversation.history` + `mergeHistory()`，`mergeHistory` 按 `seq` 覆盖，若 history 返回的数据快照早于 `conversation.message_update` 事件，则旧 text 覆盖新 text

## 验收标准

1. 事件驱动更新的数据不会被定时轮询的旧数据覆盖
2. Dashboard 节点/agent 状态与后端实际状态保持一致（无明显延迟）
3. 会话消息内容在 streaming、message_update 后保持最新，不会被 history 刷新回退
4. 相关单元测试覆盖竞态条件场景
5. 在现有 Chrome 标签页中验证修复效果
