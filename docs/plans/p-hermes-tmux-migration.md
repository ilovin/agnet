# P: Hermes provider — HTTP→tmux send-keys 迁移

## 0. 背景与动机

`docs/plans/p-hermes-state-db-polling.md` 已实施落地（commit `cfe031e`、`afa9890`）：HermesDBWatcher (`agentd/internal/watcher/hermes_db.go:147-404`) 已能轮询 `~/.hermes/state.db`，OpenCode 模板的 `OnSessionSwitch` 回调 (`agentd/internal/agent/manager.go:2327-2380`) 也已为 hermes 复用。

但用户实测后确认 HTTP 发送路径仍存在 4 类无法在 HTTP 框架内修复的缺陷：

1. **并行上下文割裂**：app 走 HTTP `/v1/chat/completions`，CLI 用户在同一个 hermes session 里直接对话。两条流写同一份 `state.db`，但响应在两端不互通——CLI 用户看不到 app 发的 prompt 和回答；反之亦然。
2. **多 CLI 实例无法定位**：HTTP 永远打到唯一 gateway endpoint，没有"路由到某个 PID 的 CLI"的能力。
3. **CLI 内部命令无法触发**：`/clear`、`/help`、`/model` 这类 CLI slash 命令只对 stdin 有效，HTTP chat completion 完全发不出去。
4. **Auth 双轨**：HTTP 要求 `HERMES_API_KEY`，但用户 CLI 已是登录态，需要双份凭据维护。

**终态目标**：app 是 hermes CLI 的远程视图——发送走 `tmux send-keys` 写进同一个 CLI 进程的 stdin，响应通过 `state.db` 轮询（已就绪）+ 必要时辅以 pane scrape。从架构上与 Claude tmux attach 模式 (`agentd/internal/agent/agent.go:333-412`) 同构。

本计划**不**涉及：
- agentd 主动 spawn hermes 进程（继续依赖用户 CLI 已经在 tmux 中跑起来）。
- gateway HTTP 服务的存废（可继续作为 hermes CLI 后端的一部分；与 agentd 解耦）。
- contentMatch、scanner 性能等无关优化。

## 1. 现状梳理

### 1.1 hermes CLI 实际运行方式

- **TODO 需要用户/dev agent 实测**：`ssh oracle "tmux list-panes -a -F '#{pane_pid} #{pane_tty} #{session_name}:#{window_index}.#{pane_index} #{pane_current_command}'" | grep -i hermes`，确认：
  1. hermes CLI 是否始终在 tmux pane 里？还是有用户在裸 PTY (`ssh` 直接跑 `hermes`) 中运行？
  2. 如果在 tmux 中：pane_pid 是 hermes 还是 shell？是否需要走 `pane_current_command` 而不是 `pane_pid` 来识别？
  3. 是否存在多个 hermes CLI 共存的 case（场景 C）？
- **scanner 现状** (`agentd/internal/scanner/scanner_linux.go:80-105`)：tmuxTarget 是从 `terminal` 反查 tmux pane list 得到的，与 provider 无关。**这意味着如果 hermes CLI 运行在 tmux 里，`ProcessInfo.TmuxTarget` 已经被填上**——只是 `AttachReadOnlyReason()` (`scanner.go:159-161`) 显式短路返回 `""`，使 hermes 当前永远走 `AttachModeTmux` 但同时被特判走 HTTP 路径。
- **倒置的注释**：`scanner.go:159-160` "Hermes via HTTP does not need tmux/PTY" 假设是 HTTP 时代的产物，迁移后需要反过来：hermes via tmux **需要** TmuxTarget；缺失则降级（详见 §6）。

### 1.2 当前 hermes Attach 创建的 agent

`agentd/internal/agent/manager.go:2080-2127` 的 hermes Attach 分支创建的 agent：
- `ag.PID = info.PID`（hermes CLI 的 PID，存在）。
- `ag.SetAttachInputRoute(info.AttachMode(), info.AttachReadOnly(), info.AttachReadOnlyReason(), info.TmuxTarget)` 设置了 `attachMode="tmux"` + `attachReadOnly=false` + `tmuxTarget="<sess>:<win>.<pane>"`（**已就绪**——`AttachReadOnlyReason()` 对 hermes 返回 `""` 即 readOnly=false）。
- `ag.process == nil`、`ag.w` 在末尾被赋为 `HermesDBWatcher`。

**关键观察**：`Agent.WriteInput()` (`agent.go:298-331`) 的逻辑是：`process != nil` → 写 PTY；否则 `attachMode=="tmux" && tmuxTarget != ""` → 调 `sendTmuxInput()`。也就是说，**hermes agent 现在已经可以走 tmux send-keys 路径**，唯一的拦截器是 `handler.go:712-720` 显式提前 return 走 `hermesSend` HTTP 路径。删掉这个早 return，发送就会自动落到 tmux 分支 (`handler.go:723-760`)。

### 1.3 当前 hermesSend 的副作用

`agentd/internal/ws/handler.go:2311-2485`：
- `BeginSend`/`EndSend` (line 2319-2320) 为 watcher 与 chunk.Done 协同所需。
- 调 `client.ChatCompletion()`（line 2369）→ 收 SSE 流 → broadcast `conversation.message` partial/final → `UpdateResumeSessionID(chunk.SessionID)` 同步 session id（line 2418）→ 同步 watcher (line 2423)。
- 用户消息直接 `RecordConversationEvent` 落盘 + broadcast。

迁移后需要替代它的 source：
- **session id 同步**：原本靠 chunk.Done header；新路径**完全依赖 HermesDBWatcher 轮询** (`hermes_db.go:280-318`)。`SetSendingChecker` 抑制窗口必须移除或重新设计（见 §4）。
- **partial 流**：原本靠 SSE 100ms 批量；新路径需要从 state.db 增量轮询 + 现有 `HermesDBWatcher.poll()` 的同 session 内 `timestamp > lastTS` 增量逻辑 (`hermes_db.go:340-381`)。**问题**：state.db 是 hermes 写完整 message 才落库，没有逐 token 增量——partial 在 state.db 路径下基本不可得（详见 §4）。
- **完成通知**：原本靠 SSE `[DONE]`；新路径需要从 state.db 看到 assistant role 的 message 写入即视为完成（**TODO 需实测**：hermes 写 assistant message 是流式 INSERT/UPDATE 还是一次性？看 schema 是否有 streaming 字段）。

## 2. 目标架构

### 2.1 同构 Claude tmux attach 模式

迁移后 hermes agent 与 Claude tmux-attached agent 在 `Agent` 层完全同构。`process=nil` + `attachMode="tmux"` + `tmuxTarget=sess:win.pane`，发送走 `WriteInput → sendTmuxInput`，与 Claude/OpenCode tmux 同源代码。

agentd **不**主动启动 hermes（与 Claude attach 路径一致）。`ResolveLaunch` (`agent_service.go:78-80`) 的 `hermes -> hermes gateway run` 在迁移后基本无用，可保留作为 fallback 或删除（建议删除，见 §10）。

### 2.2 CLI 必须在 tmux 中

迁移后 hermes 用户必须在 tmux 中跑 CLI。文档需明确这一约束：

> Phone-Talk attach 到一个 hermes CLI，要求 CLI 运行在 tmux pane 内（与 Claude attach 模式相同）。裸 PTY 中运行的 hermes 将变为 read-only attach（仅展示 state.db 历史，无法发送）。

### 2.3 send 路径

直接复用 `Agent.WriteInput()` → `sendTmuxInput()` → `tmux send-keys -l <text>` + `Enter`（`agent.go:333-412`）。

**TODO 需实测**：hermes CLI 是否像 Claude 一样把多行 prompt 用 `\` 续行？还是单 Enter 即提交？默认按 Claude 模式：`message + "\n"`。

### 2.4 响应路径

- **历史权威**：`~/.hermes/state.db`。
- **实时增量**：`HermesDBWatcher.poll()` 已实现（3s 周期）。
- **partial 流不可得**：state.db 是完整 message 粒度。第一版接受 3s 延迟（与 OpenCode 同档）。

## 3. send 路径迁移设计

### 3.1 改动点

`agentd/internal/ws/handler.go:712-720` 的 hermes 早 return **删除**。删除后，hermes agent 自然落入 `handler.go:723-760` 的通用 tmux 分支。

`handler.go:701` 的 `&& ag.Provider != "hermes"` 特例可去掉。

### 3.2 多行处理

第一版与 Claude tmux 路径完全一致：末尾追加单个 `\n`。`raw=true` 则照原样发送。

### 3.3 BeginSend / EndSend 与 watcher 协同

保留 `BeginSend`/`EndSend` + `SetSendingChecker`：window 缩到只覆盖 send-keys 命令本身（毫秒级）。watcher 的 SetSendingChecker 实质退化为防御性代码。

### 3.4 错误处理

| 失败模式 | 处理 |
|---|---|
| tmux 不在 PATH | scanner 启动时 lookpath 失败警告 |
| pane 死了 | send-keys 报 "can't find pane" → -32000 |
| hermes CLI crash 后 send-keys 污染 shell | M4: send-keys 前 `tmux display-message -p -t <target> '#{pane_current_command}'` 检查仍是 hermes |
| state.db 写入失败 | watcher 看不到新消息，3s 后 app 显示无响应 |

## 4. 历史 + 实时事件 source of truth

| 数据 | 主 source | 辅助 |
|---|---|---|
| 历史（attach/restart） | `state.db` via `HermesStateDBHistory()` | 无 |
| Session 切换 | `HermesDBWatcher.poll()` | 无 |
| 同 session 新 message | `HermesDBWatcher.poll()` 增量 | （可选）pane scrape，第一版不做 |
| Assistant partial | **不可得** | — |

`OnSessionSwitch` 链路保留不动。

## 5. HTTP 客户端的处置

**建议：M5 删除 `internal/hermesclient/`、`Manager.HermesClient()`、`hermesSend`**。理由见 §0 + §1.3，HTTP 4 类核心缺陷无法在 HTTP 框架内修复。

删除范围：
- `internal/hermesclient/{client,sse,client_test}.go` 整个包
- `manager.go:80, 125-138` HermesClient 字段与方法
- `handler.go:2311-2485 hermesSend()`、`handler.go:712-720 早 return`
- `agent_service.go:78-80 ResolveLaunch hermes 分支`
- 保留：`Agent.BeginSend()` / `EndSend()` / `IsSending()` 与 watcher SetSendingChecker（防御性）

**保留 fallback 反方案不推荐**——env var 双轨永远不会被删，配置漂移导致两台机器行为不同。

## 6. 兼容性 & scanner 改动

### 6.1 scanner.go:159-161 改写

```go
case "hermes":
    if p.TmuxTarget == "" {
        return "no tmux pane found for hermes process; attach is observe-only (state.db history available)"
    }
    return ""
```

### 6.2 AttachMode 设定

不引入新 mode `"hermes-tmux"`。复用 `AttachModeTmux`：有 tmux → tmux 路径；无 tmux → readOnly。

### 6.3 LoadFromStore 重算 attach metadata

`manager.go:223-241` LoadFromStore 当前对 hermes agent 的 `attachMode`/`tmuxTarget` 没有重新计算（持久化 AgentRecord 没保存这些字段）。**改动**：在 hermes 分支末尾重新 scan PID 是否仍在 tmux：

```go
if r.PID > 0 && isProcessRunning(r.PID, "hermes") {
    procs, _ := m.ScanExisting()
    for _, p := range procs {
        if p.PID == r.PID && p.Provider == "hermes" {
            ag.SetAttachInputRoute(p.AttachMode(), p.AttachReadOnly(),
                p.AttachReadOnlyReason(), p.TmuxTarget)
            break
        }
    }
}
```

## 7. 测试方案

### 7.1 单元测试
- `TestWriteInput_HermesTmux`：构造 attachMode=tmux + tmuxTarget hermes agent + sendTmuxLiteral mock → 断言 args
- `TestAttachReadOnlyReason_HermesNoTmux` / `_HermesTmux`：无/有 TmuxTarget 各自断言

### 7.2 集成测试

`agentd/internal/agent/manager_hermes_tmux_test.go`：
- `tmux new-session -d -s hermes-it 'cat'`（cat 模拟 stdin）
- 注入 findHermesStateDBFunc 指 tmpdir sqlite
- 模拟 attach、`agent.WriteInput("hello\n")`、`tmux capture-pane` 验证
- INSERT state.db assistant → 等 watcher poll → 断言 EventBuf 收到

### 7.3 端到端（用户在 oracle 实测）

1. ssh oracle, `tmux new -s hermes-test`, 跑 `hermes` CLI
2. app 选 hermes agent
3. app 发送 → oracle pane 显示 + hermes 自然回复 → app 在 3s 内看到 assistant message
4. ssh oracle 直接在 hermes CLI 输入 "/clear"，再发 "test" → app 收到 `conversation.cleared` 并刷新历史
5. kill hermes 进程 → app 看到 stopped 状态

## 8. 里程碑分解

每个 M 都可独立验证 + 独立 commit。**M5 等 M1-M4 在 oracle 上稳定 1 周以上再做**。

### M1：scanner 反转 + agent 降级路径（无破坏性）
- 文件：`scanner.go` + 单测
- DoD：单测过；线上 hermes 行为零变化（HTTP 早 return 还在）

### M2：LoadFromStore 重算 attach metadata
- 文件：`manager.go:223-241`
- DoD：重启 agentd 后日志看到 hermes agent tmuxTarget 回填；HTTP 行为零变化

### M3：开 tmux 路径 + 关 HTTP 早 return（核心切换）
- 文件：`handler.go:712-720`（删除）+ `:701`（去掉特判）
- `hermesSend` 函数暂保留不删
- 上线前 oracle 充分实测；准备 1 commit revert
- DoD：oracle hermes 走 tmux 路径、并行上下文同步、`/clear` 端到端工作

### M4：BeginSend window 调整 + 错误处理强化
- 文件：`handler.go`、`agent.go`
- send-keys 前 `pane_current_command` 验活检查、watcher PID 验活
- DoD：hermes crash 后 pane 不污染 shell；agent 状态正确

### M5：删除 HTTP 路径与 hermesclient 包
- 见 §5.2 删除清单
- DoD：build 通过、引用清理、端到端测试 §7.3 全过

### M6（可选）：tmux pane scrape partial 流
仅当 §4 决定上 partial 时启动。**当前不规划**。

## 9. 风险 & 回滚

| # | 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|---|
| 1 | tmux 不在 PATH | 低 | 发送全失败 | scanner 启动时 lookpath 失败警告 |
| 2 | hermes CLI 不在 tmux 中 | 中 | attach 后只能看历史 | M1 降级路径 |
| 3 | hermes crash 后 send-keys 污染 shell | 中 | 安全/可见事故 | M4 验活 |
| 4 | `\n` 在 hermes 中被解释为多次提交 | 中 | 用户消息错乱 | §3.2 实测；必要时 app 端折叠 |
| 5 | tmux scroll buffer 超限 | 低 | 历史不全 | state.db 是权威，不依赖 capture-pane |
| 6 | state.db 写入与 send-keys 时序竞态 | 低 | 短暂消息丢失/重复 | watcher timestamp 严格单调 |
| 7 | 多 hermes CLI 实例共存时误检测 | 低 | 历史被错误清空 | 单实例约束文档化 |
| 8 | M3 上线后端到端不可用 | 中 | 全员 hermes 用户挂掉 | M3 revert commit 准备 |
| 9 | 首次 attach state.db 还无 message → 无 sessionID | 中 | resume id 缺失 | watcher emit 第一条 user message 时同步设 sessionID |
| 10 | 大消息超 tmux 命令行长度限制 | 中 | 大消息发送失败 | sendTmuxLiteral 分块（M4） |

**整体回滚预案**：每个 M 单独 git revert。M5 之前都可立即恢复 HTTP 路径。

## 10. 悬而未决，需要 Manager 拍板

> **2026-05-25 Manager + User 确认结论（覆盖原推荐）**：
> 1. **hermes CLI 必须在 tmux 中** ✅ 确认
> 2. **state.db 唯一 source of truth，不做 pane scrape** ✅ 确认
> 3. **M5 彻底删除 HTTP fallback**（M3+M4 稳定 1 周后）✅ 确认
> 4. **send-keys 失败不自动重试** ✅ 确认
> 5. **不新增 `agent.tmux_lost` 事件**，复用 `agent.status_changed → stopped` ✅ 确认

1. **hermes CLI 是否必须在 tmux 中？** → 推荐 YES，与 Claude 同构
2. **state.db 仍是 source of truth？还是 pane scrape？** → 推荐 state.db 唯一；不做 pane scrape
3. **保留 HTTP fallback 还是彻底删？** → 推荐 M5 删除（M3+M4 稳定 1 周后）
4. **send-keys 失败时是否自动重试？** → 推荐 NO
5. **是否需要新事件 `agent.tmux_lost`？** → 推荐 NO，复用 `agent.status_changed`

## 11. 实测先决条件 (TODO 表)

| TODO | 信息源 | 影响 M |
|---|---|---|
| hermes CLI 在 oracle 上是否始终运行在 tmux 中？ | ssh oracle + tmux list-panes | M3 |
| hermes 是否将 `\n` 视为 submit？ | `printf 'a\nb\n' \| hermes` 实测 | M3 §3.2 |
| hermes CLI crash 后 pane 行为？ | 实测 | M4 §3.4 |
| `tmux display-message -p '#{pane_current_command}'` 兼容性 | 实测 | M4 |
| state.db schema：assistant message timestamp 流式？ | sqlite3 .schema + 长 prompt 观察 | §1.3 |
| 多 hermes CLI 实例并发性 | ps -ef 长期采样 | §10 |

## 12. 下一步

1. Manager 评审本文档，对 §10 的 5 个开放问题给答案
2. dev agent 完成 §11 TODO 实测调研
3. 按 M1→M5 顺序执行；M3 上线后观察 1 周再启动 M5
4. M5 完成后归档 `docs/plans/p-hermes-state-db-polling.md` 与本文档为已实施
