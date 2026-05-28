---
name: R-014 Empty User Message Filter
description: 双端协同修复空 user 消息泄漏问题：agentd stream-json 路径漏过滤 tool_result-only 消息，app 端过滤过头放行空串
---

# R-014 空 User 消息过滤修复

## 背景

近期两个独立 commit 分别修改了消息过滤逻辑，但各自引入了回归问题：

- **commit c68c8a6** (agentd): 在 watcher 路径 (`claude.go`) 正确跳过了 `tool_result`-only 的 user 消息，但未同步到 `stream-json` 路径 (`manager.go`)。
- **commit 7197158** (agentapp): 为了让短消息（如 "ok"）能显示，去掉了 `isNoiseOnlyText` 过滤，连带把 `cleaned == ""` 的守卫也一并移除。

## 需求

### Bug 1 — agentd 端漏过滤

**位置**: `agentd/internal/agent/manager.go:410-466` (stream-json 的 `case "user", "assistant"`)

**问题**: 解析 content array 时只提取 `type == "text"` 的 block。当 user 消息 content 数组中只有 `tool_result` block 时，`text == ""`，但事件仍被发送到前端。

**修复目标**:
1. 在 stream-json 路径的 content array 解析中，识别 `tool_result` block 并跳过（参考 watcher 路径 `claude.go:935-939`）。
2. 如果 user 消息的 content 数组解析后 `text == ""` 且没有 `tool_use`，则直接 `return` 不发事件（参考 `claude.go:944-946`）。
3. **TDD**: 先写测试，后实现。

**已有参考实现** (`claude.go:935-946`):
```go
case "tool_result":
    // Tool results are system-level messages...
// ...
if ev.Text == "" && !hasToolUse {
    return ConversationEvent{}, false
}
```

### Bug 2 — app 端过滤过头

**位置**: `agentapp/lib/screens/agent_detail_screen.dart:682-706`

**问题**: user 消息分支直接 `messages.add(ChatMessage(text: cleaned, ...))`，即使 `cleaned.isEmpty` 也放行。

**修复目标**:
1. 恢复 `cleaned.isNotEmpty` 守卫：空字符串的 user 消息不应被添加到消息列表。
2. 保留短消息（如 "ok"）显示能力：仅绕过 `isNoiseOnlyText` 的噪音规则，不绕过空串检查。
3. **TDD**: 先写 Flutter widget 测试，后实现。

## 验收标准

- [ ] agentd: stream-json 路径中 tool_result-only 的 user 消息被过滤，不发事件
- [ ] agentd: 有 text 内容的 user 消息正常通过
- [ ] agentd: 有 tool_use 的 assistant 消息正常通过（assistant 的 tool_use 不应被误过滤）
- [ ] agentd: 新增/修改的 Go 单元测试通过
- [ ] app: 空字符串 user 消息不添加到消息列表
- [ ] app: "ok" 等短 user 消息正常显示
- [ ] app: 新增/修改的 Flutter widget 测试通过
- [ ] app: `flutter analyze` 无新增错误
- [ ] 双端集成验证：在现有 Chrome 标签页中验证空消息不再出现，正常消息不受影响

## 关联

- 引入 Bug 1 的 commit: `c68c8a6`
- 引入 Bug 2 的 commit: `7197158`
- watcher 路径的正确实现: `claude.go:935-946` (commit `1675eb2`)
- 相关已完成任务: T-022 (watcher rebind + 空 user 消息)
