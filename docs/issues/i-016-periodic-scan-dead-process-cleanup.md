---
name: i-016 PeriodicScan interval + dead process cleanup
description: agentd PeriodicScanAndAttach 扫描间隔过长（120s），且运行时无死亡进程清理，导致新进程attach延迟和僵尸记录残留
type: issue
---

# i-016 PeriodicScan 扫描间隔过长 + 运行时死亡进程未清理

## 问题描述

### 1. 扫描间隔过长
`agentd/internal/agent/manager.go:2111` 中 `PeriodicScanAndAttach` 使用 `120 * time.Second` 的 ticker：
```go
ticker := time.NewTicker(120 * time.Second)
```

这导致新启动的 claude/opencode 进程需要等待最多 **2 分钟** 才会被 agentd 发现并 attach。用户体验上就是"扫描太慢"。

### 2. 运行时无死亡进程清理
`LoadFromStore()` 在启动时会检查进程存活并清理已死记录，但运行中退出的进程不会被清理：
- `PeriodicScanAndAttach` 只负责 attach 新进程，不负责清理已死进程
- 已死进程的 agent 记录会一直保留在内存中，status 仍显示为 `live`
- 这会导致 dashboard 显示不存在的 agents，且可能占用错误的 session 绑定

### 3. Session 绑定错误（关联问题）
当同一 projectDir 下有多个 session 文件时，`findClaudeSessionInfo` 的 fallback 逻辑可能将进程绑定到错误的 session：
- `listSessionCandidates` 列出 projectDir 下所有 jsonl 文件
- `contentMatchSession` 依赖 tmux pane 内容匹配，匹配失败时 fallback 到最活跃的 candidate
- 多个 claude 进程在同一目录下运行时，容易出现 session 绑定混乱

## 复现步骤
1. 启动 agentd
2. 在 tmux 中启动 claude 进程 A，等待 attach
3. 退出进程 A（或 kill）
4. 在 tmux 中启动新的 claude 进程 B
5. 观察：
   - 进程 B 可能需要等待 2 分钟才被 attach
   - 进程 A 的残留记录仍然存在，status=live
   - 进程 B 可能被绑定到错误的 session

## 修复方案

### 扫描间隔
将 `120 * time.Second` 缩短为 `15 * time.Second` 或 `30 * time.Second`。

### 死亡进程清理
在 `PeriodicScanAndAttach` 的每次扫描循环中：
1. 扫描完新进程后，遍历现有 agents
2. 对每个 agent 检查进程是否仍在运行（`isProcessRunning`）
3. 如果进程已死：
   - 设置 status 为 `stopped`
   - 从内存中移除（或标记为 dead）
   - 可选：从 store 中删除

### Session 绑定改进（可选/后续）
- 增强 `findClaudeSessionsFromTasks` 在 macOS 上的准确性
- 考虑用 PID 到 session 的映射缓存来避免重复绑定

## 关联
- i-012-followup: `currentBound()` blocks watcher session switch
- R-014 验证过程中发现此问题
