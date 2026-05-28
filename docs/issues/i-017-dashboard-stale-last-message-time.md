---
name: i-017 Dashboard stale lastMessageTime
description: loadAgents merge logic preserves stale lastMessageTime from WS, ignoring newer RPC values; status change events don't include lastMessageTime updates
type: issue
---

# i-017 Dashboard 显示 stale lastMessageTime

## 问题描述

Dashboard 中 agent 卡片显示 "59分钟前"，但实际会话内容是新的。

## 根因分析

### 1. loadAgents 合并逻辑错误保留旧值

`agentapp/lib/providers/nodes_provider.dart:98`:
```dart
lastMessageTime: prev.lastMessageTime,
```

`lastMessageTime`被归类为"WS更新的动态字段"而保留，但：
- WS `agent.status_changed` 事件只在 **status 改变时** 触发（`agent.go:102`：`if oldStatus != s`）
- 如果 status 不变（如一直是 `working`），`lastMessageTime` 永远不会通过 WS 更新
- RPC `agent.list` 返回了正确的最新 `lastMessageTime`，但 `loadAgents` 忽略了这个值

### 2. R-013 merge-mode 的副作用

R-013 为了防止 RPC 覆盖 WS 实时事件，在 `loadAgents` 中保留了动态字段。但 `lastMessageTime` 不是真正的"动态字段"——它应该随时间推移而更新，无论 status 是否改变。

## 修复方案 (第一批)

修改 `loadAgents` 中的 `lastMessageTime` 合并逻辑：使用 RPC 返回的值，如果它比当前值更新：

```dart
lastMessageTime: (rpcAgent.lastMessageTime != null &&
                  (prev.lastMessageTime == null || rpcAgent.lastMessageTime! > prev.lastMessageTime!))
    ? rpcAgent.lastMessageTime
    : prev.lastMessageTime,
```

## 根因分析 (第二批 — i-017-followup)

### 3. `setStatus` 在消息持久化之前触发

`agentd/internal/agent/manager.go` 的 `handleStreamJSONEvent` 中：
- `user`/`assistant` 消息先调用 `ag.setStatus()`，再执行 `appendAndPersistEvent()`
- `setStatus()` 触发的 `onChange` goroutine 是异步的
- `statusChangedParams` 计算 `lastMessageTime` 时调用 `LastConversationEventTime`（读数据库）
- 由于 goroutine 可能在 `appendAndPersistEvent` 之后执行，它读到的 **可能已经是新时间**
- 但如果 goroutine 在 `loadAgents` 之后执行，它会广播 `agent.status_changed` 事件
- Flutter 端的 `handleEvent` 直接接受 WS 事件的 `lastMessageTime`，会覆盖 `loadAgents` 设置的新值

### 4. 结果
- `loadAgents` 每 10 秒调用一次，返回正确的 `lastMessageTime`
- 但延迟的 `agent.status_changed` goroutine 可能在 `loadAgents` 之后广播旧值
- dashboard 上显示的时间被旧值覆盖，呈现 "1h" 等 stale 时间

## 修复方案 (第二批 — i-017-followup)

### 后端
将 `setStatus` 推迟到 `appendAndPersistEvent` 之后执行，确保 `agent.status_changed` 触发时数据库中已有新事件：

```go
if data != nil {
    seq := m.appendAndPersistEvent(agentID, ag, data)
    ...
    switch ev.Type {
    case "user":
        ag.setStatus(StatusIdle)
    case "assistant":
        ag.setStatus(StatusWorking)
    }
}
```

### 前端
`handleEvent` 中增加防御性判断：只接受比现有值更新的 `lastMessageTime`：

```dart
lastMessageTime: (() {
  final wsTime = params.containsKey('lastMessageTime')
      ? (params['lastMessageTime'] as num?)?.toInt()
      : null;
  if (wsTime == null) return current.lastMessageTime;
  if (current.lastMessageTime == null || wsTime > current.lastMessageTime!) {
    return wsTime;
  }
  return current.lastMessageTime;
})(),
```

## 关联
- R-013: merge-mode for loadNodes/loadAgents/mergeHistory
- commit: `03c4aef`
