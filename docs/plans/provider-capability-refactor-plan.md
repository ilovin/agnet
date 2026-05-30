# P: Provider Capability / Event Normalizer 重构计划

## 背景与目标

当前代码对 `tool.call / thinking / 提问交互` 已有部分统一语义，但发送链路与解析链路仍有较重 provider 分支。  
目标是把“新增一个 harness/provider”的成本降到可模板化接入，避免每次在 `handler/manager/watcher` 多处打补丁。

重构后应达到：

1. 新 provider 只需实现 capability + normalizer + scanner adapter，即可接入主流程。
2. `conversation.send` 不再由大 `if/switch` 驱动，而由策略派发。
3. thinking / tool_use / ask_user_question / permission_request 在历史与实时路径语义一致。

---

## 范围

### In Scope
- `agentd` provider capability 抽象
- watcher 事件归一化接口
- ws `conversation.send` 策略化
- manager 统一历史回放语义
- 回归测试补齐

### Out of Scope
- Flutter UI 视觉改版
- tunnel / deploy 基础设施重写
- provider 上游协议变更（仅做本地适配层）

---

## 现状问题

1. **解析层未插件化**：`claude.go` / `opencode_db.go` 内部逻辑各自维护，复用有限。  
2. **发送层分支重**：`conversationSend` provider 分支复杂，易出现“假在线”或静默失败。  
3. **交互工具命名耦合**：interactive 解析偏 Claude tool name。  
4. **会话切换策略分散**：session switch/backfill/rebuild 在不同 watcher 各管一套。

---

## 目标架构

### 1) ProviderCapabilities（能力声明）

新增统一能力声明接口（建议位置：`agentd/internal/provider/capabilities.go`）：

- `SupportsThinking`
- `SupportsToolUse`
- `SupportsInteractiveQuestion`
- `SupportsPermissionPrompt`
- `SupportsSessionSwitch`
- `SendMode`（`tmux` / `pty` / `resume_cmd` / `http`）
- `RequiresTmuxForegroundValidation`
- `SupportsImageAttachment`

用途：
- handler 发送策略分发
- manager/watcher 事件校验与降级
- app 可按能力做 UI fallback

### 2) EventNormalizer（事件归一化）

定义接口（建议位置：`agentd/internal/provider/normalizer.go`）：

- 输入：provider 原始事件（JSONL 行 / DB row / stream item）
- 输出：统一 `NormalizedEvent`
  - `Role/Text/Kind/ToolName/ToolUseName/ToolUseInput/StatusChange/MsgID/SessionID`

要求：
- 实时路径和历史回放路径共用同一 normalizer 语义
- interactive payload key 与 `event_kinds.go` 一致

### 3) SendStrategy（发送策略）

将 `conversation.send` 拆为策略实现（建议位置：`agentd/internal/ws/send_strategy/*.go`）：

- `TmuxSendStrategy`
- `PtySendStrategy`
- `ResumeCmdSendStrategy`
- `HttpSendStrategy`（若保留）

由 capability 决定选路，统一：
- 发送前校验（例如 tmux 前台进程活性）
- 用户事件入库时机
- 失败返回码与错误文案

### 4) SessionLifecycle（会话生命周期钩子）

抽象 provider 会话切换钩子（建议位置：`agentd/internal/provider/lifecycle.go`）：

- `OnSessionSwitch(old,new)`
- `BackfillHistory(sessionID)`
- `RebuildHistoryIfDiverged()`

统一 manager 中的 session switch / clear / restart 行为。

---

## 分阶段实施

## Phase 0: 基线冻结

1. 固定当前回归基线（codex/opencode/hermes/claude）  
2. 记录关键 E2E 用例（agentcli + app）

验收：
- 当前主干全量测试通过
- 基线日志与用例存档

## Phase 1: Capability 抽象落地

1. 新建 capability registry  
2. 让现有 provider 先声明能力，不改行为

验收：
- 行为零变化
- handler 可读取 capability（仅日志）

## Phase 2: SendStrategy 拆分

1. 把 `conversation.send` 分支迁移到策略层  
2. 引入统一错误返回（含 tmux 假在线检测）

验收：
- `conversation.send` 主函数只保留分发与公共埋点
- Codex/Hermes 断联场景返回明确错误而非静默成功

## Phase 3: EventNormalizer 统一

1. 抽象 `NormalizedEvent`  
2. Claude/Codex/OpenCode/Hermes 分别实现 normalizer  
3. 历史回放复用同一语义

验收：
- thinking/tool_use/interactive 在实时与history一致
- app 侧无需 provider 特判

## Phase 4: SessionLifecycle 统一

1. 抽 session switch/backfill/rebuild 钩子  
2. 收敛 manager 中 provider-specific 会话逻辑

验收：
- `/clear`、restart、reattach 后会话连续性稳定
- 历史序列不丢、不重复

## Phase 5: 收尾与文档化

1. 删除废弃分支  
2. 更新 provider onboarding 文档与 PR checklist

验收：
- 新 provider 接入步骤可在 1 个文档中完成

---

## 测试计划

### 单元测试
- capability registry 映射测试
- send strategy 路由测试
- normalizer 语义测试（thinking/tool_use/interactive）
- lifecycle hook 行为测试

### 集成测试
- `agentd/internal/ws`：conversation.send 各 provider 路径
- `agentd/internal/agent`：history backfill/rebuild/session switch

### E2E（agentcli）
- `list-agents` 状态正确
- `send-message` 入库与响应链路
- `history` 连续 seq + kind 语义正确

---

## 风险与回滚

1. **风险：事件语义漂移导致 app 渲染异常**  
回滚：保留旧 normalizer 分支开关（feature flag）

2. **风险：发送策略拆分引入行为差异**  
回滚：策略层外包一层 legacy adapter，可快速切回旧路径

3. **风险：session switch 统一后破坏某 provider 特例**  
回滚：provider lifecycle 可局部覆盖默认实现

---

## 里程碑与交付物

1. `provider/capabilities.go` + provider 声明实现  
2. `ws/send_strategy/*` + handler 精简  
3. `provider/normalizer.go` + 4 类 provider normalizer  
4. `provider/lifecycle.go` + manager 集成  
5. 文档更新：
   - `docs/operations/provider-harness-onboarding.md`
   - 本计划文档

---

## 完成定义（DoD）

满足以下即视为完成：

1. 新增一个 provider 的改动文件数明显下降（主要集中在 scanner + normalizer + capability）。  
2. `conversation.send` 不再包含大量 provider 分支。  
3. thinking/tool/interactive 的实时与历史语义一致。  
4. `deploy.sh local` + `agentcli` 验证流程对所有已接入 provider 可复用。  
