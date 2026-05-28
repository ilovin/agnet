---
id: i-012-followup
type: bug
priority: critical
status: open
parent: i-012
---

# i-012 Follow-up: tmux /clear 后 watcher 不切换 session file

## Root Cause

`ClaudeWatcher.refreshSessionFile()` 中的 `currentBound()` 闭包过于保守：

```go
currentBound := func() bool {
    for _, c := range candidates {
        if c.jsonlPath == w.path {
            return true
        }
    }
    if w.path != "" {
        if _, err := os.Stat(w.path); err == nil {
            return true  // ← 只要文件存在就返回 true
        }
    }
    return false
}
```

当用户在 tmux 中直接执行 `/clear` 时，Claude 创建新的 session file，旧文件仍存在于磁盘。`refreshSessionFile` 的 time filter 将旧文件过滤掉后，`candidates` 只剩新文件。但 `currentBound()` 发现旧文件还在，返回 `true`，阻止 `switchToFile` 执行。watcher 继续轮询旧文件，新事件永远读不到，UI 表现为无法交互。

前两版修复（`conversation.clear` RPC + UI 拦截 `/clear` + `ResetWatcherOffset`）只覆盖了"从 App 发送 `/clear`"的场景，但用户在 tmux 中直接输入 `/clear` 时完全绕过了这套逻辑。

## Fix

修改 `currentBound()` 的判断逻辑：
- 当前文件在过滤后的 `candidates` 中 → bound（正常情况）
- 当前文件不在 `candidates` 中，且 `candidates` 为空 → 检查文件是否最近被修改，如果是则 bound（保护外部提供的 session file）
- 当前文件不在 `candidates` 中，且 `candidates` 不为空 → **不 bound**，允许切换到更活跃的候选

同时考虑将 `switchToFile` 的 30s cooldown 缩短为 10s，避免 `/clear` 后的切换被之前的 cooldown 阻挡。

## Acceptance Criteria

- [ ] `currentBound()` 在旧 session file 被 time filter 过滤后不再阻止切换
- [ ] tmux 中直接 `/clear` 后，watcher 能正确切换到新 session file
- [ ] 新消息能在 UI 正常显示和交互
- [ ] 外部提供的 session file（Attach 模式）不会被错误切换
- [ ] 相关 Go 测试通过
