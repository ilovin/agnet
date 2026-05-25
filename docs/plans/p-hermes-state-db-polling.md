# P: Hermes session 实时感知（state.db 轮询）

## 0. 背景与问题

phone-talk 项目 hermes provider 的 session 切换检测当前**完全依赖 HTTP `/v1/chat/completions`** 的响应头 `X-Hermes-Session-Id`：见 `agentd/internal/ws/handler.go:2401-2415`，仅在 `chunk.Done` 时通过 `UpdateResumeSessionID` 更新 agent 的 resume session ID。

但是用户在同一个 hermes CLI 中输入 `/clear` 切换 session 时**不会触发 HTTP**，因此 agentd 无感知。后果：
- `conversation.history` 拉到的是旧 session 的消息。
- 下次 `hermesSend` 用旧 sessionID 调 ChatCompletion，要么续到错误 session，要么生成第三个 session。
- app 端看到的 EventBuf 与 hermes CLI 实际的对话流脱钩。

用户已确认架构方向：在 agentd 增加 **state.db 轮询**作为实时感知通道。HTTP→tmux 路径迁移是后续独立任务，**不在本计划范围**。

## 1. 现状摘要（目标读者：实施 dev agent）

- `agentd/internal/watcher/hermes_db.go:14-56` `HermesStateDBHistory()` 已实现"取最近活跃 session 的全部历史"，依赖 SQL `SELECT s.id FROM sessions s JOIN messages m ON s.id=m.session_id GROUP BY s.id ORDER BY MAX(m.timestamp) DESC LIMIT 1`。`?mode=ro&_journal_mode=WAL&_busy_timeout=3000` 已经是只读 + WAL，对生产环境 hermes 并发写是安全的。
- `agentd/internal/agent/manager.go:2064-2097` hermes 的 `Attach` 流程：创建 agent、`SaveAgent` 到 store、调 `HermesStateDBHistory()` 灌历史。**未启动任何 watcher**——这是本任务要补的洞。
- `agentd/internal/agent/manager.go:2293-2356` `newSessionWatcher()` 中 OpenCode 的 `OnSessionSwitch` 已实现完整的"session 切换→ Reset EventBuf → Clear 持久化历史 → 加载新 session 历史 → 广播 conversation.cleared + agent.status_changed"模板。本计划 **直接复用此模板**。
- `agentd/internal/watcher/opencode_db.go:151-162, 388-418` 的 `loop()` + `refreshSession()` 是本计划 `HermesDBWatcher` 的最佳参考实现。
- `agentd/internal/scanner/scanner.go:159-160` 的注释 "Hermes via HTTP does not need tmux/PTY" 维持不变——本计划不动 attach mode。
- `agentd/internal/agent/agent.go:43, 152-158` `Agent.w watcher.SessionWatcher` 字段 + `setWatcher/Watcher()` 已为 hermes 复用预备就绪。

## 2. 总体方案

新增 `internal/watcher/hermes_db.go` 中的 `HermesDBWatcher` 类型，实现 `watcher.SessionWatcher` 接口。

每个 hermes agent 在 `Attach` 成功后启动一个 `HermesDBWatcher` goroutine，周期轮询 `~/.hermes/state.db`：
1. 检测当前活跃 session（"最近 message 所在的 session"）是否变化 → 触发 `OnSessionSwitch`。
2. 检测当前 session 内是否有新 message → 通过 `callback` emit `ConversationEvent`，让现有 `makeWatcherCallback` 链路把事件落盘并广播。

`OnSessionSwitch` 复用 OpenCode 现有处理逻辑：Clear → reload → broadcast `conversation.cleared` + `agent.status_changed`。

## 3. 设计决策

### 3.1 后台 goroutine 生命周期

**启动时机**：每个 hermes agent 一个独立 watcher，**不**全局共享一个。
- 在 `manager.go:2064-2097` 的 hermes Attach 分支末尾、return ag 前调 `m.newSessionWatcher("hermes", ...)` + `w.Start()` + `ag.setWatcher(w)`。
- 在同 PID 重 attach 分支 (`manager.go:1994-2057`) 的 hermes 分支同样补 watcher 启动（参考 opencode/claude 处理）。
- 在 `LoadFromStore` (`manager.go:223-231`) 的 hermes 分支补 watcher 启动，让 agentd 重启后 hermes agent 也能继续感知 session 切换。

**理由**：
- 每 agent 一个 watcher 与 OpenCode 模式一致，避免全局 watcher 还要做 agentID 路由。
- 多 hermes agent 都监视同一个 state.db 并不浪费——sqlite WAL 模式下并发只读 + 事务级缓存极廉价；且每个 watcher 关心的是 **它自己** 的 sessionID 是否变化，状态机隔离更清晰。

**停止时机**：
- `Manager.Remove(id)` 链路通过 `Stop()` 已经会调 `ag.kill()`，但**不会**停 watcher。需在 `Remove` (`manager.go:1706`) 中显式 `if w := ag.Watcher(); w != nil { w.Stop() }`，与 OpenCode 一致。**TODO 验证**：现有 Remove 是否已有此处理？grep 显示 `setWatcher(nil)` 多次，但未在 Remove 中。建议在 Remove 中显式停 watcher（同时让 OpenCode/Claude 也受益）。
- `Attach` 重 attach 路径 (`manager.go:2014-2018, 1919-1922`) 已有 `oldWatcher.Stop()`。

### 3.2 轮询策略

**周期：3 秒。**

理由：
- OpenCodeDBWatcher 用 3s（`opencode_db.go:152`），ClaudeWatcher 用 2s（`claude.go:136`）。Hermes CLI 用户感知到 `/clear` 切换的延迟容忍度与 OpenCode 同级（不像 Claude 那样有打字流式预期）。
- 2s 也可以但收益有限：hermes 的 SSE 流式渲染走的是 HTTP 路径，state.db 轮询主要服务于 `/clear` 切换感知，3s 用户基本无感。
- 对 sqlite 的查询非常便宜（两条索引查询 + 一次 ORDER BY MAX），3s 周期下 CPU/IO 微不足道。

**退避策略（保守起见，第一版不做）：**
- 不引入"连续无变化拉长周期"。理由：3s 已经够慢，再退避会让 `/clear` 检测延迟变得不可预测。
- 错误退避：连续 3 次 `db.Query` 失败 → log warn 并保持 3s 不变。state.db 不存在则空轮询 no-op（与 `OpenCodeDBWatcher.Start()` 行为一致 `opencode_db.go:90-92`）。

**state.db 不存在或被删除：**
- 启动时 `findHermesStateDB()` 返回空路径 → `Start()` no-op（不启动 goroutine），与 OpenCode 一致。
- 运行中文件被删（罕见）：sqlite Open 会失败，poll() 静默 return；下一周期重试。

### 3.3 Session 变化检测 SQL 与判定

**当前活跃 session 的查询**：复用 `hermes_db.go:29` 的现有 SQL：
```sql
SELECT s.id, MAX(m.timestamp) FROM sessions s
  JOIN messages m ON s.id=m.session_id
  GROUP BY s.id
  ORDER BY MAX(m.timestamp) DESC LIMIT 1
```
要把 `MAX(m.timestamp)` 也 SELECT 出来，作为"最新 message 时间戳"，用于在 watcher 内做"同 session 但有新消息"的去重。

**变化判定逻辑**（在 `HermesDBWatcher.poll()` 里）：

| 条件 | 含义 | 处理 |
|---|---|---|
| `latestSessionID == ""` | state.db 还没数据 | no-op |
| `latestSessionID != w.sessionID` | session 切换了（`/clear` 后用户已发新消息） | 触发 `OnSessionSwitch(latestSessionID)` |
| `latestSessionID == w.sessionID && latestTS > w.lastEmittedTS` | 同 session 有新消息 | 加载 (sessionID, lastEmittedTS, latestTS] 范围的消息 → callback emit |
| `latestSessionID == w.sessionID && latestTS == w.lastEmittedTS` | 无变化 | no-op |

**关键边界：`/clear` 后但用户尚未发新消息**——hermes 的行为是 `/clear` 不立即在 state.db 创建新 session row，新 session 是在用户发出第一条消息时才落库（**TODO Manager 拍板**：需要由 dev agent 在实施时 `sqlite3 ~/.hermes/state.db .schema` + 实测 /clear 行为验证）。这意味着我们**只能在用户发出新 session 第一条消息后才感知到切换**，这是物理限制。app 端在那之前看到的旧 session 历史是事实，不是 bug。

**处理顺序**（OnSessionSwitch 内部）必须严格按此顺序：
1. 调 `m.UpdateResumeSessionID(agentID, newSessionID)` —— 后续任何 hermesSend 都用新 ID。
2. 调 `m.ClearConversationEvents(agentID)` 清持久化历史。
3. 调 `ag.EventBuf().Reset()` 清内存 buffer。
4. 调 `watcher.HermesStateDBLoadSession(newSessionID)` 加载新 session 全部历史，逐条 `m.appendAndPersistEvent`。
5. 广播 `conversation.cleared` + `agent.status_changed`（与 `manager.go:2320-2354` 完全相同的代码块）。

理由：步骤 1 必须最先做，否则在步骤 2-4 之间到来的 `hermesSend` 还会用旧 sessionID。步骤 5 必须在 4 之后，让 app 收到 `conversation.cleared` 时 server 端已经准备好新历史，app `conversation.history` re-fetch 一定能拿到正确数据。

### 3.4 历史加载扩展

新增函数（`internal/watcher/hermes_db.go`）：

```go
// HermesStateDBLoadSession loads all conversation events for the specified session ID.
// Returns events in chronological order. Returns empty slice (not error) if session
// doesn't exist or DB is missing, to keep the call site simple.
func HermesStateDBLoadSession(sessionID string) ([]ConversationEvent, error) { ... }
```

实现：等同于 `HermesStateDBHistory` 但跳过"找当前 session"那步、直接按入参 sessionID 查询 messages 表。**保留** `HermesStateDBHistory()` 不动（外部已有 3 处调用方），新函数与之并存。

**旧 session 事件处理**：丢弃（通过 `ClearConversationEvents` + `EventBuf().Reset()`）。理由：
- app 端的"sessions 列表"功能尚未规划，没有"切回旧 session"需求。
- 持久化 store 是 per-agent 的，不是 per-session；保留旧 session 事件会污染同一 agentID 的历史。
- 用户真要回看旧 session，可以直接读 `~/.hermes/state.db`（hermes 自己有 CLI 命令）。

### 3.5 事件广播

**复用现有 `agent.status_changed`**，**不**新增 `agent.session_changed`。

理由：
- `agent.status_changed` 的 params 已经包含 `sessionId` 字段（见 `manager.go:2345-2348`），切换时 sessionId 变化即可表达"session 切换了"语义。
- 引入新事件需要 app 端单独处理，跨端协议变更成本 > 收益。
- 同时广播 `conversation.cleared` 给 app 一个明确信号"快重新拉历史"，已经是 OpenCode 路径的现有约定。

**对 app 端的建议（边界，不深入实现）**：
- 收到 `conversation.cleared` (agentId) → 清空本地 message 列表 → 重新调 `conversation.history` 拉。
- 收到 `agent.status_changed` 时如果 `sessionId` 字段与本地缓存的 sessionId 不同，等价于 `conversation.cleared`，做防御性 history 重拉。

### 3.6 与 chunk.Done 路径的竞态分析

**冲突点**：`hermesSend` 在 `chunk.Done` 时调 `UpdateResumeSessionID(agentID, chunk.SessionID)`（`handler.go:2412`）；同时 `HermesDBWatcher.poll()` 也可能调 `UpdateResumeSessionID`。

**场景分析**：

| 场景 | chunk.Done 写入 | poll 写入 | 结果 |
|---|---|---|---|
| A：正常发消息 | sessionID = X | 看到 sessionID = X，无变化，不写 | OK |
| B：用户在 send 前 `/clear` 后发了新消息（极端，因 send 也是新消息） | sessionID = Y（新） | 可能在 send 完成前先看到 Y → 切换 | poll 先切换会触发完整 reset，chunk.Done 后 UpdateResumeSessionID 是同值幂等。但 reset 会丢掉 send 已发的 user message —— **Bug 风险** |
| C：用户在另一个 hermes CLI 实例 `/clear`（不同 agent 共享 state.db） | 不发生 | 切换 | 这是误检测：本 agent 的 sessionID 不应被另一个 agent 的 /clear 影响 |

**B 的解决**：在 `hermesSend` 进入"发送中"状态时设一个 agent 级标志位 `ag.sending = true`；watcher poll 看到 `ag.sending` 时**跳过 OnSessionSwitch**，但仍处理同 session 内的新消息。`hermesSend` chunk.Done / 错误 / context cancel 都要 reset 标志。这要求在 `Agent` 结构体加一个 `sendingMu sync.Mutex` + `sending bool` 字段（或用 atomic.Bool）。

**C 的解决（关键架构问题）**：
- 现状：单机有多个 hermes CLI 实例时它们共享同一个 `~/.hermes/state.db`（hermes 没有 per-instance state）。
- `HermesStateDBHistory` 当前 SQL 只看"最近活跃 session"，全局唯一，没有 PID/workdir 维度。
- **本计划暂不解决多 hermes CLI 共存场景**。第一版的契约是：phone-talk 只挂一个 hermes agent；如果检测到多个 hermes 进程 attach，state.db 信号会有干扰。要彻底解决需要把 sessionID 与 PID/workdir 关联（hermes 自己得改 schema），属于上游问题。
- 实施层面：在 `HermesDBWatcher` 加一个 `agentID` 字段记日志（`[HermesDB][agent=xxx] session switched ...`）便于调试。

**source of truth 决策**：以 state.db 轮询为**主**，chunk.Done 为**辅**。
- 当 chunk.Done 返回的 sessionID 与 watcher 当前 sessionID **不同**时，相信 chunk.Done（HTTP header 是权威），watcher 在下一轮 poll 也会赶上。
- 当相同时，no-op。
- chunk.Done 调 `UpdateResumeSessionID` 之外，**还需要**通知 watcher 立即同步 `w.sessionID = newSessionID` —— 否则 watcher 下一轮可能误以为又切换了。建议在 `HermesDBWatcher` 上加 `SetSessionID(string)` 方法，`hermesSend` 在 chunk.Done 时调一下：
  ```go
  if hw, ok := ag.Watcher().(*watcher.HermesDBWatcher); ok {
      hw.SetSessionID(chunk.SessionID)
  }
  ```

### 3.7 测试方案

#### M1 单元测试（`internal/watcher/hermes_db_test.go`）

- `TestHermesStateDBLoadSession_HappyPath`：用 `:memory:` 不行（modernc/sqlite 文件 URI 限制），改用 `t.TempDir() + "/state.db"`，CREATE TABLE sessions/messages，INSERT 几条 message，断言 `HermesStateDBLoadSession("s1")` 返回正确顺序的 events。
- `TestHermesStateDBLoadSession_NoSession`：DB 存在但 sessionID 不存在 → 返回空 slice 无 error。
- `TestHermesStateDBLoadSession_NoDB`：DB 路径不存在 → 返回空无 error。
- `TestHermesStateDBHistory_RegressionUnchanged`：现有 (`hermes_db.go:14`) 行为未改。

为绕开 `findHermesStateDB()` 硬编码 `~/.hermes/state.db`，把 path 解析提到 package-level var：`var findHermesStateDBFunc = findHermesStateDB`，测试里替换。

#### M2 单元测试（HermesDBWatcher poll 行为）

- `TestHermesDBWatcher_DetectsSessionSwitch`：往 tmpdb 灌 session A 的 message，启 watcher（注入 path），`onSwitch` 不被调；INSERT session B message，poll 触发 → `onSwitch("B")` 被调一次。
- `TestHermesDBWatcher_EmitsNewMessages`：同 session 内 INSERT message，callback 收到正确 ConversationEvent。
- `TestHermesDBWatcher_NoEmitOnUnchanged`：连续 poll 无新数据，callback 不被调。
- `TestHermesDBWatcher_StopIsIdempotent`：`Stop()` 多次调用不 panic。

#### M4 集成测试（`internal/agent/manager_hermes_session_test.go`）

- 用 `t.TempDir()` 起一个真 sqlite，monkey-patch `watcher.findHermesStateDBFunc` 指向它。
- 模拟：attach hermes agent → INSERT session A 几条 → 等 watcher poll → 断言 EventBuf 有数据。
- INSERT session B 一条 → 等 poll → 断言：
  - `m.GetResumeSessionID(id)` 返回 B。
  - EventBuf 只有 B 的 message（A 已被 clear）。
  - `onOutput` 收到 `conversation.cleared`。
  - `onStatusChange` 收到带 `sessionId=B` 的 `agent.status_changed`。

#### 回归

- `TestUpdateResumeSessionID` (`manager_test.go:206`) 三处不应受影响。
- `internal/store/store_test.go` 不受影响。
- `TestOpenCodeDBHistoryWithReasoning` 模板不受影响。

### 3.8 风险与回滚

| 风险 | 缓解 |
|---|---|
| state.db 并发读取与 hermes 写入竞争 | `?mode=ro&_journal_mode=WAL&_busy_timeout=3000` 已经够 —— 与 OpenCodeDBWatcher 多年验证一致 |
| 多 hermes 进程共存时误检测（场景 C） | 第一版不解决，doc 中明确"建议单 hermes agent 运行"；后续依赖 hermes 自己改 schema |
| 轮询误报：watcher 错把 chunk.Done 刚写完的同一 session 当成切换 | `SetSessionID(newID)` 在 chunk.Done 时同步，配合"poll 看 sending=true 跳过 switch"双保险 |
| watcher goroutine 泄漏 | `Remove(id)` 中显式 `Stop()`；agent.go 加 finalizer 兜底（可选） |
| 多个 hermes agent 都查同一 state.db 浪费 IO | 3s 周期 × N agents：实测 N<10 时 < 1% CPU；超过则优化为 process_manager 共享 db handle（M3 后再考虑） |
| 历史加载在切换时阻塞 watcher | `OnSessionSwitch` 回调里同步加载几十条 message 是亚毫秒级；如未来 hermes 单 session 上千条，把加载移到独立 goroutine（M5+ 优化） |

**回滚预案**：本计划的 5 个 M 都通过 `internal/watcher/hermes_db.go` 与 `manager.go` 的 hermes 分支隔离，单 commit 可 revert。如果观测到误切换爆量发生，把 watcher 启动的三处调用注释掉即可回到现状。

## 4. 里程碑分解

### M1：`HermesStateDBLoadSession` 函数 + 单测

文件：`internal/watcher/hermes_db.go`（新增函数，不动现有）+ 新文件 `internal/watcher/hermes_db_test.go`。

DoD：
- `HermesStateDBLoadSession(sessionID) ([]ConversationEvent, error)` 实现。
- 单测覆盖 happy path / 不存在 session / 不存在 DB。
- `findHermesStateDB` 提取为可注入 var（`findHermesStateDBFunc`）。
- 不动 `HermesStateDBHistory`。

### M2：HermesDBWatcher 骨架 + 启停 + 单测

文件：`internal/watcher/hermes_db.go`（追加新类型）+ `internal/watcher/hermes_db_test.go`（追加用例）。

DoD：
- `HermesDBWatcher` 实现 `SessionWatcher` 接口（Start/Stop/SetSkipExisting）。
- 字段：dbPath、sessionID、callback、onSwitch、stop、once、lastTS、agentID（仅日志用）。
- 方法：`SetSessionID(string)`、`OnSessionSwitch(func(string))`。
- 3s ticker loop；poll 内做 session 比对 + 同 session 内增量加载。
- 退出时 `Stop()` 关闭 chan；幂等。
- 单测：M2 列出的 4 个 case 通过。

### M3：集成到 Manager（attach / re-attach / load-from-store 启停）

文件：`internal/agent/manager.go`（hermes 分支三处）。

DoD：
- `manager.go:2293-2356` `newSessionWatcher` 增加 `if provider == "hermes"` 分支，构造 `HermesDBWatcher` + 配置 `OnSessionSwitch` 回调（复用 OpenCode 的回调实现，几乎是逐字 copy）。
- `manager.go:2064-2097` 新 hermes attach 末尾启动 watcher。
- `manager.go:1994-2057` 重 attach 路径的 hermes 分支启动 watcher（与 OpenCode 重 attach 一致）。
- `manager.go:223-231` LoadFromStore 的 hermes 分支启动 watcher。
- `manager.go:1706` Remove() 中显式 `if w := ag.Watcher(); w != nil { w.Stop() }`（同时让 OpenCode/Claude 受益的修复）。
- 不破坏现有的 watcher 测试。

### M4：会话切换处理 + chunk.Done 协同 + 集成测试

文件：`internal/agent/agent.go`（加 sending 标志），`internal/ws/handler.go`（chunk.Done 同步通知 watcher），`internal/agent/manager_hermes_session_test.go`（新）。

DoD：
- `Agent` 加 `sendingFlag atomic.Bool` 字段 + `BeginSend()`/`EndSend()`/`IsSending()`。
- `hermesSend` (`handler.go:2313`) 进入时 BeginSend，return 时 defer EndSend。
- `hermesSend` 在 chunk.Done 时调 `hw.SetSessionID(chunk.SessionID)`（类型断言 watcher 是否 hermes）。
- `HermesDBWatcher.poll` 在做 OnSessionSwitch 前检查 `IsSending()` → 跳过本轮切换。
- 集成测试覆盖完整切换流程，断言 conversation.cleared / agent.status_changed / GetResumeSessionID。

### M5：app 端事件处理（独立任务）

**不在本计划范围**。本计划只交付边界：
- agentd 在 session 切换时广播 `conversation.cleared` + `agent.status_changed`（含新 sessionId）。
- app 端任务（建议）：收到任一事件 → 重拉 `conversation.history`。
- 在 `docs/plans/p-hermes-app-session-handling.md`（占位文件名）单独立项。

## 5. 阻碍 / 已识别的痛点

1. **`HermesStateDBHistory()` 全局取最近 session** 的语义在多 hermes agent 共存时不准。本计划保留它仅供 LoadFromStore/Attach 首次灌历史，运行期改用 `HermesStateDBLoadSession(sessionID)`。
2. **`Manager.Remove()` 不停 watcher** 是 OpenCode/Claude 也存在的潜在泄漏（M3 顺手修）。
3. **scanner.go:159-160 注释**「Hermes via HTTP does not need tmux/PTY」**保持不变**——本计划不改 attach 路由。
4. **hermes state.db schema 假设**（sessions / messages 表，timestamp 是字符串）：现有 `HermesStateDBHistory` 已隐式依赖；M1 实施时 dev agent 用 `sqlite3 ~/.hermes/state.db .schema` 复核，若 schema 不同步处理。

## 6. 悬而未决，需要 Manager 拍板

1. **`/clear` 之后无新消息时是否也要感知？** 物理上做不到（state.db 没新行），建议接受这一限制。如 Manager 坚持要立即感知，唯一路径是把 `/clear` 改成 hermes CLI 通过 IPC 通知 agentd（属于 hermes 上游改动，超出本计划）。
2. **多 hermes agent 共存场景**是否在第一版打补丁？建议 NO，第一版仅承诺单 hermes agent；多 agent 在文档明确"未支持"。
3. **轮询周期 3s 是否需要可配置？** 建议 NO（YAGNI），需要时再加 env var `HERMES_POLL_INTERVAL_MS`。
4. **`agent.session_changed` 新事件**是否真的不要？建议 NO（用 `agent.status_changed` 携带 sessionId 已足够），但如果 Manager 觉得未来 app 端要在不重拉 history 的情况下做"session 切换 toast"，那需要新事件。

## 7. 下一步

Manager 评审通过后：
- 把本文档分配给 dev agent 按 M1→M4 顺序实施（M5 单独立项）。
- M1+M2 可一个 PR 合（纯 watcher 包，无 manager 改动）。
- M3+M4 一个 PR（manager 改动 + 集成测试）。
- 实施期间每个 M 完成后回到 Manager 短 review，再进入下一个。
