# Provider Harness 新适配指南（基于 Codex 落地）

## 目标

把一个新的 CLI harness（类似 codex / opencode / hermes）接入到 phone-talk，并保证：

- 可被 scanner 发现并 attach
- 可读历史与实时消息
- 可发送消息且不会“假在线断联”
- 可通过 `deploy.sh local` 部署并用 `agentcli` 验证

---

## 一、最小抽象要求

新 harness 需要在系统里对齐 4 个抽象点：

1. **Provider 识别与启动参数抽象**
- 位置：`agentd/internal/ws/agent_service.go`
- 要求：支持 create/attach/restart 场景下的 provider 命令解析（含 resume 参数规则）

2. **Session 发现抽象（scanner）**
- 要求：能稳定拿到 `PID / WorkDir / SessionFile(or SessionID) / AttachMode / TmuxTarget`
- 关键：tmux attach 场景必须可拿到 pane 目标，否则只能读不能写

3. **历史解析抽象（watcher）**
- 若是 jsonl：实现/复用统一解析入口（role/text/status/tool 事件）
- 若是 DB：实现轮询 watcher，并处理 session switch
- 要求：重启后可 backfill 历史，避免 app 只看到 1-2 条

4. **发送路径抽象（conversation.send）**
- tmux attach：`send-keys`
- 非 tmux attach：按 provider 策略（read-only / restart with message / resume 子命令）
- 要求：明确失败返回，不允许静默吞消息

---

## 二、Codex 这次落地的关键改造

1. **Codex 历史解析并入通用 JSONL 流程**
- `agentd/internal/watcher/claude.go`：补充 codex 事件类型 fallback 解析
- `agentd/internal/agent/manager.go`：`LoadFromStore` 增加 codex 的 JSONL backfill/rebuild

2. **重启后历史一致性修复**
- DB/JSONL 历史量不一致时触发 rebuild，避免 app 端历史缺失

3. **tmux 假在线防护（本次核心）**
- `agentd/internal/ws/handler.go`：对 `codex/hermes` 的 tmux 发送先做 pane 活性校验  
  （CLI 不在 pane 前台时直接报错并标记 stopped，避免“发出去了但其实写进 shell”）

4. **本地部署脚本链路对齐**
- `scripts/install.sh`（不是根目录 `install.sh`）补充保活、重试、端口/HTTP 健康检查
- 保证 `deploy.sh local` 后网关可持续连接

---

## 三、新 harness 接入清单（可直接照做）

1. **定义 provider 启动规则**
- 改 `agent_service.go`：create/restart/resume 参数规则

2. **实现 scanner 识别**
- 返回 `ProcessInfo`（含 `AttachMode/TmuxTarget`）
- 明确 read-only 条件与 reason

3. **实现 watcher**
- 先做“可解析 user/assistant/tool/status”最小闭环
- 再做 session switch / backfill / rebuild

4. **接入 manager 恢复链路**
- `LoadFromStore` 恢复时重建 watcher
- 历史来源冲突时定义优先级并可重建

5. **接入 ws handler 发送链路**
- `conversation.send` 分 provider 分支
- tmux 路径增加“前台进程有效性”检查（至少对易断联 provider 开启）

6. **补测试**
- watcher 解析测试
- LoadFromStore/backfill 测试
- conversation.send 路由/错误测试

7. **部署与运行验证**
- `./scripts/deploy.sh local`
- `agentcli list-agents --json`
- `agentcli history --agent-id <id> --limit 20 --json`
- `agentcli send-message --agent-id <id> --message "<probe>"`

---

## 四、标准验收（必须通过）

1. **连接可用**
- `curl http://localhost:7374/` 返回 `200`

2. **会话可见**
- `list-agents` 中 provider 正确、`sessionId` 非空、`runtimeState=live`

3. **历史可读**
- `history` 能看到连续 `seq`，非空 role/text

4. **发送可达**
- 发送 probe 文本后，`history` 出现对应 user 事件
- 若 CLI 不在前台，必须收到明确错误（不能静默成功）

---

## 五、Codex 本次验证样例

以下 probe 已在当前会话验证成功并入库：

- `ping-from-agentcli-20260530-2050`
- `codex-link-check-20260530-2053`

对应表现：
- `agentcli send-message` 返回成功
- `agentcli history` 出现新增 user 事件（含新 `seq`）

---

## 六、常见故障与定位顺序

1. **“app 看不到新消息”**
- 先看 `agentcli history` 是否有新 `seq`
- 有：app 路由/渲染问题
- 没有：watcher 或发送链路问题

2. **“消息发不进去像断联”**
- 看 `agentd` 日志是否进入 `conversationSend` tmux 分支
- 检查 tmux pane 前台是否已退回 shell

3. **“restart 后 tunnel.offline / 7374 不通”**
- 优先检查 `scripts/install.sh restart` 是否真的保活成功
- 再看 `deploy.sh local` 后续步骤是否覆盖了本地进程状态

---

## 七、建议的后续规范

- 新 provider 必须提供一份 `onboarding + runbook` 文档（按本文结构）
- `conversation.send` 不允许 silent failure，必须结构化报错
- 所有 tmux attach provider 都应逐步统一前台进程活性校验策略
