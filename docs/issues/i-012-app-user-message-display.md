---
id: i-012
type: bug
priority: high
status: open
labels: needs-triage
---

# APP端无法显示用户新输入的信息

## 问题描述

在 agentapp（Flutter Web）中，用户发送的新输入消息无法在聊天界面显示。当前只能看到：
- Agent（assistant）回复的消息
- 特殊字符/工具调用信息（如 `[Bash: cmd]`、`[Agent]`、`[SendMessage]` 等）

用户自己输入的消息完全不可见。

## 初步分析

从 `agentapp/lib/screens/agent_detail_screen.dart` 的 `convertEventsToMessages` 函数来看：

1. **用户消息处理逻辑存在**（第680-704行）：当 `role == 'user'` 时，会调用 `flushMergeBuf()`、`flushActivityBuf()`、`flushThinkingBuf()` 等清理操作，然后通过 `isNoiseOnlyText(cleaned)` 过滤后添加到消息列表。

2. **噪音过滤**（`isNoiseOnlyText`，第392行）：过滤空字符串、"Terminal"、长度≤2的文本、spinner动画字符等。用户消息如果命中此过滤条件会被丢弃。

3. **可能的原因**：
   - 用户消息在事件流中的 `role` 字段值不是预期的 `'user'`
   - `cleaned` 文本为空或被过度过滤
   - 用户消息事件类型（`kind`）与当前处理逻辑不匹配
   - `flushMergeBuf()` 等操作可能意外清除了待显示的用户消息内容

## 复现步骤

1. 打开 agentapp Web 界面
2. 在任意 session 中输入用户消息并发送
3. 观察聊天列表 —— 用户消息不出现，只有 agent 回复和工具调用信息

## Acceptance criteria

- [ ] 用户发送的消息能在聊天界面正确显示
- [ ] 用户消息和 agent 消息在视觉上可区分（不同背景色/对齐方式）
- [ ] 修复后不影响现有 agent 消息、工具调用、thinking block 的显示
- [ ] 修复后 `flutter test` 无回归
- [ ] 在现有 Chrome tab 中验证修复效果

## Blocked by

None — can start immediately.
