# 显示改进设计文档

## 1. 清理重复节点

### 现状
`~/.agentgw/nodes.yaml` 中存在两个指向同一 `localhost:7373` 的节点：
- `local-agentd` (name: "Local AgentD") — 人类可读，约定俗成
- `a8719f28-...` (name: "Local Agentd") — UUID，误创建产物

### 方案
保留 `local-agentd`，删除 UUID 节点。

---

## 2. 会话名称改进

### 现状
会话名称格式为 `claude-attached-50745`（provider-attached-PID），无法快速识别项目。

### 改进
主标题：`{projectName} ({provider})`
- 从 workDir 提取最后一段目录名作为 projectName
- 例：`/Users/.../phone-talk` → `phone-talk (claude)`

副标题：`Attached · PID 50745 · tmux · 可交互`

### 后端变更（agentd）
- agent.list / session.catalog 返回新增 `projectName` 和 `displayName` 字段
- Attach 时 name 改为 `{projectName}-{provider}-{pid}`

### 前端变更（agentapp）
- 列表 title 优先用 displayName，fallback 到 `{projectName} ({provider})`
- PID/sessionId/terminal 降级到副标题

---

## 3. 工具调用标题改进

### 现状
工具调用只显示 `工具调用: Bash`，看不到参数。

### 改进
后端在 tool_use 事件中增加 `toolSummary` 字段，按工具类型生成：

| 工具 | Summary 模板 | 示例 |
|------|-------------|------|
| Glob | `Glob {pattern} in {path}` | `Glob **/*.go in agentd/` |
| Grep | `Grep /{pattern}/ {glob}` | `Grep /tmux/ **/*.go` |
| Read | `Read {basename}:{offset}-{end}` | `Read scanner.go:1-100` |
| Bash | 取 command 前 60 字符 | `go test ./internal/scanner/... -v` |
| Edit | `Edit {basename}` | `Edit scanner_darwin.go` |
| Write | `Write {basename}` | `Write TEST_SCRIPTS.md` |

前端展示：
- header：`工具调用: {toolName}`
- collapsedPreview：优先用 `toolSummary`

---

## 4. 思考过程标题改进

### 现状
thinking 块 header 永远是固定的 `思考过程`。

### 改进
header 改为 `思考：{topic}`，topic 从内容第一行提取前 20 字符。

示例：
- 内容：`我需要先确认 nodes.yaml 的重复节点...`
- header：`思考：确认重复节点清理方案`
- fallback：`思考过程`
