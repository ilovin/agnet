# R-008 部署链路隔离测试（dry-run + 静态层）

## 背景

提交 `f3db38d feat(scripts): unify build/packaging scripts (issue #1)` 后，`scripts/deploy.sh local` 出现回归：`install.sh` 找不到 `agentgw` 二进制（因 `dist/platform/` 未生成）。回归在 commit 035e26b 已修复（`deploy.sh` local 分支补 `package.sh` 调用 + `package.sh` mode 100644→100755）。

但事故暴露了 `scripts/test.sh` 的覆盖盲区：

- `scripts` 子命令 Test 13 仅做静态 grep，不真跑 `deploy.sh local`
- `smoke` 子命令真跑但默认调用链不带，且端口 7373/7374 占用即跳过
- 没有 shellcheck，没有脚本可执行权限位断言

直接补端到端冒烟会 break 用户当前在用的 agentd/agentgw（多 worktree 并行尤甚），所以需要隔离方案。

## 目标

让 `test.sh` 能在**任何 worktree、任何时刻**自动检测 deploy 链路回归，**不影响**当前运行中的 agentd/agentgw。

## 范围

### 必做

1. **静态层** — 集成进 `test.sh scripts` 子命令：
   - `bash -n scripts/*.sh` 语法校验
   - `shellcheck scripts/*.sh`（或至少 deploy/build/install/package.sh 四个核心脚本）
   - 关键脚本可执行权限位断言：`[[ -x scripts/{deploy,build,install,package,test}.sh ]]`

2. **dry-run 层** — `deploy.sh local --dry-run`：
   - 跑完整链路：build → package → 校验 install.sh `resolve_artifact` 能找到所有平台二进制
   - **不真 restart**：要么 stub 掉 `restart_agentgw` / `restart_agentd`，要么 `install.sh` 增加 `restart --check-only` 模式（仅解析二进制路径，不动进程）
   - 退出码 0 = 链路完整；非 0 = 哪一步断了，明确报告

3. **test.sh 集成** — `scripts` 或新增 `smoke-dry` 子命令调用 `deploy.sh local --dry-run` 并断言退出码 0。

### 不做

- 不改造 agentd / agentgw 让它们支持端口/数据目录配置注入（沙箱方案 B 暂不做，留给后续 R-XXX）
- 不改 smoke 子命令的"端口占用就跳过"逻辑（避免改动过大；dry-run 已能补上覆盖盲区）
- 不引入 docker / podman / nerdctl 等容器化测试基础设施

## 验收标准

1. **回归再现验证**：`git revert 035e26b` 后跑 `bash scripts/test.sh scripts`（或 dry-run 子命令），必须**失败**（指明 deploy.sh local 缺 package.sh 或 package.sh mode 644）
2. **无副作用**：在 agentd/agentgw 正在运行的情况下跑 dry-run，进程 PID 不变、端口持续可达、Flutter web 不掉线
3. **多 worktree 并行**：两个 worktree 同时跑 dry-run，互不干扰、都能成功
4. **shellcheck 通过**：当前 scripts/*.sh 在静态层全部通过，或明确豁免特定 SC-XXXX 警告
5. **与现有 test.sh 风格一致**：用例输出格式（`✅ Test N: ...`）、错误处理、日志路径与 `scripts/test.sh` 现有 16 个用例对齐
6. **CHANGELOG / progress.md 记录**：本次能力新增明确写入 `docs/management/progress.md`

## 风险与权衡

- **dry-run 仍是模拟**：抓不到 agentd 启动时才暴露的运行时配置错误。可接受 —— smoke 仍保留作为 CI 补充手段
- **shellcheck 可能引入大量 noise**：现有脚本未 lint 过，可能 100+ 条警告。建议初期只对 4 个核心脚本启用，或逐项 disable + TODO 注释
- **install.sh 加 `--check-only` 改动需要谨慎**：必须不改 restart 主流程的副作用顺序，避免再次引入回归

## 拆解（待 Explore 调研后细化）

- [ ] T1: Explore — 调研 deploy.sh / install.sh / agentd / agentgw 当前结构，确定 dry-run 注入点
- [ ] T2: 评审 Explore 结果 + 确定最终拆分
- [ ] T3: 实现 `install.sh restart --check-only`（或等价机制）
- [ ] T4: 实现 `deploy.sh local --dry-run`
- [ ] T5: 实现 test.sh 静态层（shellcheck + 权限位 + bash -n）
- [ ] T6: 实现 test.sh 集成 dry-run 用例
- [ ] T7: 验证（回归再现 + 无副作用 + 并行）

## 引用

- 触发本需求的修复 commit: `035e26b`
- 暴露盲区的根因 commit: `f3db38d`
- 遗留 issue: GitHub #2（android pipefail，独立于本需求）
- 进度跟踪: `docs/management/progress.md`
