---
name: R-016 App 多子 Agent Tab 视图
description: agentapp 端为 Manager 并行派出的多个 worktree dev agent 提供 tab 切换视图，每个 tab 显示一个子 agent 的实时输出/状态/关联 task，无需切回终端 `claude agents`
status: Pending
date: 2026-05-27
priority: High
source: 用户反馈 + r-009/r-010/d-009 现状
---

# R-016 App 多子 Agent Tab 视图

## 背景与动机

phone-talk 已落地 Manager-Agent 架构（参见 Manager 工作流文档 [[manager-workflow]] 与 user memory 中的 manager-agent 架构反馈，未入库）：Manager 把所有实际 dev/test 工作拆成子 agent，并发派到独立 worktree 执行。R-008 / R-012 阶段已经多次出现并行 5 个左右 dev agent 的真实场景。

当前查看子 agent 进度的唯一通道是终端命令 `claude agents`：

- R-009 / d-009 把它定为"播报 agentId + 让用户另开终端跑 `claude agents`"的工作流
- 但这个路径**完全脱离 phone-talk app**：用户在 app 上看到 dashboard 节点和 Manager 主会话，想看子 agent 实时状态时必须切回 macOS 终端、记住 agentId、attach、`←` detach、再切回 app
- 移动端用户场景下尤其难受：手机上没有 `claude agents` 终端，等同于无法观察子 agent

R-010 让 app 能渲染 Claude 的交互卡片，但只针对**单个 agent 会话**。一旦 Manager 在主会话里并发派 5 个 dev agent，app 端依然只看到 Manager 那一条会话流，子 agent 的输出对 app 不可见。

**用户期望**：当 Manager 派多个并行子 agent 时，agentapp 在该会话节点下提供 **tab 视图**，每个 tab 对应一个子 agent，可点选切换查看实时输出/当前 status line/关联 task。tab 视图是会话内部的 sub-tab，不替代 dashboard 节点级聚合。

## 前置依赖（Blocking）

**i-018（tmux contentmatch 跨 session 串扰）必须先修复**。

现状：agentd 通过 tmux pane 的 content match 把 jsonl 落盘文件绑定到 tmux session（参见 `agentd/internal/scanner/content_match.go`，i-018 §Root Cause 详细分析）。当多个 claude 子 agent 在不同 worktree 并发跑时，content match 的 fingerprint fuzzy 匹配会把输出归属到错误的 session：

- tab A 显示了 tab B 应有的输出（错位）
- 一个 agent 退出后，新进程被绑到旧 session，残留消息混入新 tab
- cache TTL 30s 把错绑结果"锁死"半分钟，期间无法纠正

**未修复前不开本需求工作**。即使 app 端 tab UI 写好，底层数据归属错乱也会让所有 tab 显示混乱内容，反而更难排查。

i-018 issue 已立项跟踪。R-016 进入 Phase 4 拆解前，必须先确认 **i-018 已 closed**（合入 main）。close 即可开工，不强制额外观察期。

## 范围

### 必做

1. **多 agent tab 容器**
   - 在 agentapp 当前会话视图（点击 dashboard 节点后进入的 agent_detail_screen）之上，引入"多子 agent tab bar"
   - 当节点上只有 1 个 agent 时，tab bar 隐藏（保持现有 UX 不变）
   - 当节点上有 ≥ 2 个 agent 时，按 agent 出现顺序自上而下/自左而右排 tab

2. **tab 内信息**（每个 tab 对应一个子 agent）
   - 实时输出流（复用现有 conversation 渲染 — text + tool_use + tool_result + 交互卡片，R-010 已支持）
   - 当前 status line（idle / working / waiting / error / completed）
   - 关联 task ID（如 R-008-T6）+ 关联 worktree 路径（如 `.claude/worktrees/agent-ac93d0bf224a8cdeb`）
   - **tab 生命周期与 agent 绑定**：agent 还在跑则 tab 在；agent 退出（completed / stopped / 进程已死）后 tab 自动收掉，不保留历史 tab

3. **切换交互**
   - tab 顺序：按 agent 创建时间升序固定，避免抖动
   - tab 标题：使用 agent name；超长截断 + ellipsis（移动端约 12 字符，Web 宽屏约 24 字符），hover/long-press 显示完整名 tooltip
   - 未读徽标：非当前 tab 收到新 conversation event 时显示徽标（多个未响应交互卡片时用数字徽标）；切到该 tab 立即清零
   - **关闭 = detach**：用户可主动关闭任一 tab，**不会 kill agent**；agent 仍活着；下次刷新或重连时若 agent 仍存活，tab 会重新出现
   - agent 自然完成或被 Manager 显式 kill 后，tab 永久收掉，不再回来
   - 合并：跨节点同名 task（不太常见）暂不合并；每个节点独立维护自己的 tab 列表

4. **暗色模式 + 移动端适配**
   - tab bar 在亮色/暗色双主题下可读
   - 移动端窄屏（< 600 dp）下 tab bar 可横向滚动；宽屏下尽量平铺不折行

### 范围内（依赖项，已经存在）

- 复用现有 `_CanvasSessionPanel` / agent_detail_screen 的会话渲染层（`agentapp/lib/screens/agent_detail_screen.dart`、`agentapp/lib/screens/dashboard_screen.dart:1678` 附近）
- 复用 R-010 已实现的交互卡片（AskUserQuestion / ExitPlanMode / Permission）
- 复用 R-013 / R-014 修复后的消息合并 / 过滤逻辑

### 不做

- **app 端发起新子 agent**：本需求只读不写，不允许在 app 上发"派一个子 agent 去做 X"指令；该能力由 Manager 主会话独占
- **agent 间通信**：tab 之间不联动；用户在 tab A 输入不会影响 tab B
- **跨节点 tab 视图**：tab 仅限单节点内的多 agent；不做"跨多台机器的 agent 全聚合"
- **替代 `claude agents` 终端**：tab 视图是只读窗口；attach/detach/输入回传仍走 R-010 的 `conversation.send` 通道（即只对当前 tab 生效），不接管 Claude Code 内核的 agent 面板
- **替代 dashboard**：dashboard 仍为节点级聚合（节点状态、agent 数量、最后消息时间）；tab 视图是会话内 sub-tab

## 与 dashboard / canvas 的关系

| 层级 | 职责 | 本需求影响 |
|---|---|---|
| Dashboard 节点卡 (`NodeCard`) | 显示节点级聚合：节点状态、agent 总数、最后活跃时间 | 不变；可考虑显示"5 agents (3 working)"汇总文案，但**不在本需求范围**，留给后续优化 |
| Canvas Session Panel (`_CanvasSessionPanel`) | 节点下每个 agent 一个面板 | 改造为多 agent tab 容器；当 agent ≥ 2 时切换为 tab 视图 |
| Agent Detail Screen | 单 agent 详情会话流 | 嵌入到 tab 内；tab 切换时复用同一 detail 容器 + agent id 切换 |

**心智模型**：dashboard 是"多节点鸟瞰"；多 agent tab 是"单节点内多 agent 切换"；agent detail 是"单 agent 实时流"。三层各司其职。

## 限制清单

### A. 后端（agentd / agentgw）层面

1. **tmux content-match 错绑（i-018）** — 多并发 agent 场景下 jsonl 与 tmux pane 路由不可靠；详见 i-018（已入库 issue，本仓库 docs/issues/i-018-tmux-contentmatch-session-bleed.md）§Root Cause
2. **死亡进程残留（i-016）** — agent 退出后 store 仍保留记录，dashboard / tab 列表会显示鬼影 agent；PeriodicScan 间隔 120s，新 agent attach 延迟最多 2 分钟
3. **WS 事件路由按 agent_id** — 现有 `conversation.message` 事件按 `agent_id` 路由到 detail screen；多 tab 场景下需要确保事件能精准路由到对应 tab，不漏不重；如 agent_id 在生命周期内变化（如 attach 切换），现有路由会丢事件
4. **conversation.history RPC 单 agent 假设** — 当前协议按单 agent 拉历史；5 个并发 agent 进入 tab 视图时一次性触发 5 次 history 拉取，可能加倍 backend 压力
5. **stream-json vs watcher 双路径一致性** — agentd 通过 PTY 和 JSONL 文件两条路径读输出（参见 R-010 风险段）；多 tab 场景下两路要在 agent_id 维度对齐，否则同一 tab 会出现重复或缺失事件
6. **lastMessageTime stale（i-017）** — merge 逻辑可能保留旧 lastMessageTime；tab 排序若依赖该字段会抖动

### B. 前端（agentapp）层面

7. **`_CanvasSessionPanel` 现状不是多 agent 容器** — 现有代码（`dashboard_screen.dart:1678`）每个 agent 一个面板；需评估是改造为 tab 容器还是新建 widget
8. **路由栈深度** — Flutter Navigator 在嵌套 tab 时若不用 nested navigator，BackButton 会一次性退出整个 detail screen 而非切回上一个 tab；需要决定是否引入 nested navigator
9. **状态管理（Riverpod / Provider）粒度** — 现有 `nodes_provider` / agent provider 按 agent_id 分片；多 tab 同时活跃时需评估是否需要按 (node_id, agent_id) 联合 key
10. **滚动状态保留** — 用户在 tab A 滚到 100 行历史，切到 tab B 再切回，能否保留 tab A 的滚动位置？需 keepalive 机制（PageView + AutomaticKeepAliveClientMixin）
11. **交互卡片焦点冲突** — 用户在 tab A 的 AskUserQuestion 卡片选项已选中但未提交，切到 tab B 再回来，选中状态是否保留？需要把卡片 state 上提到 provider
12. **暗色主题下徽标对比度** — 红点/数字徽标在暗色背景下需要描边或替换色

### C. 性能 / 资源

13. **N tab × M 消息内存占用** — 5 个 agent × 1000 条消息 = 5000 条 ChatMessage 对象常驻内存；移动端低端机有 OOM 风险；需评估懒加载或窗口化
14. **WS 流量放大** — 现状每个 agent_id 订阅一条流；5 个 tab 即 5 条并发流；移动数据网络下可能拥塞
15. **tab 切换重绘代价** — 切 tab 触发 ListView 重建；如不缓存会闪烁；keepalive 后内存代价上升（见 13）
16. **fps 可见性** — 5 个并发 agent 同时 streaming 时，UI 主线程是否扛得住？非当前 tab 的事件可累积合并，但需要不漏 unread 计数

### D. 数据一致性

17. **历史回放 vs 实时 stream 差异** — 用户切到 tab B 时，先拉 history 拿到 N 条历史，期间 streaming 进来 K 条新消息；merge 顺序错可能导致重复或乱序（参考 R-013 dashboard 双轨更新坑）
18. **离线 / 重连恢复** — app 退到后台或网络中断后重连，多 tab 状态如何恢复？建议：重连后按 dashboard agent 列表重建 tab，丢弃本地状态；与 R-013 的 "事件驱动 + 定时 RPC 双轨" 一致
19. **跨设备 tab 选择不同步** — 同一用户的 Web 端选了 tab A，移动端可能选了 tab B；本需求**不做**跨设备同步，每端独立选择
20. **agent 状态枚举对齐** — backend agent status（StatusIdle / StatusWorking / StatusWaiting / StatusError / StatusCompleted）与 tab 上显示的 status line 文案需 1:1 映射

### E. 用户认知 / 交互

21. **tab 数量上限** — 5 个常见、10 个上限是否需要折叠？暂不设上限，先观察实战
22. **tab 命名长度** — 移动端窄屏下 tab 标题易溢出（已有现成 `maxLines: 2` 经验）；需要超长截断 + tooltip
23. **未读徽标重置时机** — 切到 tab 立即清零 vs 滚到底部才清零？建议立即清零，与微信/Slack 心智一致
24. **关闭 tab 的二次确认** — 关掉 completed tab 后历史还能找回吗？建议关闭只是从 tab 列表移除，agent 数据仍在 dashboard / store 中
25. **多 tab 同时收到交互卡片** — 5 个 agent 同时弹 AskUserQuestion 时，用户怎么知道哪个 tab 需要响应？徽标 + 高亮 + 可选的 toast 提示

### F. 与现有需求 / issue 联动

26. **R-009 / d-009 不被替代** — 多 tab 视图是补充而非替代；用户仍可用 `claude agents` 在终端 attach
27. **R-010 交互回传通道** — 用户在 tab 内的回复仍走 `conversation.send` / `conversation.permission_response`，与 R-010 一致
28. **R-013 merge 模式** — tab 列表更新需要遵循 R-013 的 "事件驱动 + 定时轮询不互相覆盖" 原则
29. **R-014 空消息过滤** — tab 内消息流仍走 R-014 的过滤逻辑

## 验收标准

### 功能
- [ ] 同一节点上有 ≥ 2 个并行 agent 时，agent_detail_screen 顶部显示 tab bar
- [ ] tab 数量 = 当前节点活跃 agent 数（completed / stopped 的 agent 自动从 tab 列表移除）
- [ ] 点击 tab 切换会话内容，实时输出流随之切换
- [ ] 每个 tab 显示 agent 关联 task ID 和 worktree 路径（如可拿到）
- [ ] tab 标题用 agent name；超长截断 + tooltip
- [ ] agent status line 显示 idle / working / waiting / error / completed
- [ ] 非当前 tab 收到新 event 时显示未读徽标；切回立即清零
- [ ] 用户手动关闭 tab 不 kill agent（关闭等于 detach）；下次刷新若 agent 仍活，tab 重新出现
- [ ] agent 退出后 tab 自动收掉，不留历史 tab
- [ ] tab 切换后滚动位置保留

### 体验
- [ ] tab 顺序按 agent 创建时间稳定排序，刷新不抖动
- [ ] 移动端窄屏下 tab bar 横向滚动顺畅
- [ ] 宽屏 / Web 端 tab bar 平铺
- [ ] 单 agent 场景 UX 与现状一致（tab bar 隐藏）
- [ ] tab 切换响应时间 < 300ms（Web 端 Chrome）

### 暗色模式
- [ ] tab bar、徽标、关闭按钮在暗色主题下可读
- [ ] 当前 tab 高亮在两套主题下都清晰可辨

### 与底层修复联动
- [ ] i-018（tmux contentmatch session bleed）已 closed 并合入 main（不要求额外观察期）
- [ ] 5 个并发 dev agent 实战场景下，每个 tab 显示的内容与 `claude agents` attach 看到的内容**完全一致**（无错位、无串扰、无延迟超过 5 秒）
- [ ] 一个 agent 退出后立即启动新 agent，新 tab 的内容不混入旧 tab 的残留消息

### 性能
- [ ] 5 个并发 tab、每个 tab 1000 条消息时，内存占用 < 200MB（移动端）
- [ ] 切 tab 不出现明显闪烁或卡顿
- [ ] 离线重连后 tab 列表能正确恢复

### 回归
- [ ] 单 agent 场景下 agent_detail_screen 行为不变
- [ ] dashboard 节点卡 / NodeCard / `_CanvasSessionPanel` 在没有多 agent 时显示不变
- [ ] R-010 的 4 类交互卡片在 tab 内仍可正确渲染并回传
- [ ] R-013 / R-014 的消息过滤与合并逻辑不被打破

### 验证
- [ ] 在现有 Chrome 标签页中跑 5 agent 并发场景验证（遵循 user memory 中的 "existing Chrome only" 反馈，未入库）
- [ ] 在移动端真机或 Chrome DevTools 移动模拟器中验证 tab 横向滚动

## 风险与未决问题

### 已拍板（用户决策，2026-05-27）

1. **tab 命名规则** — 使用 **agent name**（如 `T6 deploy.sh DEPLOY_DRY_RUN`），不使用 task ID。理由：agent name 是 Agent 工具 result 的现成字段，无需新增元数据通道
   - 显示策略：tab 标题区域固定宽度（移动端约 12 字符，宽屏 24 字符），超长**截断 + ellipsis**，hover/long-press 显示 tooltip 完整名（移动端用 long-press 触发 tooltip，Web 端用 hover）
   - 截断位置建议：保留前缀 + 末尾几位（如 `T6 deploy.sh D…RUN`），让 task 编号和动作关键词都可见
2. **tab 生命周期与子 agent 绑定** — tab 跟 agent 生命周期 1:1 绑定
   - agent 还在跑（status = idle / working / waiting / error） → tab 在
   - agent 退出（status = completed / stopped / 进程已死） → tab **自动收掉**，不保留
   - 不存在"completed agent tab 保留时长"概念，无"全部关闭"按钮
   - 用户主动**关闭 tab 等同于 detach**：只移除 UI，不 kill agent；agent 仍活着；下次刷新或重连时若 agent 仍存活，tab 重新出现
   - agent 完成或被 Manager 显式 kill 后，tab 永久收掉，不会再回来
3. **跨设备同步 tab 选择** — **不同步**，每个客户端独立选择当前 tab，避免协议开销
4. **未读徽标重置时机** — 切到该 tab **立即清零**，与微信/Slack 心智一致（不要求滚到底部）
5. **多 tab 同时弹交互卡片提示** — **仅徽标**，不加全局 toast / 顶部横幅，避免打扰；徽标在多 tab 同时等待响应时通过数字（而非红点）传达数量

### 待用户拍板

1. **tab 数量上限**：5 个常见，10 个+ 是否需要折叠/分页？暂不设上限，用户负担过重再优化

### 技术风险（同限制清单 §C/§D）

- **tab 数据通道**：现有 `conversation.history` / `conversation.message` RPC 假设单 agent；多 agent 并发拉历史时可能加倍 backend 压力。需要评估是否要新增 `node.agents.history(node_id)` 批量接口
- **WS 事件路由**：app 收到的 `conversation.message` 事件目前按 `agent_id` 路由到对应 detail screen；多 tab 场景下需要确保事件能精准路由到对应 tab 的 message 列表，不漏不重
- **i-018 修复时序**：i-018 是 agentd 端 tmux watcher 改动，回归风险大；本需求开工前必须确认 i-018 已 closed（不要求额外观察期）
- **tab 与 R-010 交互卡片的焦点冲突**：用户在 tab A 的 AskUserQuestion 卡片选了选项后切到 tab B，回来时卡片状态是否保留？需要在拆解阶段明确
- **tab 自动收掉时机的事件源**：tab 跟生命周期绑定要求前端能可靠收到 agent "退出"信号（status_changed → completed/stopped 或进程死亡通知）；当前 i-016 死亡进程清理未做，可能导致 tab 该收没收。需在拆解时确认事件链路

### 不确定项（留给 Explore agent 调研）

- 当前 `_CanvasSessionPanel` 是否已有 list-of-agents 数据结构可复用，还是需要新建 multi-agent provider
- 移动端导航栈 + tab bar 同时存在时是否会与 Flutter 默认 BackButton 行为冲突
- task ID / worktree 路径是否能从现有 agent metadata 通道直接拿到，还是需要 agentd 新增字段
- WS 事件路由层是否已支持按 (node_id, agent_id) 联合 key 派发

## 关联

- 直接前置：[[i-018-tmux-contentmatch-session-bleed]]（必须先修；已入库 issue）
- 完整 PRD：[[multi-agent-tab-view-prd]]（i-018 + R-016 合并的分期实施 PRD；已入库）
- 相关需求：r-009（Claude Code Agent View 适配，终端 `claude agents` 工作流，未入库）、r-010（app 端 Claude 交互工具渲染与回传，未入库）
- 相关设计：d-009（Manager Workflow 与 Agent View 集成设计，未入库）
- 相关 issue：i-016（PeriodicScan + 死亡进程清理，未入库 — 影响 tab 自动收掉时机）、i-017（Dashboard stale lastMessageTime，未入库 — tab 顺序稳定性可能受影响）
- Manager 工作流：[[manager-workflow]]（必须先 Explore → Decompose → Dev/Test；已入库）
- 痛点参考：R-008 / R-012 实战中并发 5 个 worktree dev agent 时无法在 app 端观察
