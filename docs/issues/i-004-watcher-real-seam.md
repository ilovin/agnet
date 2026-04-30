---
id: i-004
type: architecture
priority: high
status: pending
owner: —
worktree: arch-003-watcher-seam
---

# SessionWatcher 真 seam

## Parent

架构重构批次2 — 来自 `docs/issues/README.md`

## What to build

修复 `SessionWatcher` 假 seam — 当前接口只有 3 个方法，但 Manager 立即 downcast 调用类型特定方法。扩展接口使其真正成为多态 seam。

## Vertical slice

- **Schema**: 扩展后的 `SessionWatcher` 接口
- **API**: Manager 通过接口调用，无需类型断言
- **UI**: 无（纯后端重构）
- **Tests**: 多 provider watcher 测试（ClaudeWatcher + OpenCodeDBWatcher 通过同一接口测试）

## Current interface (broken)

```go
type SessionWatcher interface {
    Start() error
    Stop()
    SetSkipExisting(bool)
}
```

Manager 调用 `SetWorkDir()`, `SetPID()`, `SetTmuxTarget()`, `OnSessionSwitch()` — 都不在接口上。

## Target interface

```go
type SessionWatcher interface {
    Start() error
    Stop()
    SetSkipExisting(bool)
    SetWorkDir(string)
    SetPID(int)
    SetTmuxTarget(string)
    OnSessionSwitch(func())
    // ... 其他通用方法
}
```

## Acceptance criteria

- [ ] `SessionWatcher` 接口扩展，覆盖 Manager 所有调用点
- [ ] Manager 中所有类型断言 (`w.(*ClaudeWatcher)`) 消除
- [ ] `newSessionWatcher` 返回接口后不再 downcast
- [ ] ClaudeWatcher 和 OpenCodeDBWatcher 都实现扩展接口
- [ ] `./scripts/test.sh unit` 中 watcher 模块 PASS
- [ ] `cd agentd && go test ./internal/watcher/` 开发阶段可用
- [ ] `./scripts/test.sh unit` 全模块无回归

## Blocked by

- [i-002 Manager 拆分为核心模块](./i-002-manager-split.md) — EventManager/ProcessManager 提取后 watcher 调用模式更清晰

## Notes

- 如果某些方法确实 provider-specific，考虑用 embedded interface 或 option pattern
- 不要为统一而统一 — 只把 Manager 实际调用的方法抽象到接口
