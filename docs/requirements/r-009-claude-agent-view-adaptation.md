# R-009 Claude Code Agent View 适配

## 背景

Claude Code 自 v2.1.139 起提供 **Agent View** 特性（命令 `claude agents`，TUI 面板，`Enter` attach 子会话查看实时输出，`←` detach 返回主会话）。本机 settings 已开启 `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: 1`。

但当前 phone-talk 的 Manager workflow（`docs/management/manager-workflow.md`）没有利用这个能力：

- Manager 派子 agent → 子 agent 后台运行 → Manager 转述完成结果给用户
- **用户只能看摘要，无法实时观察子 agent 中间过程**
- R-008 实例：T6 dev agent 在 build/package 反复跑 1-2 分钟一次，用户看 Manager 静默几十分钟，问"为啥卡住了"。Agent View 恰好是为这个场景设计

## 目标

让用户在 Manager 派子 agent 后，**有清晰的入口和指引**去 attach 子会话查看进度，无需 Manager 主动转述中间过程。

## 范围

### 必做

1. **更新 `docs/management/manager-workflow.md`**：在 Phase 2（Explore）和 Phase 6（Dev/Test）启动子 agent 的描述里，加一段"Manager 须在派出 agent 后向用户播报子 agent 句柄；用户可独立 attach 查看"的约定
2. **更新 `CLAUDE.md`**：在 Manager 自检章节加一行"派子 agent 时报告 agentId/handle，并提示 `claude agents` 入口"
3. **新增 `docs/designs/d-009-agent-view-workflow.md`**：完整设计 + 用户操作手册片段

### 不做

- 不实施"自动推送子 agent 完成通知到主会话"（hook + session 通信，复杂，留给后续迭代）
- 不调整子 agent 的输出风格（无运行时改造）
- 不改 Agent 工具或后台任务通知机制（Claude Code 内核不动）
- 不写代码

## 验收

1. Manager 派子 agent 后的下一条用户可见消息中**包含可 attach 的标识**（agentId 或 session-id，待 d-009 确定）
2. `manager-workflow.md` Phase 2/6 章节显式引用 `claude agents`
3. `CLAUDE.md` Manager 自检表加入对应一项
4. `d-009-agent-view-workflow.md` 包含：场景描述、播报模板、用户操作步骤、待确认事项清单

## 风险与权衡

- **agentId vs session-id 不明确**：当前 Agent 工具 result 返回 `agentId: aXXX...`，但 `claude agents` 面板的列项可能是 session UUID。d-009 需要给出"先按 agentId 播报，若实测 attach 失败再切换"的回退方案
- **用户负担**：需要用户主动开终端跑 `claude agents`。可接受 — 这是 Research Preview 阶段的常见模式

## 引用

- Claude Code changelog 2.1.139：introduces `claude agents` (Research Preview)
- Claude Code 2.1.145：`claude agents --json` 供脚本/状态栏
- Claude Code 2.1.147：`Ctrl+T` 在 agents 面板中 pin 后台会话
- 官方文档：https://code.claude.com/docs/en/agent-view
- 痛点参考：R-008 T6 期间的"卡住几十分钟"事件
