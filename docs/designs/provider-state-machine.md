# Provider / cc-switch 状态共享与 Session 状态机设计

**日期**：2026-04-15
**状态**：草稿
**类型**：可直接映射到当前代码的增量设计

---

## 1. 背景

当前仓库里已经有一套可运行的 Agent 状态与 Provider 切换实现，但状态语义分散在多个层面：

- Agent 运行态：`created / starting / idle / working / stopped / crashed`
  - 定义：`agentd/internal/agent/agent.go:15`
- Session 连续性：`resume_session_id`
  - 存储：`agentd/internal/store/store.go:53`
- Provider 当前选择：分散在
  - `~/.claude/settings.json`
  - `~/.cc-switch/cc-switch.db`
  - `~/.cc-switch/settings.json`
  - 切换逻辑：`agentd/internal/ws/handler.go:1624`

当前实现能工作，但存在两个问题：

1. **`idle` 语义过载**
   - 既可能表示“进程活着但暂时空闲”
   - 也可能表示“Claude -p 已退出，但 session 可继续 resume”
   - 还可能表示“watcher 已挂上，但当前无输出”

2. **Provider 状态是多副本同步，不是显式状态模型**
   - `provider.switch` 会同时写三处状态
   - `provider.list` 优先从 Claude runtime settings 反推当前 provider，失败才退回 DB 标记
   - 读取逻辑：`agentd/internal/ws/handler.go:1561`

本设计的目标不是重写现有架构，而是在**保持当前代码可运行、尽量兼容前端**的前提下，把“运行态 / Session 态 / Provider 同步态”显式建模出来。

---

## 2. 设计目标

### 2.1 目标

- 保留当前 `agent.status` 语义，避免一次性打断现有 UI
- 在现有实现上增加**可推导、可展示、可调试**的状态字段
- 明确 Provider 与 cc-switch 的状态共享模型
- 明确 spawned agent 与 attached agent 在 provider switch / session continuity 上的差异
- 让后续前端可以直接显示：
  - 当前是否正在工作
  - 当前 session 是否可恢复
  - 当前 provider 是否与 runtime 一致

### 2.2 非目标

- 本阶段不重构为单一持久化源
- 本阶段不改变现有 `provider.switch` 的基础行为
- 本阶段不替换现有 watcher 机制
- 本阶段不删除现有 `status` 字段

---

## 3. 当前实现归纳

### 3.1 Agent 运行态

当前 Agent 状态定义：

```text
created / starting / idle / working / stopped / crashed
```

关键实现：

- 状态定义：`agentd/internal/agent/agent.go:15`
- 启动成功后 `idle`：`agentd/internal/agent/manager.go:1145`
- 启动失败后 `crashed`：`agentd/internal/agent/manager.go:1140`
- stop 后 `stopped`：`agentd/internal/agent/manager.go:1471`
- watcher 事件驱动 `working/idle`：`agentd/internal/agent/manager.go:1750`
- Claude `-p` 进程正常退出后仍保持 `idle`：`agentd/internal/agent/manager.go:800`

### 3.2 Session 连续性

当前连续会话依赖 `resume_session_id`：

- 字段：`agentd/internal/store/store.go:18`
- 更新：`agentd/internal/store/store.go:113`
- parent/child session 传递：`agentd/internal/agent/manager.go:1324`
- 启动 watcher 时读取：`agentd/internal/agent/manager.go:1386`

当前 watcher 路径：

- Claude：JSONL watcher
- OpenCode：DB watcher
- 分流实现：`agentd/internal/agent/manager.go:1778`

### 3.3 Provider / cc-switch 共享状态

当前 `provider.switch`：

1. 从 `cc-switch.db` 读取 provider 配置
2. merge 到 `~/.claude/settings.json`
3. 更新 `cc-switch.db.providers.is_current`
4. 更新 `~/.cc-switch/settings.json.currentProviderClaude`
5. 如果是 spawned agent，则 restart in place；如果是 attached agent，则不重启

关键实现：

- `agentd/internal/ws/handler.go:1624`
- `agentd/internal/ws/handler.go:1676`
- `agentd/internal/ws/handler.go:1703`
- `agentd/internal/ws/handler.go:1710`
- attached/spawned 分支：`agentd/internal/ws/handler.go:1734`

当前 `provider.list`：

- 优先根据 `~/.claude/settings.json` 的 env 匹配 DB 配置
- 匹配失败再 fallback 到 `is_current`
- 实现：`agentd/internal/ws/handler.go:1579`

### 3.4 Team 模式（sub-agent）约束

当前仓库其实已经隐含考虑了 team mode / sub-agent，只是还没有把它上升为显式状态模型。

现有代码证据：

- Darwin scanner 会过滤 `claude -p` 子进程，不把它们当作独立 session
  - `agentd/internal/scanner/scanner_darwin.go:79`
- `Attach()` 明确避免仅按 projectName 复用 watcher，因为这会让 team-mode child hijack parent session watcher
  - `agentd/internal/agent/manager.go:1624`
- 周期扫描会跳过与已跟踪 `sessionID` 重叠的进程，因为 sub-agent 与 parent 共享同一个 session 文件
  - `agentd/internal/agent/manager.go:1896`
- `sessionParents` 已支持 child -> parent 的 session continuity 传播
  - `agentd/internal/agent/manager.go:1324`
  - `agentd/internal/agent/manager.go:1365`

这说明当前实现对 team mode 的隐式约束其实是：

1. **team child 不是独立 session 真源**
   - 它通常共享 parent 的 Claude session / session file
2. **team child 不应单独拥有一份 provider 当前态**
   - 否则会把同一底层 runtime 错误建模成多个 provider domain
3. **provider switch 的实际生效边界应按 root session / root runtime 理解**
   - 而不是按每个 child agent 单独理解

因此 Provider / cc-switch 状态共享设计如果不考虑 team mode，就会把“同一根会话上的多个子代理”误判成多份可独立切换的 provider 状态。

---

## 4. 可直接落地的状态模型

本设计采用**增量建模**：

- 保留现有 `status`
- 新增两个派生状态：
  - `sessionState`
  - `providerState`
- 如有必要，再补一个简化的 `runtimeState`

### 4.1 保留字段：`status`

继续保留当前字段，兼容现有前端：

```json
{
  "status": "starting | idle | working | stopped | crashed"
}
```

这个字段继续回答“**Agent 当前在不在忙 / 死没死**”这个问题。

### 4.2 新增字段：`sessionState`

先明确：Session 生命周期状态机主要不是回答“进程现在忙不忙”，而是回答下面 3 个问题：

1. **这个 session 是谁？**
   - 它的 provider 是什么
   - 它的 sessionId 是什么
   - 它来自 live process、历史文件，还是已经被 agentd 接管
2. **它现在是不是 live？**
   - 还有没有活着的底层进程
   - watcher 当前是不是挂得上
   - 当前看到的是实时会话还是历史残留
3. **它能不能接管？**
   - 能否 attach 到 live process
   - 能否只通过 sessionId 做 rebind / resume
   - 是否只能只读观察

因此这里建议把 Session 设计拆成两层：

- **Session 生命周期阶段**：discover → judge → attach/rebind
- **Session 展示状态**：live / history / stale 等

建议新增：

```json
{
  "sessionState": "none | standby | active | resumable | missing | broken",
  "sessionStateReason": "..."
}
```

定义如下：

| 值 | 含义 | 直接映射当前代码的判断方式 |
|---|---|---|
| `none` | 没有 session，上下文不可恢复 | `resume_session_id == ""` |
| `active` | 当前会话正在产出内容 | `status == "working"` |
| `standby` | 当前会话空闲，但 watcher 正常，后续可继续使用 | `status == "idle"` 且 watcher 已挂上 |
| `resumable` | 当前进程不在运行，但有可继续的 session | `status == "idle"` 且 process 已退出且 `resume_session_id != ""` |
| `missing` | 记录了 sessionId，但底层 session 文件 / DB 记录找不到 | `resume_session_id != ""` 但 `StartWatcherForAgent` 无法定位 session |
| `broken` | watcher / resume / 解析出现异常 | watcher 启动失败或 resume 失败 |

#### 4.2.1 Session 生命周期阶段

建议补充一个面向 catalog / attach / rebind 的生命周期阶段字段，哪怕第一阶段先不对外暴露，也要在设计里明确：

```text
discovered → judged → attachable | resumable | history-only | stale → attached/rebound
```

各阶段含义：

| 阶段 | 含义 | 当前代码对应位置 |
|---|---|---|
| `discovered` | 发现了一个 session 线索，可能来自 live process、session file、OpenCode DB、managed agent | `session.catalog` 聚合多来源：`agentd/internal/ws/handler.go:409` |
| `judged` | 对发现结果做归一化判定：是谁、是不是 live、能不能接管 | `catalogSessionID` / `filterLiveClaudeFileSessions` / attach 元数据：`agentd/internal/ws/handler.go:350`, `agentd/internal/ws/handler.go:375`, `agentd/internal/scanner/scanner.go:77` |
| `attachable` | 有 live process，可直接 attach 或 observer attach | `agent.scan` + `ProcessInfo.AttachMode()`：`agentd/internal/scanner/scanner.go:77` |
| `resumable` | live process 不一定还在，但有稳定 sessionId，可重新绑定 | `session.attach` 允许按 `sessionId` 创建/恢复：`agentd/internal/ws/handler.go:528` |
| `history-only` | 只有历史记录可看，不保证可继续写入 | `conversation.history` / OpenCode DB history：`agentd/internal/agent/manager.go:1694` |
| `stale` | 看到的是旧残留，不能确认 live，也不能安全接管 | 例如 session 文件残留、PID 已复用、watcher 无法启动 |
| `attached` | 已被 agentd 管理并建立 watcher | `Manager.Attach` / `StartWatcherForAgent` |
| `rebound` | 原 session 通过 sessionId 被重新绑定到新的 managed agent 或旧 agent watcher | `session.attach` 走 `agentId/sessionId` 分支：`agentd/internal/ws/handler.go:504` |

#### 4.2.2 Session 三个核心判定维度

为回答“这个 session 是谁？是不是 live？能不能接管？”，建议在设计上显式区分 3 个维度，而不是只靠一个 `sessionState`：

```json
{
  "sessionIdentity": {
    "provider": "claude|opencode",
    "sessionId": "...",
    "source": "managed|live_process|history_file|history_db"
  },
  "sessionLiveness": "live|history|stale|unknown",
  "sessionControl": "managed|attachable|rebindable|read_only|unavailable"
}
```

##### (1) SessionIdentity：这个 session 是谁？

最小字段：
- `provider`
- `sessionId`
- `source`
- 可选 `workDir` / `projectName`

当前代码基础：
- `session.catalog` 已在聚合 `managed / attachable / opencodeFiles / claudeFiles`
- `catalogSessionID()` 已在统一提取 session 主键
  - `agentd/internal/ws/handler.go:350`

##### (2) SessionLiveness：它是不是 live？

建议值：

| 值 | 含义 |
|---|---|
| `live` | 底层进程仍在线，或 managed watcher 正在跟 live source 绑定 |
| `history` | 当前只能确认有历史，不确认底层 live process 还在 |
| `stale` | 很可能是残留：session 文件还在，但 live process 不在，且无法安全重绑 |
| `unknown` | 信息不足，暂不能判断 |

这部分正好对应用户提出的：
- `live`
- `history`
- `stale`

##### (3) SessionControl：它能不能接管？

建议值：

| 值 | 含义 |
|---|---|
| `managed` | 已经被 agentd 接管，直接继续使用即可 |
| `attachable` | 有 live process，可 attach |
| `rebindable` | 无需 live process，只要 sessionId 仍有效就能 rebind / resume |
| `read_only` | 只能观察，不能安全写入 |
| `unavailable` | 既不能 attach，也不能 rebind |

这部分对应：
- `attach`
- `rebind`
- “能不能接管”

#### 4.2.3 面向 team mode 的 session 归属规则

如果把 team child 也纳入这个模型，建议补一个归属约束：

```json
{
  "sessionOwnership": "root | child | standalone"
}
```

定义：

| 值 | 含义 |
|---|---|
| `root` | 这是一个根会话 / 根 runtime，对 provider state 与 session continuity 负责 |
| `child` | 这是 team mode 派生出来的子代理，共享 root session，不单独声明 provider 当前态 |
| `standalone` | 与 team 无关的普通独立 agent |

第一阶段即使不把这个字段对外暴露，也建议在设计里明确它的判断原则：

- 如果发现该进程只是 Claude team mode 生成的 sub-agent，则 `sessionOwnership=child`
- 如果当前 agent 被其他 child 通过 `sessionParents` 指向，则它是 `root`
- 其他普通 spawned / attached agent 视为 `standalone`

这层约束的意义是：

- 避免把 child session 当作新的 attach/rebind 目标
- 避免 child 覆盖 parent 的 watcher
- 避免前端把 child 显示为“也有一份独立 provider 当前选择”

#### 4.2.4 面向当前仓库的 session 分类

结合现在 `session.catalog` 的输出，建议把 catalog 结果理解为下表：

| catalog 分组 | 语义 | 推荐映射 |
|---|---|---|
| `managed` | 已被 agentd 纳管的 session | `sessionControl=managed` |
| `attachable` | 当前 live process，可 attach | `sessionLiveness=live`, `sessionControl=attachable` |
| `claudeFiles` / `opencodeFiles` | 文件或 DB 中发现的可恢复候选 | `sessionLiveness=history` 或 `stale`，`sessionControl=rebindable` 或 `unavailable` |

其中：
- `filterLiveClaudeFileSessions()` 已经在避免 live session 同时出现在 `attachable` 和 `claudeFiles`
  - `agentd/internal/ws/handler.go:375`
- 这说明当前实现其实已经隐含了：
  - **live session**
  - **history session**
  - **managed session**
  这三类区分

#### 4.2.5 attach 与 rebind 的区别

这两个动作在设计上必须拆开：

##### attach

含义：
- 对一个**仍然 live 的底层进程**建立接管/观察关系
- 可能是 tmux 可交互 attach
- 也可能只是 watcher 只读 attach

当前代码对应：
- `agent.attach`
- `session.attach` 中 `pid` 分支
- `ProcessInfo.AttachMode()` / `AttachReadOnlyReason()`

##### rebind

含义：
- 不依赖当前 live process
- 只要 `sessionId` 仍有效，就重新把 managed agent 与这个 session 绑定起来
- 本质是“重新绑定上下文”，不是“抢占进程”

当前代码对应：
- `session.attach` 中 `agentId` 分支：重启 watcher
- `session.attach` 中 `sessionId` 分支：按 sessionId 新建/恢复 agent
- `StartWatcherForAgent()` 按 `resume_session_id` 找 session 文件

所以设计上建议明确：

- `attach` 回答：**能不能接上现在这个 live 进程**
- `rebind` 回答：**即使 live 进程不在，能不能把这个 session 重新接回来**

#### 4.2.6 推荐的最小 API 扩展

如果只做最小扩展，建议先不要一次把所有字段都推到前端，而是从这 4 个字段开始：

```json
{
  "sessionId": "...",
  "sessionLiveness": "live|history|stale|unknown",
  "sessionControl": "managed|attachable|rebindable|read_only|unavailable",
  "sessionState": "none|standby|active|resumable|missing|broken"
}
```

这样前端已经能回答：

- 这个 session 是谁：`sessionId + provider + source`
- 它是不是 live：`sessionLiveness`
- 它能不能接管：`sessionControl`

而 `sessionState` 则继续服务于详情页和运行态展示。

### 4.3 新增字段：`providerState`

这里要补一个 team mode 约束：**providerState 默认属于 root session / root runtime，而不是属于每个 child agent。**

也就是说，在 team mode 下：

- child agent 可以继承展示 `providerState`
- 但 child 不应被建模为拥有独立的 `currentProviderId`
- child 上触发的 provider switch，本质上仍然是在修改 root runtime 对应的 Claude / cc-switch 共享配置

否则会出现一个错误模型：

- parent 显示 provider=A
- child-1 显示 provider=B
- child-2 显示 provider=C

但实际上它们共享的是同一份 `~/.claude/settings.json` 与同一份 cc-switch 当前选择，这在当前代码里并不存在真正的多 runtime 隔离。

建议新增：

```json
{
  "providerState": "synced | drifted | unknown",
  "currentProviderId": "...",
  "runtimeProviderId": "...",
  "providerStateReason": "..."
}
```

定义如下：

| 值 | 含义 |
|---|---|
| `synced` | cc-switch 当前选择与 Claude runtime 实际配置一致 |
| `drifted` | cc-switch 选择与 runtime 配置不一致 |
| `unknown` | 当前无法可靠判断 |

其中：

- `currentProviderId`：逻辑上的“当前选择 provider”
- `runtimeProviderId`：根据 `~/.claude/settings.json` 实际匹配出的 provider
- 在 team mode 下，这两个字段都应理解为 **root session 级别字段**，child 只继承展示，不单独拥有真值

可选地，可以再补一个仅供诊断/前端文案使用的字段：

```json
{
  "providerScope": "root | inherited | standalone"
}
```

定义建议：

| 值 | 含义 |
|---|---|
| `root` | 当前 agent 自己就是 provider state 的根作用域 |
| `inherited` | 当前 agent 是 team child，展示的是继承来的 provider 状态 |
| `standalone` | 当前 agent 与 team 无关，拥有独立 runtime 作用域 |

### 4.4 可选字段：`runtimeState`

如果需要进一步分离“进程/生命周期”与“会话可恢复性”，可增加：

```json
{
  "runtimeState": "starting | live | exited | stopped | crashed"
}
```

映射建议：

| runtimeState | 说明 |
|---|---|
| `starting` | 正在拉起 |
| `live` | 进程当前存在，或 attach 处于在线接管状态 |
| `exited` | Claude `-p` 等一次性交互进程已退出，但 agent 语义上可继续使用 |
| `stopped` | 被显式停止 |
| `crashed` | 异常退出 |

这个字段不是第一阶段必需，但它能把当前 `idle` 的多重含义再拆开一层。

---

## 5. 状态转移设计

### 5.1 兼容现有代码的主状态转移

```text
created
  → starting
  → idle
  ⇄ working
  → stopped
  → crashed
```

这部分保持不变。

### 5.2 sessionState 转移

```text
none
  → standby        (创建新会话并成功挂上 watcher)
  → active         (开始输出)
  → standby        (输出结束)
  → resumable      (Claude -p 退出，但 resume_session_id 仍有效)
  → missing        (sessionId 存在，但底层文件/DB 不可定位)
  → broken         (watcher/resume 失败)
```

### 5.3 providerState 转移

```text
unknown
  → synced         (provider.list 成功匹配 runtime 与 current)
  → drifted        (runtime 与 current 不一致)
  → synced         (执行 provider.switch 后重新对齐)
```

### 5.4 Team 模式下的作用域转移

在 team mode 下，provider 状态管理还需要一个隐含作用域状态机：

```text
standalone
  → root           (agent 被识别为 team root)
  → inherited      (agent 被识别为 team child)
```

约束如下：

- `root` 可以发起 provider switch，并对共享 runtime 生效
- `inherited` 可以展示 provider 状态，但不应被解释为拥有独立 provider 真源
- 如果 UI 从 child 入口触发 provider switch，后端语义也应仍然落到 root runtime 的共享配置上

---

## 6. 直接落地到当前代码的判断规则

### 6.1 `sessionState` 推导规则

建议集中放在 `agentd/internal/agent/manager.go` 或单独 helper 中，按如下顺序推导：

1. 如果 `status == working` → `active`
2. 如果 `resume_session_id == ""` → `none`
3. 如果 `status == idle` 且 watcher 非空 → `standby`
4. 如果 `status == idle` 且 watcher 为空，且 `resume_session_id != ""` → `resumable`
5. 如果已知 session 文件 / OpenCode session 记录不存在 → `missing`
6. 如果 watcher 启动失败 / resume 失败 → `broken`

### 6.2 `providerState` 推导规则

直接复用 `provider.list` 已有判定逻辑：

1. 用 `~/.claude/settings.json` 匹配 provider config，得到 `runtimeProviderId`
2. 取当前逻辑选择的 `currentProviderId`
3. 比较两者：
   - 相等 → `synced`
   - 不等 → `drifted`
   - 任一为空或无法判定 → `unknown`

这样第一阶段不需要重写 provider switch，只需要把已有逻辑显式化。

### 6.3 team mode 下的 provider / session 作用域规则

建议把下面几条作为第一阶段的硬规则写死：

1. **team child 不创建独立 provider 当前态**
   - child 共享 root 对应的 `currentProviderId/runtimeProviderId`
2. **team child 不作为独立 attach/rebind 目标暴露**
   - 当前 scanner/periodic attach 已经在尽量过滤这类进程
3. **sessionID 冲突优先解释为 parent/child 共享，而不是重复 session**
   - 这与当前 `trackedSessionIDs` 跳过逻辑一致
4. **provider.switch 的作用对象是共享 runtime 配置，不是 child 私有配置**
   - 当前 `provider.switch` 直接写全局 `~/.claude/settings.json` 与 cc-switch 状态，本质就是全局/根作用域更新
5. **如果 child 入口触发 switch，展示层可保留从 child 发起，但状态层要按 root/inherited 解释**

这样可以避免 team mode 下最常见的三个误判：

- 把 child 当成新 session
- 把 child 当成可单独切换 provider 的 runtime
- 把共享配置漂移误判成多个 agent 各自不同步

---

## 7. API 变更建议

### 7.1 `agent.list`

当前返回：
- `id`
- `name`
- `provider`
- `workDir`
- `projectName`
- `status`
- `hasHistory`
- `attachMode`
- `readOnly`
- `readOnlyReason`
- `sessionId`

实现：`agentd/internal/ws/handler.go:125`

建议新增：

```json
{
  "runtimeState": "live",
  "sessionState": "standby",
  "sessionStateReason": "watcher attached",
  "providerState": "synced"
}
```

### 7.2 `agent.status_changed`

当前事件只传：

```json
{
  "agentId": "...",
  "status": "idle"
}
```

建议兼容式扩展：

```json
{
  "agentId": "...",
  "status": "idle",
  "sessionState": "resumable",
  "runtimeState": "exited"
}
```

### 7.3 `provider.list`

建议返回结构从：

```json
{
  "providers": [...],
  "current": "provider-id"
}
```

扩展为：

```json
{
  "providers": [...],
  "current": "provider-id",
  "runtimeProviderId": "provider-id",
  "providerState": "synced",
  "providerStateReason": "matched by settings.json env",
  "providerScope": "root"
}
```

这能直接支撑前端显示“当前已选择 / 当前已生效 / 当前漂移”的区分。

在 team mode 下，如果当前 agent 是 child，则建议允许前端把 `providerScope=inherited` 转译成类似文案：

```text
当前 Provider 状态继承自根会话；切换将作用于共享 runtime 配置。
```

---

## 8. spawned / attached / team child 的差异约束

这是当前代码里最容易被误解，但又最值得文档化的部分。

### 8.1 spawned agent

行为：

- 由 agentd 拉起
- provider switch 后可以 restart in place
- restart 时保留 `resume_session_id`
- 所以语义上是：
  - **同一会话上下文，切换后续执行 provider**

关键代码：`agentd/internal/ws/handler.go:1740`

### 8.2 attached agent

行为：

- agentd 只是接管观察，不拥有该进程生命周期
- provider switch 时**不 kill 进程**
- 只改配置文件，让外部进程在后续时机自行感知

语义上应明确为：

- **配置已切换，不等于当前正在跑的 attach 会话立即切换完成**

因此建议前端在 attached 模式下，如果执行了 provider switch，应允许显示轻提示：

```text
配置已更新；当前附着会话可能不会立即完全切换，取决于外部 Claude 进程何时重新读取配置。
```

### 8.3 team child agent

行为：

- 由 Claude team mode / Agent tool 在 parent 会话内部派生
- 与 parent 共享底层 session 线索，通常不是独立 session 真源
- 不应因为被扫描到就单独创建一份新的 watcher / provider 当前态
- provider switch 若从 child 视角触发，语义上仍应解释为修改 root runtime 对应的共享配置

当前代码证据：

- Darwin scanner 明确过滤 `claude -p` sub-agent
  - `agentd/internal/scanner/scanner_darwin.go:79`
- `Attach()` 避免因 friendly name 复用错误而 hijack parent watcher
  - `agentd/internal/agent/manager.go:1624`
- 周期扫描按 `sessionID` 去重，避免把 child 当成独立 managed agent
  - `agentd/internal/agent/manager.go:1896`

语义上应明确为：

- **child 可以有自己的展示入口，但默认不拥有独立 provider state 真源**
- **child 的 session continuity 默认附着在 root session 上理解**

因此建议前端如果识别到 `providerScope=inherited` 或未来补充 `sessionOwnership=child`，应显示轻提示：

```text
当前子代理复用根会话 / 根运行时；Provider 切换作用于共享配置，而不是子代理私有配置。
```

---

## 9. 推荐落地顺序

### Phase 1：只做增量状态暴露

目标：不改变现有业务路径，只把状态显式返回，并先把 team mode 作用域显式化。

变更点：

- `agentd/internal/ws/handler.go`
  - 扩展 `agent.list`
  - 扩展 `agent.status_changed`
  - 扩展 `provider.list`
- `agentd/internal/agent/manager.go`
  - 增加状态推导 helper
  - 增加 team root / child / standalone 判定 helper
- `agentapp/lib/models/agent_model.dart`
  - 增加可选字段
- `agentapp/lib/screens/dashboard_screen.dart`
- `agentapp/lib/screens/agent_detail_screen.dart`
  - 展示 session/provider 状态

### Phase 2：补齐错误态与诊断态

目标：把 `missing / broken / drifted` 真正做成可见诊断信息。

变更点：

- watcher 启动失败时记录原因
- session file/DB 不存在时显式落为 `missing`
- provider 匹配失败时返回 `providerStateReason`

### Phase 3：如有必要，再收敛为单一真源

这个阶段再考虑：

- 重新定义 current provider 的唯一真源
- 减少三份状态漂移风险

但不应该作为第一步落地前提。

---

## 10. 为什么这是“能直接落地到现在代码里的设计”

原因是它满足五个条件：

1. **不推翻现有 `status`**
   - 现有前端还能继续跑
2. **大部分状态都能从现有实现直接推导**
   - 不需要重写核心生命周期
3. **providerState 直接复用当前 `provider.list` 的判定逻辑**
   - 只需要把隐式逻辑变成显式返回
4. **spawned / attached / team child 的行为差异在代码里都已有雏形**
   - 不是新增概念，而是把已有约束文档化
5. **team mode 不需要第一阶段就引入新的 runtime 隔离机制**
   - 只需要先把 root / inherited / standalone 作用域说清楚

因此这是一份**低风险、增量、兼容现状**的设计，而不是另起炉灶。

---

## 11. 建议的最小落地契约

如果只做一轮最小实现，建议后端先补这 4~6 个字段即可：

### `agent.list`

- `sessionState`
- `sessionStateReason`
- `runtimeState`
- `providerState`
- 可选：`providerScope`
- 可选：`sessionControl`

### `provider.list`

- `runtimeProviderId`
- `providerState`
- `providerStateReason`
- 可选：`providerScope`

这组字段已经足够让前端把当前最困扰人的状态问题说清楚：

- 这是“空闲”还是“可恢复”？
- 当前 provider 是“已切换”还是“已生效”？
- attached 会话现在到底处于哪种语义？
- team child 当前展示的是自己的状态，还是继承自 root 的共享状态？

---

## 12. 关联代码索引

- Agent 状态定义：`agentd/internal/agent/agent.go:15`
- Agent 状态回调：`agentd/internal/agent/manager.go:776`
- Claude pipe 退出后保持 idle：`agentd/internal/agent/manager.go:800`
- RestartInPlace：`agentd/internal/agent/manager.go:1112`
- ResumeSessionID 更新：`agentd/internal/agent/manager.go:1324`
- StartWatcherForAgent：`agentd/internal/agent/manager.go:1386`
- watcher → working/idle 映射：`agentd/internal/agent/manager.go:1750`
- agent.list：`agentd/internal/ws/handler.go:125`
- provider.list：`agentd/internal/ws/handler.go:1561`
- provider.switch：`agentd/internal/ws/handler.go:1624`
- attached/spawned provider switch 分支：`agentd/internal/ws/handler.go:1734`
- 前端 AgentModel：`agentapp/lib/models/agent_model.dart:1`

---

## 13. 一句话总结

**当前代码已经有“运行态 + session continuity + provider 同步”的基础能力；本设计只是把这些隐式状态显式化，并以兼容方式暴露给前后端，而不是重写整个状态机。**
