# Delivery Progress Tracking

## 1) Sprint / Work Window
- Window: 2026-05-25
- Manager: team-lead
- Last updated: 2026-05-25

## 2) Overall Progress
- Total tasks: 28 active (4 new: R-007)
- Done (05-09): ARCH-002, ARCH-003, ARCH-006, CLI client skeleton+testing, pre-existing test fix
- Done (05-08): R-006, i-012-followup verified
- Done (05-01): i-012
- Done (04-30): T-014, TEST-007, ARCH-001, ARCH-004, ARCH-007, Test isolation fix
- In Progress: 2 (ARCH-005 flutter screens, R-007 hermes provider)
- Pending: 0
- New Issues: 0
- Overall health: 🟢 功能需求大部分交付; 🟢 架构重构全部完成; 🟡 R-007 hermes 支持进行中; 🟡 ARCH-005 Flutter重构进行中

## 3) Task Execution Board

| Task ID | Requirement ID | Owner | Assignee | Status | ETA | Last Update |
|---|---|---|---|---|---|---|
| T-014 | R-004 轻触显示时间 | Developer | dev agent | **Completed** | 2026-04-30 | Backend+frontend; backend 2个TDD测试agentd PASS; frontend 114/114 tests flutter PASS; analyze clean |
| TEST-007 | — | Developer | dev agent | **Completed** | 2026-04-30 | handler_integration_test.go PASS; attach→load history→agent.list HasHistory 端到端验证 |
| ARCH-001 | — | Developer | dev agent | **已合并** | 2026-04-30 | Manager拆分: merged to main at commit `0d8494b`; ProcessManager+EventManager+StreamParser+PermissionResolver; manager.go 2307→1472行 |
| ARCH-002 | — | Developer | dev agent | **已合并** | 2026-05-09 | WS Service层提取: merged to main; 6个函数提取+接口注入+13个测试; handler.go减少~115行; build+tests PASS |
| ARCH-003 | — | Developer | dev agent | **已合并** | 2026-05-09 | SessionWatcher真seam: merged to main; 接口扩展+no-op实现+消除downcast; tests PASS |
| ARCH-004 | — | Developer | dev agent | **已合并** | 2026-04-30 | Scanner FS abstraction: merged to main at commit `b51757e`; FileSystem接口+Real/Mem适配器; TDD |
| ARCH-005 | — | Developer | dev agent | **In Progress** | - | Flutter screens重构: DashboardService+AgentDetailService; TDD; worktree arch-005-flutter-screens |
| ARCH-006 | — | Developer | dev agent | **已合并** | 2026-05-09 | JSON-RPC类型安全v1: merged to main; 创建rpc_types.go; 完全消除handler.go中req.Params[...]使用; 326行新增/164行删除; build+tests PASS |
| ARCH-007 | — | Developer | dev agent | **已合并** | 2026-04-30 | NodeManager拆分: merged to main at commit `09e92f4`; NodeRegistry+TunnelManager+ProxyManager; TDD |
| **i-012** | — | Developer | dev agent | **Completed** | 2026-05-01 | `/clear` tmux bug + session switch re-tracking; 6 files; Go tests + 117 Flutter tests pass |
| **i-012-followup** | — | Developer | — | **Fixed** | 2026-05-08 | `currentBound()` 阻塞问题已修复: 60ba071+ece6d13 已合并到main; 12/12 watcher tests PASS |
| **R-006** | R-006 Portal本机QR连接 | Manager | team-lead | **Completed** | 2026-05-08 | Portal网页"尝试连接本机gw"按钮+二维码显示功能；Go测试2/2通过；Chrome验证完成 |
| **CLI Client** | — | Manager | team-lead | **Completed** | 2026-05-09 | agentcli骨架+编译: WebSocket客户端+cobra命令; 连接agentgw验证通过; list-nodes/watch-events工作正常 |
| **R-007** | hermes-agent Provider支持 | Manager | team-lead | **In Progress** | — | 需求文档已创建; hermes CLI协议探索完成; worktree: hermes-support; 设计计划完成; hermesclient TDD实现中 |

### 历史归档

> 04-30 及之前已完成的任务已归档至 `docs/archive/2026-04/progress-2026-04-30.md`

## 4) Blockers & Risks
| ID | Type | Description | Owner | Mitigation | Status |
|---|---|---|---|---|---|
| RISK-02 | Risk | 61 pre-existing flutter analyze issues | team-lead | Not blocking, but noise masks real issues | Accepted |
| R-007-RESUME | Risk | hermes `--resume` 跨oneshot调用是否保留上下文待验证 | hermes-explorer | 已确认: `--continue` 才真正追加; 采用 Gateway HTTP API 路径替代 oneshot | Resolved |
| RISK-H1 | Risk | oracle 机器 agentd 未部署 | hermes-dev-client | 使用 `scripts/deploy.sh oracle` | Pending |
| RISK-H2 | Risk | hermes gateway default bind=127.0.0.1仅loopback | hermes-dev-client | Oracle deploy: set `API_SERVER_HOST=0.0.0.0` | Pending |
| RISK-H3 | Risk | `API_SERVER_KEY` 可能未配置 | hermes-dev-client | 部署文档需包含gateway配置说明 | Pending |

## 5) Change Log
| Date | What Changed | By | Notes |
|---|---|---|---|
| 2026-05-25 | **PERF-CONTENTMATCH cached** | dev agent | commit `b2fb5e3` + merge `99e2420` `perf(scanner): cache contentMatch results and JSONL fingerprints`; 修 CJK 修复后 dashboard 打开 10s+ 回归（根因: AutoAttachExisting 每 15s 完整 staged matching, CJK 修复消除了"空字符串提前 reject"快路径）; 方案 A: contentMatch 整体结果 30s TTL 缓存（key=tmuxTarget+sortedCandidateIDs）; 方案 F: extractFingerprints 按 JSONL path+mtime+size 60s TTL 缓存; 5 个新缓存测试 PASS（含 race detector）; oracle/local 实测 cache hit 日志每 15s 出现，重路径密度从 15s 一次降到 30-45s 一次 |
| 2026-05-25 | **M3+M4 Manager 集成 shipped** | dev agent | commit `cfe031e` + merge `afa9890` `feat(agent): integrate HermesDBWatcher for real-time session switch detection`; newSessionWatcher 加 hermes 分支, OnSessionSwitch 复用 OpenCode 模板（UpdateResumeSessionID → ClearConversationEvents → EventBuf.Reset → load new session → broadcast conversation.cleared + agent.status_changed）; 三处启动 watcher: Attach / Re-Attach / LoadFromStore; Agent.sendingFlag (atomic.Bool) + BeginSend/EndSend/IsSending 解决与 chunk.Done 的竞态; 新增集成测试 manager_hermes_session_test.go + agent_sending_test.go; 全部测试 PASS（注: TestHermesDBWatcher_SendingCheckerSuppressesSessionSwitch 出现 sqlite BUSY flake, 重跑 PASS, 已建跟踪任务 #11）|
| 2026-05-25 | **M1+M2 HermesDBWatcher 包级实现 shipped** | dev agent | commit `f1ad44a` + merge `4865b4a` `feat(watcher): add HermesStateDBLoadSession and HermesDBWatcher`; 新增 `HermesStateDBLoadSession(sessionID)` 按指定 session 加载历史; 新增 `HermesDBWatcher` 类型（Start/Stop/SetSkipExisting/SetSessionID/OnSessionSwitch）; 3s 默认轮询 ticker（`hermesDBWatcherInterval` var 可注入）; 单测 10 个全 PASS |
| 2026-05-25 | **FIX-CONTENTMATCH-CJK + 测试加长 shipped** | dev agent | commit `3419d52` `feat(scanner): Unicode-aware content matching and test coverage`: nonWordRe 从 `[^a-z0-9]+` 改为 `[^\p{L}\p{N}]+`, pane 长度判断从 byte len 改为 utf8.RuneCountInString, 新增 3 个 CJK 测试; 解决中文用户 contentMatch 全部失效（中文清洗后空字符串）的 bug; commit `d3310f5` `test(scanner): lengthen CJK fixtures above 20-rune minimum`: 调整一个 CJK fixture 让 pane 长度过 20 rune 阈值 |
| 2026-05-25 | **scripts dry-run + CHECK_ONLY** | dev agent | commit `d8a7617` `chore(scripts): add deploy dry-run + install CHECK_ONLY + script test coverage`: deploy.sh 支持 `DEPLOY_DRY_RUN=1`, install.sh 加 `CHECK_ONLY=1`, 新增 scripts/test.sh |
| 2026-05-25 | **docs: hermes plan + slowness triage + deploy isolation** | team-lead | commit `1248b0c` `docs: add hermes polling plan, slowness triage, deploy isolation requirement`: 写入 plan 文档与诊断文档 |
| 2026-05-25 | **R-007 用户验收通过** | team-lead | 用户在已存在 Chrome tab 完成验收三件事: dashboard 速度恢复 ✅; contentMatch 不再串号 ✅; hermes session 切换实时感知 ✅（oracle 节点实测）; 关联任务 #1/#2/#4/#5/#6/#7/#8/#9 全部 completed |
| 2026-05-25 | **FIX-CONTENTMATCH-CJK shipped local** | dev/test agent | content_match.go nonWordRe 改 `[^\p{L}\p{N}]+` + utf8.RuneCountInString; scanner 单测全部 PASS（含 3 个新 CJK 测试）; scripts/build.sh + scripts/deploy.sh local 完成; /tmp/agentd-local.log 中文 pane 出现 `pane match success bestScore=13 secondBest=6 margin=7`（修复前为 score=0），同 pane 另一个 candidate 13/13 ambiguous reject 属于内容近似的预期行为；待用户在已存在 Chrome tab 完成最终业务验收 |
| 2026-05-21 | **R-007 需求+计划完成** | team-lead | docs/requirements/r-007-hermes-agent.md; docs/plans/hermes-provider-impl.md; Gateway API集成路径 |
| 2026-05-21 | **hermes-explorer 完成** | hermes-explorer | CLI协议探测: oneshot/REPL/gateway全面分析; 推荐Gateway HTTP API路径 |
| 2026-05-21 | **hermes-dev-client 启动** | team-lead | TDD开发 hermesclient HTTP client包 |
| 2026-05-09 | **ARCH-002/003/006 merged** | team-lead | 全部架构重构合并完成: ARCH-003→main, ARCH-002→main, ARCH-006→main; 修复预存在测试TestSessionCatalogReturnsGroupedData (opencodeFiles/claudeFiles已在9fad3f6移除) |
| 2026-05-09 | **CLI client validated** | team-lead | agentcli编译通过, 连接ws://localhost:7374/ws成功; list-nodes返回2个节点; watch-events正常; 修复float64/int64 ID映射bug |
| 2026-05-08 | **R-006 completed** | team-lead | Portal本机QR连接: agentgw /local-info端点+CORS, portal按钮+二维码, 2个Go测试通过, Chrome验证通过 |
| 2026-05-08 | **i-012-followup verified fixed** | team-lead | `currentBound()` 阻塞问题已修复: 60ba071+ece6d13 已合并到main; 12/12 watcher tests PASS; progress.md 状态同步 |
| 2026-05-08 | **ARCH-002/003 worktree rebased** | team-lead | arch-002-ws-service 和 arch-003-watcher-seam 已rebase到main |
| 2026-05-08 | **ARCH-003 completed** | team-lead | SessionWatcher真seam: 接口扩展+no-op实现+消除downcast; 测试通过; commit ac893e7 |
| 2026-05-08 | **ARCH-002 completed** | dev agent | WS Service层提取: 6个纯函数提取到AgentService; handler.go减少~115行; 13个边界测试; build+tests PASS |
| 2026-05-09 | **ARCH-002 analysis completed** | team-lead | WS Service层函数提取分析完成; 3批次函数分类; 设计文档已输出 |
| 2026-05-01 | **i-012 fixed** | dev agent | `/clear` tmux bug + session switch re-tracking; 6文件; agentd Go tests + 117 Flutter tests pass |

## 6) Completion Summary
- 2026-05-25: R-007 hermes session 切换实时感知 + contentMatch CJK 修复 + perf 回归紧急修复 — 用户 Chrome 验收通过（dashboard 速度恢复、contentMatch 不串号、hermes session 实时切换在 oracle 实测）；本轮交付 M1+M2 HermesDBWatcher 包级实现 (`f1ad44a`/`4865b4a`)、M3+M4 Manager 集成 (`cfe031e`/`afa9890`)、CJK 修复 (`3419d52`/`d3310f5`)、perf 缓存修复 (`b2fb5e3`/`99e2420`)、scripts dry-run/CHECK_ONLY (`d8a7617`)、文档 (`1248b0c`)；关联任务 #1/#2/#4/#5/#6/#7/#8/#9 全部 completed
- 2026-05-21: R-007 hermes 支持启动 — 需求文档、设计计划、CLI协议探索完成，hermesclient TDD开发中
- 2026-05-09 deliveries: ARCH-002 (WS Service层), ARCH-003 (SessionWatcher真seam), ARCH-006 (JSON-RPC类型安全), CLI client骨架+验证
- 2026-05-08 deliveries: R-006 (Portal本机QR连接), i-012-followup (`currentBound()` fix)
- 2026-05-01 deliveries: i-012 (`/clear` tmux bug + session switch re-tracking)
- In progress: ARCH-005 Flutter重构, R-007 hermes provider支持
- All architecture refactor tasks (ARCH-001~ARCH-007) completed and merged to main
- Historical records archived to `docs/archive/2026-04/progress-2026-04-30.md`
