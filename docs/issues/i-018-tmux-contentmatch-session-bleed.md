---
name: i-018 tmux contentmatch session bleed across parallel agents
description: agentd 通过 tmux pane content match 把 jsonl session 绑定到 tmux target，多个 claude 子 agent 在不同 worktree 并发跑时输出归属错乱（串 session）；R-016 多 tab 视图开工前必须先修
type: bug
priority: critical
status: open
---

# i-018 tmux contentmatch 跨 session 串扰（多并发 agent 场景）

**Date**: 2026-05-27
**Reporter**: Manager（基于用户反馈 + R-016 调研）
**Status**: Open
**Priority**: Critical
**Component**: agentd / scanner / watcher

## 问题描述

当 Manager 在多个 worktree 并发派出 ≥ 2 个 claude 子 agent 时，agentd 用来把 jsonl session 文件绑定到 tmux pane 的 **content-match** 机制会出现归属错乱：

- A 进程在 worktree-A 跑，B 进程在 worktree-B 跑，两个进程都在终端可见
- agentd 的 watcher 把 worktree-A 的 jsonl 输出错误绑定到 worktree-B 的 tmux pane（或反过来）
- 表现：app 端 / `claude agents` 看到的 A 会话里混进 B 的消息流，或两个会话内容时不时互换

并发数越多（5 个 dev agent 是常见场景）问题越严重。R-016（agentapp 多 agent tab 视图）未修此 bug 前不能开工，否则 app 端 tab 显示的内容也会错乱。

## Root Cause（基于代码）

**位置**：`agentd/internal/scanner/content_match.go` + `agentd/internal/watcher/claude.go:322-330`

**机制**：
1. 每个 ClaudeWatcher 持有一个 `tmuxTarget`（如 `0:1.2`），代表它绑定的 tmux pane
2. 当多个 jsonl session 候选并存时（同一 projectDir 下），watcher 调用 `contentMatchFromCandidates(tmuxTarget, candidates, 5)`
3. `contentMatchSession` 用 `tmux capture-pane -t <target>` 抓 pane 文本，与每个 candidate jsonl 末尾提取的 fingerprint 做模糊匹配，分数最高的 candidate 即被绑定（`content_match.go:533-547`）

**为什么会串**：

- **fingerprint 是文本指纹，不是身份标识**：fingerprint 是从 jsonl assistant/user 消息内容里提取的短 token（`extractFingerprintsUncached`，`content_match.go:297-408`）。两个 worktree 上的 agent 如果都在做"修 tmux contentmatch"这种相似任务，pane 里出现的 token（"contentmatch"、"session"、"agentd" 等）几乎一样，fingerprint 互相之间能匹中
- **tmux pane 内容会被多 agent 输出污染**：用户可能在 tmux 里来回切 pane 看不同 agent，capture-pane 抓到的 200 行历史里混入了多个 agent 的输出片段
- **modulo cache TTL 把错误结果"锁死" 30 秒**：strong-match 的结果会被缓存 30 秒（`contentMatchCacheTTL`），一旦初次绑定到错误 candidate，后续 30 秒内即使有更好的信号也不重算
- **margin ratio 临界**：当两个 candidate 分数接近时（`marginRatio < contentMatchMinMarginRatio = 0.30`），系统会按 `LastActivity` 时间戳做 tie-break（`considerScore`，`content_match.go:645-669`）。多 agent 同时活跃时 LastActivity 时间窗很近，tie-break 不稳定
- **进程退出后旧 jsonl 残留**：参考 i-016 §3，进程退出后 store 里仍有旧 agent 记录，新进程的 candidate 列表里依然包含旧 jsonl，content match 容易把活进程绑到死 jsonl 上

## 复现步骤

1. 启动 agentd
2. 在 worktree-A 启动 claude 进程，让它跑一段对话产生 jsonl 内容
3. 在 worktree-B 启动 claude 进程，跑相似主题的对话（如都在讨论 agentd / tmux）
4. 观察 app dashboard 上两个 agent 卡片的实时输出
5. 现象：A agent 卡片偶尔显示 B 会话的消息；切换 tmux pane 焦点后，归属可能再次翻转

## 修复路径（多方案对比，未拍板）

### 方案 P1 — Pane-id 直接路由（推荐主方向）

**思路**：claude 进程启动时把 pane id（`$TMUX_PANE` 环境变量，每个 pane 唯一）写入 `~/.claude/sessions/<PID>.json`，agentd 读这个文件直接拿到权威 PID → pane 映射，**完全跳过 content match**。

**已有部分基础**：`pidMapSessionFile`（`claude.go:357-384`）已经在用 `~/.claude/sessions/<PID>.json` 做 PID → sessionId 解析，扩展加一个 `tmuxPane` 字段即可。

**代价**：需要 claude CLI 配合写 `tmuxPane` 字段（不在我们仓库内）；或 agentd 启动 claude 时自己注入并维护。前者依赖上游版本；后者需要在 agentd 启动 claude 的 wrapper（如果有）里加一层。

### 方案 P2 — 显式 marker 注入（unique pipe-id）

**思路**：agentd 启动每个 claude 进程时给它注入一个唯一的环境变量（如 `PHONETALK_AGENT_MARKER=<uuid>`），让 claude 在第一条 system 消息里 echo 这个 marker。content match 阶段优先按 marker 字符串完全匹配，命中即绑定；fingerprint fuzzy 仅作 fallback。

**代价**：marker 必须落到 jsonl 里（claude 自身可能不会主动把环境变量写到 jsonl）；如果只能落到 tmux pane 而不能落到 jsonl，则方向反了 — 我们要从 pane 反推 jsonl，需要在 jsonl 里有 anchor。需调研可行性。

### 方案 P3 — 重命名 pane title 作为绑定键

**思路**：agentd 在派 agent 时给对应 tmux pane 改名（`tmux select-pane -T <agentId>`），watcher 抓 pane 时用 `tmux display -t <target> -p '#{pane_title}'` 作权威标识，content match 退化为辅助。

**代价**：需要 agentd 拥有 tmux 控制权（已经在用 capture-pane 应该可以扩展）；用户手动改 pane title 会破坏；多 pane 共享一个 claude 进程的边缘场景需要约定。

### 方案 P4 — 完全抛弃 content match，依赖文件系统线索

**思路**：彻底放弃 tmux pane 路由，转用：
- `/proc/<pid>/fd` 反查 claude 进程实际打开的 jsonl 文件（Linux 可用，macOS 需 `lsof`）
- `~/.claude/sessions/<PID>.json` 的 PID → sessionId 映射（已有）
- worktree path / projectDir 隔离（每个 worktree 是独立 projectDir，jsonl 不会跨目录）

**代价**：macOS 上 `/proc` 不可用，需依赖 `lsof -p <pid>` 实测；性能可能不如 capture-pane 快；某些 claude 启动方式（PTY 间接）拿不到 fd。

### 方案 P5 — 临时降级缓解（不推荐）

**思路**：
- 把 `contentMatchCacheTTL` 从 30s 缩到 5s
- 提高 `contentMatchMinMarginRatio` 到 0.50（要求更明显的差异才接受）
- 多并发场景下宁可 reject 也不绑定错误

**代价**：reject 后 watcher 会 fallback 到"最活跃 candidate"，更可能错；且无法根本解决相似任务的串扰；只是减轻症状。仅做应急 patch。

### 推荐组合

**短期（缓解）**：P5（缩 cache TTL + 紧 margin）+ i-016 死亡进程清理（减少候选列表中的死 jsonl）。
**中期（根治）**：P4 的 fd / pid-map 路径作主路由 + P1 在 macOS 上用 lsof 兜底；content match 仅在 fd / pid-map 都失败时作最后 fallback。
**长期（架构）**：跟 claude 上游协商在 `~/.claude/sessions/<PID>.json` 里加 `tmuxPane` 字段（P1），让 agentd 完全跳过 content match。

## Acceptance Criteria

- [ ] 在 5 个并发 worktree dev agent 场景下，每个 agent 的 watcher 绑定的 jsonl 与 `~/.claude/sessions/<PID>.json` 里的 sessionId 始终一致（无错绑）
- [ ] 在 5 个并发场景下，跑 30 分钟相似主题的对话，每次 capture-pane 命中的 candidate 与 PID 真实持有的 jsonl fd 一致（`lsof` 可验证）
- [ ] 一个 agent 退出后立即起新 agent，新 agent 不会被绑定到旧 agent 的 jsonl
- [ ] R-016 在此 bug closed 后能稳定显示 5 tab 内容（验收标准合并到 R-016）
- [ ] 单 agent 场景行为不变（无回归）
- [ ] 现有 `claude.go` / `manager.go` 单测通过 + 新增多并发场景测试覆盖

## Out of Scope（本 issue 不做）

- agentapp 端的多 tab UI（属 R-016）
- claude CLI 自身的 jsonl 字段扩展（属上游）
- 死亡进程清理 / PeriodicScan 间隔（属 i-016）

## 关联

- 阻塞需求：[[r-016-app-multi-agent-tab-view]]
- 相邻问题：[[i-016-periodic-scan-dead-process-cleanup]]（死 jsonl 残留导致 content match 候选池污染）、[[i-012-clear-tmux-interaction-followup]]（`currentBound()` 在切换时机上的相似 bug）
- 关键代码：
  - `agentd/internal/scanner/content_match.go:533-741`（contentMatchSession 主流程）
  - `agentd/internal/watcher/claude.go:322-330`（watcher 调用 content match 的入口）
  - `agentd/internal/watcher/claude.go:357-384`（pidMapSessionFile，已有的更可靠路径）
- PRD：[[multi-agent-tab-view-prd]] §阶段 1
