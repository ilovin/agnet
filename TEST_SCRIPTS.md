# Test Scripts Reference

本文档汇总了所有测试脚本的位置和使用方法，便于后续快速执行测试。

## 目录结构

```
phone-talk/
├── agentd/scripts/          # agentd 专用测试脚本
├── agentgw/scripts/         # agentgw 专用测试脚本
├── agentapp/scripts/test/   # 应用层测试脚本 (Playwright/Node.js)
├── agentapp/scripts/debug/  # 调试/开发辅助脚本
└── TEST_SCRIPTS.md          # 本文档
```

---

## 1. Go 单元测试 (agentd / agentgw)

### agentd 测试

```bash
cd agentd

# 运行所有测试
go test ./... -v -timeout 30s

# 运行特定包测试
go test ./internal/config/... -v
go test ./internal/agent/... -v
go test ./internal/ws/... -v
go test ./internal/scanner/... -v

# 包含集成测试
go test -tags integration ./... -v -timeout 30s
```

**测试文件位置：**
- `agentd/internal/config/config_test.go`
- `agentd/internal/agent/manager_test.go`
- `agentd/internal/agent/claude_integration_test.go`
- `agentd/internal/ws/handler_test.go`
- `agentd/internal/ws/server_test.go`
- `agentd/internal/scanner/scanner_test.go`
- `agentd/internal/eventbuf/eventbuf_test.go`
- `agentd/internal/pty/pty_test.go`
- `agentd/internal/store/store_test.go`
- `agentd/internal/watcher/claude_test.go`
- `agentd/integration_test.go`

### agentgw 测试

```bash
cd agentgw

# 运行所有测试
go test ./... -v -timeout 30s

# 运行特定包测试
go test ./internal/ws/... -v
go test ./internal/node/... -v
go test ./internal/proxy/... -v
```

**测试文件位置：**
- `agentgw/internal/config/config_test.go`
- `agentgw/internal/ws/server_test.go`
- `agentgw/internal/node/manager_test.go`
- `agentgw/internal/nodecfg/nodecfg_test.go`
- `agentgw/internal/proxy/proxy_test.go`
- `agentgw/internal/tunnel/tunnel_test.go`
- `agentgw/internal/deployer/deployer_test.go`

---

## 2. Flutter 测试 (agentapp)

```bash
cd agentapp

# 运行所有测试
flutter test -v

# 运行特定测试文件
flutter test test/widget_test.dart -v
flutter test test/models_test.dart -v

# 运行特定测试用例
flutter test test/widget_test.dart --plain-name "thinking classification" -v
flutter test test/models_test.dart --plain-name "writable tmux session" -v

# 静态分析
flutter analyze
```

**测试文件位置：**
- `agentapp/test/widget_test.dart` - Widget/UI 测试
- `agentapp/test/models_test.dart` - 数据模型测试
- `agentapp/test/ws_client_test.dart` - WebSocket 客户端测试
- `agentapp/test/connection_store_test.dart` - 连接存储测试
- `agentapp/test/nodes_provider_test.dart` - 节点管理测试
- `agentapp/test/conversation_provider_test.dart` - 对话管理测试

---

## 3. Node.js 测试脚本 (agentapp/scripts/test/)

这些脚本用于集成测试和 Playwright UI 测试。

### 先决条件
```bash
cd agentapp
npm install
```

### 脚本列表

| 脚本 | 用途 | 运行方式 |
|------|------|----------|
| `test_conversation_complete.js` | 完整对话流程测试 | `node scripts/test/test_conversation_complete.js` |
| `test_full_conversation.js` | 全量对话测试 | `node scripts/test/test_full_conversation.js` |
| `test_conversation_ui.js` | 对话 UI 测试 | `node scripts/test/test_conversation_ui.js` |
| `test_ui_conversation.js` | UI 对话测试 | `node scripts/test/test_ui_conversation.js` |
| `test_agent_reply.js` | Agent 回复测试 | `node scripts/test/test_agent_reply.js` |
| `test_agent_reply_long.js` | 长回复测试 | `node scripts/test/test_agent_reply_long.js` |
| `test_agent_detail_direct.js` | Agent 详情页直接测试 | `node scripts/test/test_agent_detail_direct.js` |
| `diagnose.js` | 诊断工具 | `node scripts/test/diagnose.js` |

### 快速测试命令

```bash
cd agentapp

# 完整流程测试
node scripts/test/test_conversation_complete.js

# Agent 回复测试
node scripts/test/test_agent_reply.js

# UI 测试
node scripts/test/test_conversation_ui.js
```

---

## 4. agentd 测试脚本 (agentd/scripts/)

| 脚本 | 用途 | 运行方式 |
|------|------|----------|
| `check_nodes.js` | 检查节点状态 | `node agentd/scripts/check_nodes.js` |

---

## 5. agentgw 测试脚本 (agentgw/scripts/)

| 脚本 | 用途 | 运行方式 |
|------|------|----------|
| `check_agents.js` | 检查 Agents | `node agentgw/scripts/check_agents.js` |
| `check_remote_agents.js` | 检查远程 Agents | `node agentgw/scripts/check_remote_agents.js` |
| `check_remote_catalog.js` | 检查远程会话目录 | `node agentgw/scripts/check_remote_catalog.js` |
| `test_attach.js` | 附加会话测试 | `node agentgw/scripts/test_attach.js` |
| `test_attach2.js` | 附加会话测试 V2 | `node agentgw/scripts/test_attach2.js` |

---

## 6. 调试脚本 (agentapp/scripts/debug/)

这些脚本用于开发和调试，不纳入常规测试流程。

```bash
cd agentapp

# 常用调试脚本
node scripts/debug/check_chrome.js       # Chrome 连接检查
node scripts/debug/check_api.js          # API 检查
node scripts/debug/check_nodes.js        # 节点检查
node scripts/debug/check_agents.js       # Agent 检查
```

---

## 7. 快速测试清单

### 每次代码变更后必测

```bash
# 1. agentd 核心测试
cd agentd && go test ./internal/scanner/... ./internal/agent/... ./internal/ws/... -v

# 2. agentgw 核心测试
cd agentgw && go test ./internal/ws/... -v

# 3. Flutter 模型测试
cd agentapp && flutter test test/models_test.dart -v

# 4. Flutter Widget 测试
cd agentapp && flutter test test/widget_test.dart -v

# 5. 静态分析
cd agentapp && flutter analyze
```

### 完整验收测试

```bash
# 1. 全量单元测试
cd agentd && go test ./... -v -timeout 30s
cd agentgw && go test ./... -v -timeout 30s
cd agentapp && flutter test -v

# 2. Node.js 集成测试
cd agentapp && node scripts/test/test_conversation_complete.js

# 3. Chrome 验收（手动）
# - 打开 http://localhost:18086
# - 验证会话附加、消息发送、思考过程折叠等功能
```

---

## 8. 新增测试规范

1. **Go 测试**: 放在对应包的 `*_test.go` 文件中
2. **Flutter 测试**: 放在 `agentapp/test/` 目录下
3. **Node.js 测试**: 放在 `agentapp/scripts/test/` 目录下
4. **调试脚本**: 放在 `agentapp/scripts/debug/` 目录下
5. **组件专用脚本**: 放在 `agentd/scripts/` 或 `agentgw/scripts/` 目录下

---

## 9. .gitignore 规则

```gitignore
# Debug artifacts (已存在于 agentapp/.gitignore)
/scripts/debug/
*.png

# Test outputs
*.log
coverage/
```

注：`agentapp/scripts/test/` 中的脚本应纳入版本控制，`agentapp/scripts/debug/` 已配置为 gitignore。
