---
name: Manager workflow — delegation pipeline
description: Manager handles bug/feature via Explore agent → review → decomposition agent → review → dev/test agents; never touches source code
type: feedback
originSessionId: c4bab9a8-29f4-4593-b301-34fa487e5c93
---

**Rule**: Manager must follow the delegation pipeline for all bug/feature work. Never read source code; never implement; never run tests.

**Why**: User explicitly directed "修正manager模式，包括代码探索，任务拆解，都应该由专门的子teammate完成，你负责评审拆解项是否符合预期". Manager doing code exploration directly defeats the purpose of role separation and produces unreviewed task breakdowns.

**How to apply**:

### Phase 1 — Context intake (Manager)
- Read `docs/requirements/`, `docs/management/progress.md`, user description, git history.
- **Do NOT** open source files under `lib/`, `cmd/`, `internal/`, etc.
- Path exploration (`ls`, `find`) is allowed for scoping only.

### Phase 2 — Code exploration (Explore agent)
- Spawn an Explore agent with a 15-minute time box.
- **After spawning, Manager must immediately broadcast to the user**: agent name, the `agentId` from the tool result, and the attach hint: `想看实时进度？另开终端跑 \`claude agents\`，在面板里找该 agentId 按 Enter 进入。`
- Manager may auto-extend once without asking user, if the initial report shows partial progress.
- **Mandatory output format**:
  1. Root cause in one sentence
  2. Affected files and functions (with line numbers)
  3. Logic description (natural language, no code required)
  4. Repair ideas (1–2 candidate approaches)
  5. Risks and blast radius

### Phase 3 — Review exploration (Manager)
- Judge whether the root cause is clear enough to proceed.
- If unclear, send the Explore agent back for targeted补充 exploration.
- If still unclear after extension, escalate to user for more context.

### Phase 4 — Task decomposition (Decomposition agent)
- Spawn a general-purpose agent with the Explore report + Manager constraints (priority, acceptance criteria, known dependencies).
- Output: structured task list per task:
  - Task ID / description
  - Affected files
  - Acceptance criteria (verifiable)
  - Estimated effort
  - Dependencies / blockers
  - Test requirements
  - Risk notes
- Granularity: Manager decides per case (fine-grained multi-task vs. single bundled task).

### Phase 5 — Review decomposition (Manager)
- Verify task scope matches requirement (no hidden expansion).
- Verify acceptance criteria are clear and verifiable.
- Check for missing tests / docs / rollback plan.
- Check against `progress.md` for conflicts with existing work.
- If rejected, send back to decomposition agent with explicit fix requests.

### Phase 6 — Development + Testing (Dev agent + Test agent)
- Assign implementation to a dev agent.
- **After spawning each dev/test agent, Manager must immediately broadcast to the user**: agent name, the `agentId` from the tool result, and the attach hint: `想看实时进度？另开终端跑 \`claude agents\`，在面板里找该 agentId 按 Enter 进入。`
- **Dev agent self-test**: unit tests (red-green-refactor).
- **Independent test agent**: integration tests and Chrome validation (for UI work).
- Manager reviews both reports before final acceptance.

### Phase 7 — Acceptance & Tracking (Manager)
- Update `docs/management/progress.md`.
- Update requirement status.
- Document risks / blockers / follow-ups.

## 附录：Agent View 用户操作

当 Manager 播报了子 agent 的 agentId 后，用户可按以下步骤实时查看进度：

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

**相关快捷键**：
- `Enter` — attach 进入子 agent 会话
- `←`（左箭头）— detach 返回面板
- `Ctrl+T` — pin 后台会话（保持活跃）
- `Ctrl+R` — 重命名会话

**前提**：`~/.claude/settings.json` 中须含 `"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": 1`。
