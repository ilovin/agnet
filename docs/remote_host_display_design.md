# 托管远端（SSH 可达）展示方案设计

## 背景

当前 agentgw 已支持通过 SSH tunnel 连接远程 agentd，但 UI 展示层面与本地节点无区分，用户难以一眼识别会话所在的物理/逻辑位置。

## 目标

- 在节点列表、会话列表、Agent 详情页明确标识**本地 vs 远程**
- 远程节点需展示**主机标识**（hostname/IP/别名）
- 会话/Agent 需继承节点位置信息，避免用户误操作（如在远程节点执行危险命令）

---

## 数据模型扩展

### 后端（agentgw）

节点配置已包含 SSH 相关信息：

```yaml
- id: remote-server-01
  name: 生产服务器 01
  host: 192.168.1.100
  ssh_port: 22
  agentd_port: 7373
  ssh_alias: prod-01  # SSH config alias
```

API 响应扩展：

```json
{
  "id": "remote-server-01",
  "name": "生产服务器 01",
  "host": "192.168.1.100",
  "location": {
    "type": "remote",
    "host": "192.168.1.100",
    "sshAlias": "prod-01",
    "displayLocation": "prod-01 (192.168.1.100)"
  },
  "status": "connected"
}
```

### 远端 Agent 数据透传

agentd 的 session.catalog / agent.list 返回中增加节点来源标记：

```json
{
  "id": "claude-attached-12345",
  "name": "project-x (claude)",
  "nodeLocation": {
    "nodeId": "remote-server-01",
    "displayName": "生产服务器 01",
    "host": "192.168.1.100"
  }
}
```

---

## UI 展示方案

### 1. 节点列表（Dashboard / 节点管理页）

| 元素 | 本地节点 | 远程节点 |
|------|---------|---------|
| 图标 | `Icons.computer` (💻) | `Icons.cloud` / `Icons.storage` (☁️) |
| 主标题 | "Local AgentD" | "生产服务器 01" |
| 副标题 | "localhost:7373" | "prod-01 (192.168.1.100)" |
| 状态徽章 | 绿色圆点 + "本地" | 绿色圆点 + "远程" |

### 2. 会话列表（Session Catalog）

每个会话卡片增加**位置标识条**：

```
┌─────────────────────────────────────┐
│ 📁 project-x (claude)              │
│ ☁️ 生产服务器 01 · 192.168.1.100    │  ← 新增位置条
│ PID 12345 · tmux · 可交互          │
└─────────────────────────────────────┘
```

本地会话不显示位置条，或显示 "💻 本地"。

### 3. Agent 详情页顶部

增加醒目的位置标识：

```
┌─────────────────────────────────────┐
│ ← 返回              [状态: working] │
│                                     │
│ 💻 本地                    [claude] │  ← 本地样式
│                                     │
│ 或者：                              │
│                                     │
│ ☁️ 生产服务器 01            [claude] │  ← 远程样式
│    192.168.1.100:7373              │
└─────────────────────────────────────┘
```

### 4. 消息输入区警示

对于远程会话，输入框上方增加微妙提示：

```
┌─────────────────────────────────────┐
│ ⚠️ 此会话运行在远程主机：生产服务器 01  │
├─────────────────────────────────────┤
│ [消息输入框...                ] [发送]│
└─────────────────────────────────────┘
```

样式：浅黄色背景，小号字体，可关闭/不再提示。

---

## 交互设计

### 节点添加流程

添加远程节点时，要求填写**显示名称**（供 UI 展示）和**主机标识**：

```
[节点名称 *] _______________  (如：生产服务器 01)
[主机地址 *] _______________  (如：192.168.1.100 或 server.company.com)
[SSH 端口  ] _______________  (默认 22)
[SSH 别名  ] _______________  (如：prod-01，使用 ~/.ssh/config)
```

### 快速切换

在 Dashboard 顶部增加位置过滤器：

```
[全部] [本地 💻] [远程 ☁️]
```

### 危险操作二次确认

对于远程会话的以下操作，增加二次确认：
- 停止 Agent
- 重启 Agent
- 删除会话历史

确认弹窗需明确显示远程主机名：

```
确定要停止此 Agent 吗？

Agent: project-x (claude)
位置: ☁️ 生产服务器 01 (192.168.1.100)

此操作将影响远程主机上的进程。

[取消] [确认停止]
```

---

## 实现要点

### 后端变更（agentgw）

1. **node.go / Node 结构体**
   - 增加 `DisplayLocation()` 方法，生成展示用位置字符串
   - 根据 `host` 判断本地/远程（localhost / 127.0.0.1 / ::1 为本地）

2. **handler.go / nodeList**
   - 返回增加 `location` 对象

3. **代理层**
   - 转发 agentd 响应时，注入 `nodeLocation` 字段

### 前端变更（agentapp）

1. **NodeModel 扩展**
   ```dart
   class NodeModel {
     String id;
     String name;
     NodeLocation location;
     bool get isLocal => location.type == 'local';
   }
   
   class NodeLocation {
     String type; // 'local' | 'remote'
     String host;
     String? sshAlias;
     String displayLocation;
   }
   ```

2. **节点列表项组件**
   - 根据 `isLocal` 显示不同图标
   - 显示 `displayLocation` 作为副标题

3. **会话列表项组件**
   - 接收 `nodeLocation` 参数
   - 有值时显示位置条

4. **Agent 详情页**
   - 顶部增加位置 header
   - 远程会话显示输入区警示

---

## 安全考虑

1. **敏感信息脱敏**
   - 如果 host 是 IP，完整显示
   - 如果 host 是域名，完整显示
   - SSH 私钥路径不在 UI 展示

2. **操作隔离**
   - 本地和远程的快捷操作区分（如批量删除仅影响本地）
   - 远程操作增加加载状态（SSH 隧道延迟）

---

## 优先级

| 功能 | 优先级 | 备注 |
|------|-------|------|
| 节点列表区分本地/远程图标 | P0 | 最直观的位置感知 |
| 会话列表显示位置条 | P0 | 避免选错会话 |
| Agent 详情页位置 header | P1 | 强化当前上下文 |
| 远程操作二次确认 | P1 | 安全必要 |
| 输入区警示 | P2 | 体验优化 |
| 位置过滤器 | P2 | 会话多时有用 |
