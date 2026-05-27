# R-012 沙箱化 worktree 隔离（HOME 重定向方案）

## 背景

R-008 解决了 deploy 链路 dry-run 静态校验，但运行时隔离仍是空白：

- 多 worktree 并行时，dev agent 在 worktree 子目录跑 `scripts/deploy.sh local` 会**覆盖主部署**（用未合 main 的 binary 替换运行环境，2026-05-27 #57 已踩此坑）
- worktree 集成测试若 spawn 真实 agentd/agentgw，会与主部署抢占 7373/7374 端口、`~/.agentd/data` sqlite lock、`~/.claude/projects/` 会话目录
- claude CLI 自身在 worktree 里跑出的 jsonl 会污染主 `~/.claude/projects/`，使 watcher 误识别为新 session

T1 调研已实测：**claude CLI、agentd、agentgw 都尊重 `HOME` 环境变量**（agentd `loadConfig` 通过 `os.UserHomeDir()`、agentgw 同），HOME 重定向作为主隔离机制可行。配合 agentd/agentgw config 里的 port + data_dir 注入，可实现**0 行 Go 代码改动**的完整沙箱化。

## 目标

让 dev agent 在 worktree 里能**安全地**：

1. 启动一套独立的 agentd + agentgw（独立端口、独立 data_dir、独立配置）
2. 跑一个独立的 claude CLI 实例（独立 `~/.claude/projects/`、独立 `~/.claude/agents/` 等）
3. 跑会启动真实 agentd/agentgw 进程的集成测试，不影响主部署

**关键约束**：纯 bash + 配置文件，**不改 agentd/agentgw 任何 Go 代码**。

## 范围

### 必做（本需求闭环）

- **T5**: `scripts/deploy.sh local` 在 worktree 子目录运行时 fail-fast 退出（仅对真实 deploy 子命令检查；sandbox 子命令豁免）
- **T3**: 新增 `scripts/deploy.sh sandbox <id> [--with-web]`、`sandbox-stop <id>`、`sandbox-list`
  - 沙箱目录布局 `$WORKTREE/.sandbox/<id>/{home,logs,pid,sandbox.env,dist}`
  - 自动找两个 17000-19999 区间的空闲端口
  - 写沙箱 agentd/agentgw config（port + data_dir + token + 关闭 tunnel/nodes）
  - static 默认 ln 到 `$REAL_HOME/.agentgw/static`（`--with-web` 重 build flutter web）
  - 编译 agentd/agentgw 到沙箱专属 dist（不污染主 `out/`）
  - 用 `HOME=$SANDBOX_HOME` 启动两个进程，pid 写文件
  - curl 验证 `http://localhost:$AGENTGW_PORT/` 200
- **T6**: 集成测试沙箱化（如果有测试假设固定端口/data_dir 则迁移）
- **T7**: 验证两组场景
  - **Test A**: 主部署 + 两个沙箱并行，PID/端口互不影响、agents 数据互不污染
  - **Test B**: 沙箱 claude CLI 只写沙箱 HOME，不污染主 `~/.claude/projects/`
- **T8**: `docs/operations/sandbox-development.md` 使用指南、`docs/management/manager-workflow.md` dev agent 推荐 sandbox、memory `feedback_unified_deploy_via_scripts` 补充沙箱模式说明
- 新增 `scripts/sandbox-claude.sh <id> [args...]` wrapper

### 不做

- **不改 Go 代码**（agentd/agentgw 本身）— HOME + config 已足够
- 不引入容器化（docker / podman）
- 不做远程沙箱（仅本地）

## 验收标准

1. `scripts/deploy.sh local` 在 worktree 子目录跑会立即退出非 0，明确提示 "must run from main worktree root"
2. `scripts/deploy.sh sandbox sandbox-test-1`：成功启动 agentd + agentgw，curl 返回 200
3. **互不干扰**：在主部署运行的同时启动两个沙箱，主 PID/端口不变，三者各自的 agents 表互不污染
4. 沙箱 claude 跑出的 jsonl 落到 `$WORKTREE/.sandbox/<id>/home/.claude/projects/`，主 `~/.claude/projects/` 不变
5. `scripts/deploy.sh sandbox-stop <id>` 成功 kill 两个 PID 并清理目录
6. `.sandbox/` 已加 `.gitignore`
7. `docs/operations/sandbox-development.md` 包含完整使用指南
8. agentd/agentgw 的 Go 源码**未改动**（git diff agentd/ agentgw/ 为空）

## 风险与权衡

- **沙箱 agentgw 关闭 tunnel** — 沙箱无远端访问能力，仅本地开发用；要测 tunnel 需用主部署或独立 worktree 测试
- **端口区间 17000-19999** — 与主 7373/7374 隔离足够远，避开常见服务端口
- **static 默认 ln 软链** — 沙箱起来后修改 web 资源会污染主 static；除非 `--with-web` 显式重 build
- **agentd binary 依赖**：沙箱编译到独立 dist，不依赖主 `out/`；编译耗时增加约 30s（首次）

## 拆解

- [x] T1: Explore 调研 — 已确认 HOME 重定向可行
- [x] T2: 评审 + 决定方案 A（HOME 重定向，0 Go 改动）
- [ ] T5: deploy.sh local fail-fast 检查
- [ ] T3: deploy.sh sandbox / sandbox-stop / sandbox-list + sandbox-claude.sh wrapper
- [ ] T6: 集成测试沙箱化（如适用）
- [ ] T7: Test A + Test B 实测
- [ ] T8: 文档 + memory

## 引用

- 触发本需求的事故：2026-05-27 #57 worktree dev 跑 deploy 覆盖主部署（[[feedback_unified_deploy_via_scripts]]）
- 前置需求：[[r-008-deploy-test-isolation]]（dry-run 静态层）
- 进度跟踪: `docs/management/progress.md`
