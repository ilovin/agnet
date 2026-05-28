# D-009 Manager Workflow 与 Claude Code Agent View 集成设计

> 对应需求：[r-009-claude-agent-view-adaptation.md](../requirements/r-009-claude-agent-view-adaptation.md)
> 状态：Draft（基于 R-008 实战痛点和 Claude Code v2.1.139+ 新特性）

## 1. 设计目标

让 phone-talk 的 Manager-Agent 工作流与 Claude Code 内置的 **Agent View** 形成闭环：用户在 Manager 派出子 agent 后，**无需等待 Manager 转述**，可独立切入查看任意子 agent 的实时进度。

非目标：自动推送、运行时拦截、后台 hook（留给后续迭代）。

## 2. Claude Code Agent View 关键能力（事实）

| 能力 | 命令 / 操作 | 来源 |
|---|---|---|
| 列出所有会话 | 终端运行 `claude agents` | changelog v2.1.139 |
| Attach 子会话 | 面板内按 `Enter` | changelog v2.1.139 |
| Detach 返回 | 按 `←`（左箭头） | changelog v2.1.139 |
| Pin 后台会话 | `Ctrl+T` | changelog v2.1.147 |
| Rename 会话 | `Ctrl+R` | changelog v2.1.142 |
| 脚本化查询 | `claude agents --json` | changelog v2.1.145 |

启用条件：`~/.claude/settings.json` 含 `"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": 1`（本机已开启）。

## 3. 现状缺口

Manager 派子 agent 时，工具 result 返回类似：
```
agentId: a991774082658a3f6 (use SendMessage with to: 'a991774082658a3f6' to continue this agent)
output_file: /private/tmp/claude-503/.../tasks/a991774082658a3f6.output
```

但 Manager 不会主动把这两个标识告诉用户。用户即便知道 `claude agents` 命令，也不知道**该 attach 哪个**。

## 4. 设计方案：C + A 组合

### 4.1 方向 C — 文档约定（一次性改动）

**改 `manager-workflow.md`**：在 Phase 2（Explore）和 Phase 6（Dev/Test）启动 agent 的描述中插入：

> Manager 派出子 agent 后，必须在主会话给用户回一条"播报句"，包含：
> - 子 agent 名（如 `T4 deploy.sh DEPLOY_DRY_RUN`）
> - agentId（来自 Agent 工具 result 的 `agentId: aXXX...`）
> - 一行操作提示：`想看实时进度？另开终端跑 \`claude agents\`，在面板里 attach 该 agent。`

**改 `CLAUDE.md` Manager 自检表**：新增一行：
> - [ ] 派出子 agent 后是否给用户播报了 agentId 和 attach 提示？→ 没播报就 STOP，立即补上。

### 4.2 方向 A — Manager 行为约定（每次派 agent 时执行）

Manager 派 agent 后下一条文字消息必须满足"播报模板"：

```
🤖 已派 <task name>
   agentId: <aXXX...>
   想看实时进度？另开终端跑 `claude agents`，找该 agentId 按 Enter 进入。
```

并行多 agent 时，列出多行。

### 4.3 已确认 / 待确认

**已确认**：
- `agentId` 是当前 Agent 工具 result 中可见的稳定标识符
- `claude agents` 面板会列出所有活跃 + 最近完成的会话
- 本机已启用 experimental agent teams 开关

**待确认**（d-009 V2 或实战中校准）：
- `claude agents` 面板列项的"主键"是 agentId 还是 session-id（外观可能是 UUID）
- 后台运行（`run_in_background: true`）的 agent 是否会出现在面板中（理论上会，因为它们是 Claude Code 启动的子会话）
- agentId 跨终端是否一致（用户的 `claude agents` 终端与 Manager 主会话是否共享 agent 注册表）

**回退方案**（实测发现 agentId 不能直接 attach）：
- 改播报内容为"会话名"（如 `T4 deploy.sh DEPLOY_DRY_RUN`）+ output_file 路径，引导用户用 `claude agents` 列表自己找

## 5. 用户操作手册（嵌入 manager-workflow.md 附录）

```
看子 agent 实时进度
─────────────────
1. 在另一个终端窗口运行：
   claude agents
2. 面板中找到 Manager 播报的 agentId 对应行
3. 按 Enter attach
4. 看完按 ← 返回面板
5. 多次 Ctrl+C 或退出面板回到原终端
```

## 6. 验收（与 r-009 一致）

- [ ] manager-workflow.md Phase 2 / Phase 6 改动落地
- [ ] CLAUDE.md Manager 自检表新增一项
- [ ] 至少 1 次实战验证：用户按指引能成功 attach 任意子 agent
- [ ] 实战发现 agentId 不可用时，启动回退方案

## 7. 不在本设计内（Out of Scope）

- 自动通知（hook 推送子 agent 完成事件到主会话） — 复杂度高、需 hooks 配置 + session 通信
- 子 agent 输出风格规范（Step N/M 进度条等） — 维护成本高，全局影响
- agentd / agentgw / Flutter app 任何代码改动 — R-009 是 Claude Code 工具层适配，不动 phone-talk 产品代码
- Web 端 / 移动端的"子 agent 切换查看"功能 — 那是 R-010 范畴

## 8. 推进路径

R-009 属于"文档+约定"型需求，不需要拆解为多个 dev task。建议直接派 1 个 doc agent 做：

1. 按 §4 修改 `docs/management/manager-workflow.md`
2. 按 §4 修改 `CLAUDE.md`
3. 一次性 commit
4. Manager 自此遵守新约定

预计工作量：S（小）。

## 9. 引用

- 需求：r-009-claude-agent-view-adaptation.md
- Claude Code changelog v2.1.139（feature 引入）
- 痛点示例：R-008 T6 期间 "卡住了几十分钟" 用户问询事件
