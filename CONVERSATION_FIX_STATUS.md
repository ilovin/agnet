# 对话系统修复状态

## 已完成

### 1. 用户消息记录 (conversationSend)
- ✓ 用户发送的消息现在被正确记录到 EventBuffer
- ✓ `conversation.message` 事件被广播
- ✓ 用户消息出现在历史记录中

### 2. PTY 输出标记 (manager.go)
- ✓ PTY 输出现在添加 `raw: true` 标记
- ✓ 客户端可以区分原始 ANSI 输出和结构化消息

### 3. Session 文件查找 (findSessionFile)
- ✓ 现在正确解析 PID 映射文件获取 sessionId
- ✓ 在 projects 目录下查找对应的 JSONL 文件

### 4. JSONL 解析 (watcher/claude.go)
- ✓ 支持 Claude Code 的 JSONL 格式
- ✓ 正确处理 `type`, `message.role`, `message.content` 字段
- ✓ 支持文本内容和工具调用

## 剩余问题

### 1. JSONL 文件延迟创建
Claude Code 的 JSONL 文件不会立即创建，需要满足以下条件之一：
- 用户发送第一条消息后
- 或者有特定的 session 配置

当前 watcher 在启动时立即查找文件，可能会失败。

**建议修复**: 让 watcher 持续重试或等待文件创建

### 2. 消息重复
当前实现中：
1. 用户消息通过 `conversationSend` 显式记录
2. Claude 的回复通过 PTY 读取（含 ANSI 序列）
3. 如果 watcher 启动，它会从 JSONL 读取同样的内容

这可能导致消息重复。

**建议修复**:
- 方案 A: 禁用 PTY 输出记录，完全依赖 watcher
- 方案 B: 给 PTY 输出添加特殊标记，客户端过滤显示

### 3. OpenCode 支持
当前 watcher 只支持 Claude Code 的 JSONL 格式。OpenCode 的格式可能不同。

## 下一步

1. 修改 watcher 启动逻辑，支持重试或延迟启动
2. 在 Flutter 客户端中过滤 `raw=true` 的消息
3. 测试完整的对话流程
