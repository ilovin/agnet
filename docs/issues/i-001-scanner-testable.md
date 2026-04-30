---
id: i-001
type: architecture
priority: high
status: in-progress
owner: dev agent
worktree: arch-004-scanner-fs
---

# Scanner 可测试化

## Parent

架构重构批次1 — 来自 `docs/issues/README.md`

## What to build

将 `agentd/internal/scanner/scanner.go` 从紧耦合真实文件系统的不可测试模块，重构为通过 `FileSystem` 接口抽象的模块。提取后可用内存文件系统做全面单元测试，无需真实 Claude/OpenCode 安装。

## Vertical slice

- **Schema**: `FileSystem` 接口定义 (`ReadFile`, `ReadDir`, `Readlink`, `Stat`, `Exec`)
- **API**: `Scanner.ScanWithFS(fs FileSystem)` 新方法，保持 `Scan()` 使用 `RealFileSystem`
- **UI**: 无（纯后端重构）
- **Tests**: `MemFileSystem` 适配器 + `scanner_test.go` 覆盖 `findClaudeSessionInfo`, `findOpenCodeSessionInfo`, `scanLinux`, `scanDarwin`

## Acceptance criteria

- [ ] `FileSystem` 接口定义在 `agentd/internal/scanner/filesystem.go`
- [ ] `RealFileSystem` 和 `MemFileSystem` 两个适配器实现
- [ ] `scanner_test.go` 包含至少 8 个测试用例（Claude session 发现、OpenCode session 发现、无 session 回退、tmux target 解析、PID 过滤等）
- [ ] `./scripts/test.sh unit` 中 scanner 模块 PASS
- [ ] `cd agentd && go test ./internal/scanner/` 开发阶段可用
- [ ] `./scripts/test.sh unit` 全模块无回归
- [ ] `ProcessInfo` 和 `Scan()` 签名不变（向后兼容）

## Blocked by

None — can start immediately.

## Notes

- 当前 `findClaudeSessionInfo` ~100 行嵌套文件系统遍历，是主要测试障碍
- `homeBaseDir` 变量已存在用于测试覆盖，保留并扩展此模式
