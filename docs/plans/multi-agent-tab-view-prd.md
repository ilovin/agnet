# PRD: Multi-Agent Tab View — agentapp 多子 Agent 切换视图

> 关联需求：[[r-016-app-multi-agent-tab-view]]
> 关联前置 bug：[[i-018-tmux-contentmatch-session-bleed]]
> 状态：Draft（2026-05-27）
> 来源：用户反馈（"派 5 个并行 dev agent 时只能切回终端 `claude agents` 才能看进度"）

## Problem Statement

phone-talk 已落地 Manager-Agent 架构，所有 dev/test 工作由 Manager 拆解后并发派给 worktree 子 agent。R-008 / R-012 实战已多次出现 5 个并行 dev agent 同时跑的场景。但当前观测路径存在以下问题：

1. **app 端只能看 Manager 主会话** — agentapp（Web + 移动）当前的 `agent_detail_screen` 是单 agent 视图。Manager 派出的子 agent 在 app 上完全不可见，用户必须切回 macOS 终端跑 `claude agents` 才能 attach。
2. **移动端无 `claude agents` 通道** — 手机上没有终端，等于完全没法观察子 agent 进度，只能等 Manager 转述。
3. **多 agent 并发时归属错乱** — 即使切到 `claude agents`，agentd 的 tmux content-match 机制在多 agent 同时跑时会把 jsonl 输出绑到错误 pane（详见 i-018）；用户在 tab A 看到的可能是 tab B 的内容，无从校对。
4. **dashboard 不替代会话级实时流** — dashboard 节点卡只显示节点级聚合（agent 总数、最后活跃时间）；不能展开每个 agent 的逐条消息流。
5. **R-009 / d-009 的播报机制未闭环** — Manager 播报 agentId 后用户仍需手动开终端，移动端用户根本走不通这条路径。

## Solution

分两个阶段串联交付：

**阶段 1（基础修复）**：先修复 agentd 的 tmux content-match 跨 session 串扰（i-018），让 jsonl ↔ tmux pane 路由在多并发场景下稳定可靠。这是后续 app 端 tab 视图能正确显示的前提。

**阶段 2（功能落地）**：在 agentapp 的 agent 详情视图引入"多子 agent tab 容器"。当节点上有 ≥ 2 个 agent 时，顶部出现 tab bar，每个 tab 对应一个子 agent，显示其实时输出 / status / 关联 task / worktree。点选切换；非当前 tab 收到新消息显示未读徽标。tab 视图与 dashboard / Canvas Session Panel / `claude agents` 终端互为补充，不替代。

阶段 1 的修复路径在 i-018 内已枚举多方案（P1-P5），最终选型在 Manager 评审拆解时拍板。阶段 2 的 widget / 数据通道 / 状态管理选型在 R-016 Explore 阶段确定。

## User Stories

1. 作为 Manager，我希望派出 5 个并行 worktree dev agent 后用户能直接在 app 上看每个 agent 的实时进度，这样我就不需要主动转述每个 agent 在做什么。
2. 作为移动端用户，我希望在手机 app 上能切换 tab 看不同子 agent 的输出，这样我离开电脑也能跟进进度。
3. 作为 Web 端用户，我希望在 Chrome 标签页上有一个 tab bar，点 tab 即可切换查看不同子 agent，这样我不需要再开终端跑 `claude agents`。
4. 作为重度用户，我希望非当前 tab 收到新消息时有未读徽标提示，这样我能在多 agent 同时活跃时知道哪个需要关注。
5. 作为开发者，我希望每个 tab 显示该 agent 关联的 task ID 和 worktree 路径，这样我能立即定位它在做哪个 task、动哪个工作树的代码。
6. 作为追踪者，我希望 agent 完成后 tab 不自动消失，能保留到我手动关闭，这样我能回看完成 agent 的全部历史。
7. 作为防误操作者，我希望 working 状态的 tab 不能被误关，这样不会因为误触失去活跃 agent 的实时窗口。
8. 作为单 agent 场景用户，我希望节点上只有 1 个 agent 时 UX 与现状完全一致（无 tab bar），这样不会增加我的认知负担。
9. 作为低端机用户，我希望 5 个 tab 同时活跃时 app 不会 OOM 或明显卡顿，这样在中端 Android 设备上也能用。
10. 作为离线场景用户，我希望网络中断重连后 tab 列表能正确恢复，这样我不会失去当前正在跟踪的 agent。
11. 作为多设备用户，我希望 Web 端选 tab A 不会影响移动端的选 tab B，每端独立切换，这样我能在不同设备并行观察不同 agent。
12. 作为暗色模式用户，我希望 tab bar / 徽标 / 关闭按钮在暗色主题下都清晰可读，这样夜间使用不刺眼也不眯眼。
13. 作为交互响应者，我希望多个 tab 同时弹 AskUserQuestion / Permission 卡片时有徽标提示哪个 tab 需要响应，这样我不会漏掉等待中的请求。
14. 作为终端用户，我希望仍能用 `claude agents` 在终端 attach 子 agent，新 tab 视图只是补充而非替代，这样我不会失去现有工作流。
15. 作为只读观察者，我希望 tab 视图是只读的，不能在 app 上发起新子 agent，这样不会越权改变 Manager 的派单决策（派单仍由 Manager 主会话独占）。

## Implementation Decisions

### 阶段 1：tmux content-match 串扰修复（依 i-018）

#### 修复路径选型（多方案，未拍板，待 Manager 评审）

i-018 §修复路径已枚举 5 方案：

- **P1 — Pane-id 直接路由**（推荐主方向）：扩展 `~/.claude/sessions/<PID>.json` 加 `tmuxPane` 字段；agentd 完全跳过 content match，按 PID → pane 直接路由
- **P2 — 显式 marker 注入**：agentd 启动 claude 时注入 `PHONETALK_AGENT_MARKER=<uuid>`，让 marker 落到 jsonl 第一条消息；content match 优先匹 marker
- **P3 — pane title 作绑定键**：agentd 给每个派出的 claude 进程对应 tmux pane 改 title 为 agentId；watcher 用 pane title 而非 pane content 路由
- **P4 — 文件系统线索**：用 `/proc/<pid>/fd`（Linux）/ `lsof -p <pid>`（macOS）反查 claude 进程实际打开的 jsonl 文件；完全抛弃 content match
- **P5 — 临时降级缓解**：缩短 `contentMatchCacheTTL` 到 5s + 提高 `contentMatchMinMarginRatio` 到 0.50；仅作短期 patch，不是根治

**推荐组合**（待用户拍板）：
- 短期缓解：P5 + i-016 死亡进程清理
- 中期根治：P4（macOS 用 lsof）作主路由 + content match 仅在 fd 失败时 fallback
- 长期架构：P1 跟 claude 上游协商扩展 sessions 文件

#### 阶段 1 范围

- agentd `internal/scanner/content_match.go` 的修复
- agentd `internal/watcher/claude.go` 调用 content match 处的兜底逻辑
- agentd `internal/agent/manager.go` 死亡进程清理（i-016 配合）
- 新增多并发 agent 场景的 Go 单测和集成测试
- 不动 agentapp 任何代码

### 阶段 2：agentapp 多 agent tab 视图（依 R-016）

#### widget 容器选型（待 Explore 调研后拍板）

可选实现方式：

- **A. PageView + TabBar**（推荐）：每个 agent 一页，TabBar 控制；天然支持滑动切换、keepalive、横向滚动
- **B. IndexedStack + 自定义 TabBar**：每个 agent 一个 child，切换不重建；内存代价更高但状态保留最稳
- **C. NavigationRail（Web 宽屏）+ TabBar（移动窄屏）**：响应式分流；代码复杂度上升

#### 数据通道决策

- **复用现有 RPC**：`conversation.history(agent_id)` 和 `conversation.message` 事件按 agent_id 路由；多 tab 场景下并发拉 N 次 history（N ≤ 5）
- **不新增批量接口**（除非性能压测发现瓶颈）：避免协议层改动放大风险
- **WS 事件路由层修改**：确保 `conversation.message` 事件能按 agent_id 精准送到对应 tab 的 provider，不漏不重；现有路由如已支持则无需改动

#### 状态管理决策

- **沿用现有 Riverpod / Provider 架构**（不引入新框架）
- **每个 tab 独立 provider，按 agent_id 分片**；tab 列表 provider 按 (node_id, agentIds[]) 计算
- **滚动状态保留**：用 PageView 的 AutomaticKeepAliveClientMixin

#### 切换交互决策（用户已拍板）

- **tab 顺序**：按 agent 创建时间升序固定，避免抖动
- **tab 标题**：使用 **agent name**（如 `T6 deploy.sh DEPLOY_DRY_RUN`），不使用 task ID
  - 超长截断 + ellipsis（移动端约 12 字符，Web 宽屏约 24 字符）
  - hover（Web）/ long-press（移动端）显示完整名 tooltip
  - 截断位置建议：保留前缀 + 末尾几位（如 `T6 deploy.sh D…RUN`）
- **未读徽标**：非当前 tab 每收到一条新消息计数 +1；切到该 tab **立即清零**（不要求滚到底部）
  - 多 tab 同时等待交互卡片响应时用数字徽标传达数量
- **tab 生命周期与 agent 绑定（关键）**：
  - agent 还在跑（idle / working / waiting / error） → tab 在
  - agent 退出（completed / stopped / 进程死） → tab **自动收掉**，不保留
  - 不存在"completed agent tab 保留时长"概念，无"全部关闭"按钮
- **关闭 tab = detach（不 kill agent）**：
  - 用户可主动关闭任一 tab（无论 status 如何）
  - 关闭只移除 UI，**不向 agent 发任何 kill / interrupt 信号**
  - 下次刷新或重连若 agent 仍存活 → tab 重新出现
  - agent 自然完成或被 Manager 显式 kill → tab 永久收掉
- **跨设备同步 tab 选择**：**不同步**，每端独立切换当前 tab，避免协议开销
- **多 tab 同时弹交互卡片**：仅徽标提示，不加全局 toast / 顶部横幅
- 单 agent 场景 tab bar 隐藏（兼容现状）

#### 暗色模式 / 移动端

- 复用现有 ThemeData / ColorScheme
- 移动端窄屏（< 600 dp）TabBar `isScrollable: true`；宽屏 `isScrollable: false`
- 徽标在暗色背景下使用描边或亮色填充

### 阶段 3（可选优化，不在本 PRD 阶段 2 必做范围）

- tab 分组（按 task / 按 worktree）
- 全局未读 toast / 横幅通知
- 跨设备 tab 选择同步
- dashboard 节点卡显示 "5 agents (3 working)" 汇总文案
- 历史 tab 折叠（completed agent 收纳到二级菜单）

## Testing Decisions

### 什么是一个好的测试

- 阶段 1：测 content match / fd 路由的输入输出（pane content + candidates 输入，绑定结果输出），不测内部 fingerprint 算法细节
- 阶段 2：widget 测试覆盖 tab 切换、徽标更新、关闭限制；不测 PageView 内部渲染细节
- 优先测试边界：5 并发 / 单 agent 退化 / 离线重连 / 暗色模式 / 移动端窄屏

### 需要测试的模块

#### 阶段 1（agentd Go 单测）

1. **content_match 多 agent 场景**：5 并发 candidate + 5 个 tmux pane，验证每个 pane 绑定到正确的 jsonl
2. **死亡进程清理 + content match 联动**：进程退出后 candidate 池剔除死 jsonl，新进程不被错绑
3. **lsof / fd 路径**（macOS / Linux）：验证 PID → jsonl 解析正确性
4. **缓存 TTL 行为**：缩短 TTL 后是否能及时纠正错绑

#### 阶段 2（agentapp Flutter 测试）

1. **TabBar 显隐逻辑**：1 agent 隐藏，≥ 2 agents 显示
2. **tab 切换不丢消息**：切换前后 message list 一致
3. **未读徽标更新**：非当前 tab 收到事件徽标 +1，切回清零
4. **关闭限制**：working tab 关闭按钮不可见 / 灰；completed tab 可关闭
5. **滚动位置保留**：切走再切回 ListView scroll offset 恢复
6. **离线重连**：模拟 WS 断开 + 重连，tab 列表正确恢复
7. **暗色主题**：tab bar / 徽标在 dark theme 下可读

### 现有测试参考

- `agentd/internal/watcher/claude_test.go:399`：contentMatchFromCandidates 测试模式
- `agentd/internal/scanner/content_match.go:108-127`：已有的 `PrimeContentMatchCacheForTest` 跨包测试钩子
- `agentapp/test/`：Flutter widget test 现有模式

### 集成 / E2E 验证

- **5 agent 并发实战**：用 worktree sandbox 启动 5 个并行 dev agent，跑 30 分钟相似主题对话，对比每个 tab 显示内容与 `claude agents` attach 内容一致
- **现有 Chrome 验证**：按 user memory 中的 "existing Chrome only" 反馈（未入库），所有 Web 端验证必须用现有 Chrome 标签页
- **移动端验证**：Chrome DevTools 移动模拟器 + 真机各跑一遍

## Out of Scope

1. **app 端发起新子 agent** — 派单能力由 Manager 主会话独占，app 仍是只读观察窗口
2. **agent 间通信** — tab A 输入不影响 tab B；tab 之间不联动
3. **跨节点 tab 视图** — tab 仅限单节点内多 agent；不做"跨多机器全聚合"
4. **替代 `claude agents` 终端** — tab 视图是补充；终端 attach 通道继续保留
5. **替代 dashboard** — dashboard 节点级聚合不变
6. **claude CLI 自身改动** — P1 路径如需上游配合，不在本 PRD 范围（属于"长期架构"待协商项）
7. **iOS / Android 各自的原生 tab UI** — Flutter 同一份代码即可，不做平台分叉
8. **跨设备 tab 选择同步** — 每端独立选择
9. **历史 tab 持久化到本地存储** — 关闭 app 后 tab 状态丢失，下次启动按 dashboard agent 列表重建
10. **dashboard 节点卡的 agent 汇总文案** — 留给阶段 3 优化

## Further Notes

### 阶段间依赖时序

- 阶段 1 必须 **closed 并合入 main** 才能开工阶段 2；不强制额外观察期
- 阶段 2 开工硬前置：i-018 已 closed（合入 main）
- 阶段 1 期间可并行做阶段 2 的 Explore（widget 调研 / 数据通道调研），但不动代码

### 与 R-009 / d-009 的关系

- R-009 / d-009 让用户能用终端 `claude agents` 看子 agent，是"终端通道"
- R-016 让用户能用 app tab 看子 agent，是"app 通道"
- 两条通道并存；移动端用户主要用 app，桌面用户两者皆可
- d-009 的 Manager 播报机制不变，仍要播报 agentId

### 与 R-010 的关系

- R-010 让 app 能渲染 4 类交互卡片（AskUserQuestion / ExitPlanMode / Permission Bash / Permission Edit）
- R-016 把这些卡片嵌到对应 tab 里；用户在 tab 内的回复仍走 `conversation.send` / `conversation.permission_response`
- 多 tab 同时弹卡片时通过未读徽标提示用户哪个 tab 等待响应

### Worktree 隔离背景（R-012）

- R-012 的 sandbox 模式让 dev agent 能在独立 worktree + 独立 HOME 跑，不污染主部署
- 5 个并发 dev agent 即对应 5 个 sandbox / 5 个 worktree / 5 个 claude 进程
- i-018 的修复必须考虑 sandbox 模式下的 tmux pane / PID / HOME 重定向场景

### 性能 budget（指导阶段 2 设计）

- 5 tab × 1000 messages 内存占用目标 < 200MB（移动端）
- tab 切换响应 < 300ms（Web Chrome）
- 5 并发 WS 流移动数据网络下不应导致明显延迟（> 5s 算异常）

### 已拍板（用户决策，2026-05-27）

- **tab 命名规则**：使用 agent name（如 `T6 deploy.sh DEPLOY_DRY_RUN`），超长截断 + tooltip
- **tab 生命周期**：与子 agent 生命周期 1:1 绑定；agent 退出 tab 自动收掉，无"completed 保留时长"，无"全部关闭"按钮
- **关闭 tab = detach**：不 kill agent；agent 仍活则下次刷新/重连时 tab 重新出现
- **跨设备 tab 选择不同步**：每端独立切换
- **未读徽标重置时机**：切到该 tab 立即清零
- **多 tab 弹卡片提示**：仅徽标，不加全局 toast / 横幅
- **阶段 1 → 2 的观察期**：drop "1 周稳定" 门槛；i-018 closed 即可开工

### 已知未决问题（仍留给用户拍板）

- 阶段 1 修复方案选型（i-018 P1 / P2 / P3 / P4 / P5 组合）
- tab 数量上限（5 / 10 / 不设上限）— 暂按"不设上限，用户负担过重再优化"推进
- 阶段 3 优化项是否同期立项

## 引用

- 需求：[[r-016-app-multi-agent-tab-view]]（已入库）
- 阻塞 bug：[[i-018-tmux-contentmatch-session-bleed]]（已入库）
- 相邻需求：r-009（Claude Code Agent View 适配，未入库）、r-010（app 端 Claude 交互工具，未入库）
- 设计：d-009（Manager Workflow 与 Agent View 集成，未入库）
- 相关 issue：i-016（PeriodicScan + 死亡进程清理，未入库）、i-017（Dashboard stale lastMessageTime，未入库）、i-012-followup（tmux `/clear` watcher 不切换，未入库）
- Sandbox 背景：[[r-012-sandboxed-worktree-isolation]]（已入库）
- 关键代码：
  - `agentd/internal/scanner/content_match.go:533-741`（contentMatchSession 主流程）
  - `agentd/internal/watcher/claude.go:322-384`（watcher 调用 content match + pidMapSessionFile）
  - `agentapp/lib/screens/dashboard_screen.dart:1678`（_CanvasSessionPanel 现状）
  - `agentapp/lib/screens/agent_detail_screen.dart`（单 agent detail screen）
