# Provider / cc-switch 共享状态设计（含 App 只读降级）

**日期**：2026-04-15
**状态**：草稿
**类型**：增量设计 / 共享状态治理

---

## 1. 背景

当前 Provider 切换链路并不是单一真源，而是跨三处状态同步：

- `~/.claude/settings.json`
- `~/.cc-switch/cc-switch.db`
- `~/.cc-switch/settings.json`

当前实现里：

- `provider.list` 优先根据 runtime settings 反推当前 Provider
  - `agentd/internal/ws/handler.go:1561`
- `provider.switch` 会同时写 runtime settings、DB `is_current`、cc-switch settings
  - `agentd/internal/ws/handler.go:1624`
- spawned agent 会 restart in place；attached agent 不会被 kill/restart
  - `agentd/internal/ws/handler.go:1734`

因此“Provider 是否切换成功”其实至少包含三层语义：

1. **逻辑选择是否更新**
2. **runtime 配置是否对齐**
3. **当前会话是否已可靠生效**

如果这三层说不清，App 就会出现一个危险状态：

- UI 允许用户点“切换”
- 但系统并不能可靠保证当前会话真的完成切换

本设计的原则就是：

> **如果不能可靠保证切换生效，App 端就只读。**

---

## 2. 设计目标

### 2.1 目标

- 明确 Provider / cc-switch 的共享状态模型
- 明确 team mode 下 root / child 的 Provider 作用域
- 明确哪些场景允许写，哪些场景只能只读展示
- 让 App 不再伪装“可切换成功”

### 2.2 非目标

- 本阶段不消灭三份状态副本
- 本阶段不引入新的 Provider 持久化系统
- 本阶段不保证 attached 会话即时切换生效

---

## 3. 共享状态模型

建议统一对外暴露：

```json
{
  "currentProviderId": "...",
  "runtimeProviderId": "...",
  "providerState": "synced | drifted | unknown",
  "providerScope": "root | inherited | standalone",
  "providerWriteMode": "writable | read_only",
  "providerReadOnlyReason": "..."
}
```

### 3.1 字段定义

#### `currentProviderId`
逻辑上的“当前选择 Provider”。

来源：
- 优先从 cc-switch 语义获取
- 当前实现里可由 `provider.list` 现有逻辑返回的 `current` 演进而来

#### `runtimeProviderId`
Claude runtime 当前实际匹配出的 Provider。

来源：
- `~/.claude/settings.json` 里的 env / top-level config 与 DB 配置匹配

#### `providerState`

| 值 | 含义 |
|---|---|
| `synced` | 逻辑选择与 runtime 实际配置一致 |
| `drifted` | 逻辑选择与 runtime 配置不一致 |
| `unknown` | 当前无法可靠判定 |

#### `providerScope`

| 值 | 含义 |
|---|---|
| `root` | 当前 agent 是共享 Provider 状态的根作用域 |
| `inherited` | 当前 agent 是 team child，展示的是继承状态 |
| `standalone` | 当前 agent 与 team 无关，拥有独立 runtime 作用域 |

#### `providerWriteMode`

| 值 | 含义 |
|---|---|
| `writable` | App 可以开放切换入口 |
| `read_only` | App 必须只读展示，不开放切换入口 |

#### `providerReadOnlyReason`

供前端直接展示的原因，例如：

- `provider scope is inherited from root session`
- `attached runtime cannot guarantee immediate provider switch`
- `provider runtime state is unknown`
- `provider drift cannot be corrected safely from app`

---

## 4. 读路径原则

Provider 状态读取遵循下面优先级：

1. **runtime truth 优先**
   - 先从 `~/.claude/settings.json` 反推 `runtimeProviderId`
2. **逻辑 current 次之**
   - 用 DB `is_current` / cc-switch current 语义得到 `currentProviderId`
3. **无法对齐时明确标记**
   - `providerState=drifted` 或 `unknown`
4. **无法可靠判断写能力时直接只读**
   - `providerWriteMode=read_only`

也就是说：

- 读可以尽量宽松
- 写必须严格保守

---

## 5. 写路径原则

### 5.1 允许写的最小条件

只有在同时满足下列条件时，App 才应显示可切换：

1. `providerScope` 是 `root` 或 `standalone`
2. 当前不是 team child (`inherited`)
3. runtime 可验证
4. 切换后能通过现有机制可靠使后续执行生效
5. 当前不是“只能观察”的运行形态

### 5.2 必须只读的场景

以下任一成立，App 直接只读：

1. **team child / inherited scope**
   - child 不拥有独立 Provider 真源
2. **attached session**
   - 当前 live 会话不一定会立即重新读取配置
   - 不能把“配置文件已改”伪装成“当前会话已切换成功”
3. **`providerState=unknown`**
   - 无法确认 runtime 当前状态
4. **作用域无法判定**
   - 不清楚当前入口是否在 root runtime 上
5. **只具备历史 / 观察能力，没有可靠写入通道**

---

## 6. team mode 约束

team mode 下需要把“显示入口”和“状态真源”分开：

- child 可以有自己的详情页/入口
- 但 child 默认不拥有独立 `currentProviderId`
- child 展示的是 root runtime 继承来的 Provider 状态
- child 入口如果触发切换，请求语义也必须解释为 root runtime 的共享配置修改

因此第一阶段建议直接采用保守策略：

> **team child 默认 `providerWriteMode=read_only`。**

这与当前仓库已有实现趋势一致：

- Darwin scanner 已过滤 Claude sub-agent
  - `agentd/internal/scanner/scanner_darwin.go:79`
- `Attach()` 已避免 child hijack parent watcher
  - `agentd/internal/agent/manager.go:1624`
- periodic scan 已按 `sessionID` 去重
  - `agentd/internal/agent/manager.go:1896`

---

## 7. attached / spawned 差异

### 7.1 spawned

spawned agent 由 agentd 拉起，`provider.switch` 后可 restart in place。

这意味着：
- 更接近“可验证切换生效”
- 可以作为 `writable` 的主要候选场景

### 7.2 attached

attached agent 只是观察外部进程，不拥有该进程生命周期。

当前实现虽然允许更新配置文件，但并不保证当前 live 会话立即完全切换。

因此本设计建议：

> **attached 默认只读。**

如果未来证明某类 attached 场景可以可靠即时切换，再单独放开；第一阶段不要乐观建模。

---

## 8. App 端交互策略

### 8.1 列表页 / 详情页

App 应直接根据后端字段决定交互，而不是自己推测：

- `providerWriteMode=writable`：显示切换入口
- `providerWriteMode=read_only`：禁用切换入口
- 同时展示 `providerReadOnlyReason`

### 8.2 推荐文案

#### team child

```text
当前 Provider 状态继承自根会话；子代理不提供独立切换。
```

#### attached

```text
当前会话为附着观察模式；为避免误导，Provider 切换在 App 端只读展示。
```

#### unknown

```text
当前无法可靠判断 Provider 运行态；暂不提供切换。
```

---

## 9. API 建议

### `provider.list`

建议扩展为：

```json
{
  "providers": [...],
  "current": "provider-id",
  "runtimeProviderId": "provider-id",
  "providerState": "synced",
  "providerStateReason": "matched by settings.json env",
  "providerScope": "root",
  "providerWriteMode": "writable",
  "providerReadOnlyReason": ""
}
```

### `agent.list`

建议补充：

```json
{
  "providerState": "synced",
  "providerScope": "inherited",
  "sessionControl": "read_only"
}
```

这样前端就能同时看见：

- 当前 Provider 是否同步
- 当前作用域是不是继承来的
- 当前会话是不是只能观察

---

## 10. 状态转移（Provider 可写性）

```text
unknown
  → read_only        (无法可靠判断 runtime/scope)
  → writable         (判定为 root/standalone 且可验证)

writable
  → read_only        (attached / inherited / unknown)
  → writable         (切换成功并验证通过)

read_only
  → writable         (仅当后续判定变得可靠)
```

第一阶段的核心不是“尽可能让更多场景可写”，而是：

- **宁可少开写入口，也不要误开**

---

## 11. 一句话结论

**Provider / cc-switch 共享状态在第一阶段应采用保守模型：runtime 优先判定、scope 明确区分、不能可靠验证切换生效时 App 直接只读。**
