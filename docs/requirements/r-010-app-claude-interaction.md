# R-010 phone-talk app 支持 Claude 交互工具的渲染与回传

## 背景

phone-talk app（Flutter Web + 移动端）当前可以查看 Claude 会话的文本和工具调用，但 **Claude 的交互类工具（要求用户输入/选择/批准）没有被正确渲染和回传**：

- Claude 调用 `AskUserQuestion`（选择题）→ app 端只显示一行 `[AskUserQuestion: ...]`，看不到选项
- Claude 调用 `ExitPlanMode`（计划批准）→ app 端只显示 `[ExitPlanMode]`，看不到 plan 内容也无法批准
- Claude 请求 `Bash` 权限批准 → agentd 已实现 PermissionManager 通道，但 app 渲染不完整
- Claude 请求 `Edit` / `Write` 文件改动批准 → 同上

结果：用户必须切回终端 Claude Code 主会话才能完成交互，phone-talk app 形同只读监视器。

## 目标

让 phone-talk app（Web + 移动）能：
1. **完整渲染** 4 类交互（选项卡片、计划批准卡、Bash 权限卡、文件改动批准卡）
2. **接收用户输入并回传**，使 Claude 不需用户切回终端就能继续

## 范围

### 必做（4 类交互）

| 工具 | 渲染需求 | 回传通道 |
|---|---|---|
| AskUserQuestion | 显示 question 文本 + 多 question + options[]（含 label/description） + multiSelect 模式（单/多选） | tool_result 回到 Claude（via `conversation.send` 或新增 RPC） |
| ExitPlanMode | 显示 plan 文本（从 Claude 输出/文件读取） + 批准/拒绝按钮 + 拒绝时支持反馈文本 | tool_result（approve true/false + feedback） |
| Bash 批准 | 显示待执行命令 + allow/deny 按钮 | `conversation.permission_response` RPC（已存在） |
| Edit/Write 批准 | 显示文件路径 + diff（如能拿到） + allow/deny 按钮 | `conversation.permission_response` RPC（已存在） |

### 端

- Flutter app **同一份代码**支持 Web + 移动（已是现状）
- 至少在 macOS Chrome（用户主要环境）端到端验证通过

### 不做

- 不实现"用户在 app 端撤销已发出的回复"
- 不实现"多用户共享会话同时投票"（单用户场景）
- 不为 Claude Code 各种边缘工具（如 Skill、SlashCommand、TodoCreate 等）做交互卡片 — 仅 4 类
- 不动 agentgw 协议层（Explore 确认它是纯透传）

## 验收标准

1. **AskUserQuestion 端到端**：
   - Claude 调用 AskUserQuestion 含多 option（如 R-008 实战中各次问题）
   - app 显示选项卡片，用户点击 → Claude 收到 tool_result 并继续
2. **ExitPlanMode 端到端**：
   - Claude 调用 ExitPlanMode（如 plan 模式实战）
   - app 显示 plan 文本 + Approve/Reject 按钮
   - Reject 支持填写反馈文本
3. **Bash 批准**：
   - Claude 请求执行命令（非 allowlist 内）
   - app 显示命令 + allow/deny；点 allow → 命令执行，点 deny → Claude 收到拒绝
4. **Edit/Write 批准**：
   - Claude 请求改文件
   - app 显示路径 + （如能拿到）diff + allow/deny
5. **回归**：现有非交互消息（text、tool_use 非交互、tool_result）渲染不变
6. **双端验证**：移动端和 Web 端都跑通至少一种交互
7. **真实 Chrome 验证**（按 CLAUDE.md "existing Chrome only" 约束）：在已打开的 Chrome 标签中走通流程

## 技术路径（从 Explore 摘录）

**采用路径 A**：agentd 解析 tool_use input → 引入新 kind → app 按 kind 渲染 → 用户选择回传到 Claude。

关键改动点：
- `agentd/internal/watcher/claude.go`：在 `buildToolSummary`（约 line 922）保留 AskUserQuestion / ExitPlanMode 的 input 字段（options[]、plan）
- `agentd/internal/agent/manager.go`：在 `handleStreamJSONEvent`（约 line 546） 为新 tool 输出新 kind 事件（如 `kind: "ask_user_question"`、`kind: "exit_plan_mode"`）
- 协议层：`rpc_types.go` 增加新 kind 常量与字段
- Flutter app：新建 `AskUserQuestionCard`、`ExitPlanModeCard`，扩展 `permission_request` 渲染
- 回传：AskUserQuestion 走 `conversation.send`（注入 tool_result JSON）；权限批准复用已有 `conversation.permission_response`

## 拆解（建议，待评审）

| Task | 范围 | 依赖 | 工作量 |
|---|---|---|---|
| **R-010-T1** | 协议层定义新 kind + 字段（rpc_types.go + Flutter 模型） | 无 | S |
| **R-010-T2** | agentd watcher 提取 AskUserQuestion / ExitPlanMode input | T1 | M |
| **R-010-T3** | Flutter `AskUserQuestionCard` 组件 + 集成 | T1 + T2 | M |
| **R-010-T4** | Flutter `ExitPlanModeCard` 组件 + plan 文本展示 + Approve/Reject + 反馈输入 | T1 + T2 | M |
| **R-010-T5** | 完善 Flutter `permission_request` 渲染（Bash + Edit + Write） | T1 | S（基础已存在） |
| **R-010-T6** | 端到端测试：Web + 移动 Chrome 验证 4 类交互 | T2-T5 | M |
| **R-010-T7** | 回归测试：非交互消息渲染不变 + 现有 conversation.send / permission_response 通路不破坏 | T6 | S |

## 风险与权衡

- **JSONL stream-json 双路解析一致性**：agentd 同时通过 PTY 和 JSONL 文件读取 Claude 输出，两路对 tool_use input 的提取必须一致，否则会出现"看到选项 vs 看不到选项"的不稳定行为
- **Edit/Write 的 diff**：Claude 在 permission 请求里通常只给 file_path + 新内容，不直接给 diff。本轮可先不做 diff 渲染，只显示文件路径 + 新内容预览
- **AskUserQuestion 多 question 同时渲染**：Claude 可一次问多个 question，app 需要在同一卡片内呈现多组选项
- **撤销/编辑回复**：用户点错按钮如何处理？本轮不做撤销，建议加 confirm 二次确认

## 引用

- Explore 报告：见 R-010 Explore agent (a720ca00d15b71bf2) 输出
- 关键代码位置：
  - agentd/internal/watcher/claude.go:910-922
  - agentd/internal/agent/manager.go:546, 817
  - agentd/internal/ws/handler.go:828, 1100, 1186, 1241
  - app/lib/agent_detail_screen.dart:182, 1467
- 相关需求：R-009（Claude Code Agent View 适配，工具层）
- 设计文档：待写 d-010-app-claude-interaction.md（本需求实施前置）
