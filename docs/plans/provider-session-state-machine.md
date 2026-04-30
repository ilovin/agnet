# Provider / Session 状态机实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 把当前 `status + session continuity + provider / cc-switch` 的隐式状态，增量落成可推导、可展示、可测试的状态机；同时把 Provider 切换能力收敛为“能可靠切就可写，不能可靠切就 App 端只读”。

**Depends on:**
- `docs/superpowers/specs/2026-04-15-provider-session-state-machine-design.md`
- `docs/superpowers/specs/2026-04-15-provider-cc-switch-shared-state-design.md`

---

## 实施原则

- 保留现有 `status`，新增字段必须兼容旧前端。
- 状态判断优先复用现有实现，不先重写生命周期。
- **runtime truth 优先于 DB 标记。**
- **如果无法可靠证明 Provider 切换会正确生效，App 必须只读，不伪装“可切换成功”。**
- team mode 下，child 默认继承 root runtime 的 Provider 状态，不拥有独立真源。

---

## 关键文件

- `agentd/internal/agent/manager.go`
- `agentd/internal/ws/handler.go`
- `agentd/internal/scanner/scanner.go`
- `agentd/internal/scanner/scanner_darwin.go`
- `agentapp/lib/models/agent_model.dart`
- `agentapp/lib/screens/dashboard_screen.dart`
- `agentapp/lib/screens/agent_detail_screen.dart`
- `agentapp/lib/services/ws_client.dart`

---

## Task 1: 后端状态推导 helper

**目标：** 先把状态机判断集中到 helper，避免散落在 handler/UI。

- [ ] 在 `agentd/internal/agent/manager.go` 增加推导 helper：
  - `runtimeState`
  - `sessionState`
  - `sessionStateReason`
  - `sessionControl`
- [ ] 在 `agentd/internal/ws/handler.go` 增加 Provider 相关 helper：
  - `currentProviderId`
  - `runtimeProviderId`
  - `providerState`
  - `providerStateReason`
  - `providerScope`
  - `providerWriteMode`
  - `providerReadOnlyReason`
- [ ] 把 team root / child / standalone 识别规则固化到 helper。
- [ ] 为上述 helper 写表驱动单测。

**完成标准：** 仅靠 helper 即可回答“是不是 live / 能不能接管 / 是否可写 Provider”。

---

## Task 2: 扩展后端 API 契约

**目标：** 在不破坏旧字段的前提下，把新状态显式返回。

- [ ] 扩展 `agent.list` 返回：
  - `runtimeState`
  - `sessionState`
  - `sessionStateReason`
  - `sessionControl`
  - `providerState`
  - 可选：`providerScope`
- [ ] 扩展 `agent.status_changed` 推送：
  - `runtimeState`
  - `sessionState`
  - `sessionStateReason`
- [ ] 扩展 `provider.list` 返回：
  - `runtimeProviderId`
  - `providerState`
  - `providerStateReason`
  - `providerScope`
  - `providerWriteMode`
  - `providerReadOnlyReason`
- [ ] 保证字段缺失时旧前端仍能工作。

**完成标准：** App 不再需要自己猜测“空闲/可恢复/只读/可切换”。

---

## Task 3: Provider 写能力治理

**目标：** 将 Provider 切换能力从“默认可写”改成“验证通过才可写”。

- [ ] 把 `provider.switch` 的可写前置条件写死：
  - scope 必须是 `root` 或 `standalone`
  - 不能是 `inherited`
  - 不能是 `providerState=unknown`
  - 如果是 attached 且无法保证当前 live 会话立即切换，则标记只读
- [ ] 保留 spawned root/standalone 场景的 switch 能力。
- [ ] attached / team child / unknown scope 场景统一走只读说明。
- [ ] 增加拒绝切换时的结构化 reason，供前端直接展示。

**完成标准：** 后端与前端都不会在“不可靠切换”场景下继续暴露写入口。

---

## Task 4: App 状态展示与只读降级

**目标：** 前端准确展示状态，并在只读场景禁用切换入口。

- [ ] 更新 `agentapp/lib/models/agent_model.dart` 与 Provider 相关模型。
- [ ] 在 Dashboard/Detail 展示：
  - `runtimeState`
  - `sessionState`
  - `sessionControl`
  - `providerState`
- [ ] 根据 `providerWriteMode` 决定切换入口：
  - `writable`：允许切换
  - `read_only`：禁用切换按钮并显示原因
- [ ] attached 场景显示轻提示：
  - 配置已更新，不保证当前 live 会话立即切换
- [ ] team child 场景显示轻提示：
  - 当前 Provider 状态继承自 root runtime

**完成标准：** 用户在 App 内能一眼看出“是否可交互 / 是否可恢复 / 是否可切换 Provider”。

---

## Task 5: 测试与验收

- [ ] Go 单测：状态推导、team 作用域、只读判定。
- [ ] Go 集成测试：
  - spawned agent Provider 切换
  - attached agent 只读降级
  - team child inherited 只读降级
- [ ] Flutter 单测：模型解析、按钮禁用、提示文案。
- [ ] Flutter analyze / flutter test。
- [ ] Go 相关包测试。
- [ ] 在现有 Chrome tab 做真实验证：
  - spawned 场景可切换
  - attached 场景只读
  - team child 场景只读/继承展示

**完成标准：** 文案、状态、交互权限三者一致。

---

## 风险与缓解

### 风险 1：`idle` 历史包袱导致误判
- 缓解：始终同时返回 `runtimeState + sessionStateReason`。

### 风险 2：team child 被误识别为独立 runtime
- 缓解：强制 `providerScope=inherited` 且 `providerWriteMode=read_only`。

### 风险 3：cc-switch 与 runtime 多副本漂移
- 缓解：读路径始终以 runtime 配置优先；无法判定时直接只读。

### 风险 4：attached 语义被误解为“已即时切换”
- 缓解：attached 默认不承诺即时生效，必要时直接只读。

---

## 最小可交付顺序

1. 后端 helper + 单测
2. `agent.list` / `provider.list` / `agent.status_changed` 扩展
3. 前端展示新状态
4. 前端按 `providerWriteMode` 只读降级
5. 真实浏览器验收

---

## 一句话实施准则

**能可靠验证切换生效，就开放写操作；不能可靠验证，就在 App 端明确显示为只读。**
