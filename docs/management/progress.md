# Delivery Progress Tracking

## 1) Sprint / Work Window
- Window: 2026-05-21
- Manager: team-lead
- Last updated: 2026-05-21

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
- 2026-05-21: R-007 hermes 支持启动 — 需求文档、设计计划、CLI协议探索完成，hermesclient TDD开发中
- 2026-05-09 deliveries: ARCH-002 (WS Service层), ARCH-003 (SessionWatcher真seam), ARCH-006 (JSON-RPC类型安全), CLI client骨架+验证
- 2026-05-08 deliveries: R-006 (Portal本机QR连接), i-012-followup (`currentBound()` fix)
- 2026-05-01 deliveries: i-012 (`/clear` tmux bug + session switch re-tracking)
- In progress: ARCH-005 Flutter重构, R-007 hermes provider支持
- All architecture refactor tasks (ARCH-001~ARCH-007) completed and merged to main
- Historical records archived to `docs/archive/2026-04/progress-2026-04-30.md`
