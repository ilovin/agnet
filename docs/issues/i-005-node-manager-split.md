---
id: i-005
type: architecture
priority: medium
status: in-progress
owner: dev agent
worktree: arch-007-node-manager
---

# NodeManager 拆分

## Parent

架构重构批次1 — 来自 `docs/issues/README.md`

## What to build

将 `agentgw/internal/node/manager.go` (590 行, 17 方法 + 8 package 函数) 拆分为 3 个专注模块：节点元数据、SSH 隧道、WS 代理。

## Vertical slice

- **Schema**: 3 个新模块的接口定义
- **API**: NodeManager 保留公共 API，内部委托
- **UI**: 无（纯后端重构）
- **Tests**: 每个模块独立单元测试（隧道和代理需要 mock 接口）

## Module split

1. **NodeRegistry** — 节点元数据管理（Add, Remove, Rename, List, Get, persist/load）
2. **TunnelManager** — SSH 连接生命周期（Connect, Disconnect, health check, port forwarding）
3. **ProxyManager** — WebSocket 代理生命周期（SetProxy, GetProxy, handle disconnect, event forwarding）

## Acceptance criteria

- [ ] 3 个新文件：`node_registry.go`, `tunnel_manager.go`, `proxy_manager.go`
- [ ] `TunnelManager` 和 `ProxyManager` 有接口定义（便于 mock 测试）
- [ ] NodeRegistry 是纯内存 + YAML 持久化，无需网络
- [ ] TunnelManager 封装 `golang.org/x/crypto/ssh`
- [ ] ProxyManager 封装 `gorilla/websocket` 客户端
- [ ] `./scripts/test.sh unit` 中 agentgw 模块 PASS
- [ ] `cd agentgw && go test ./internal/node/` 开发阶段可用
- [ ] `./scripts/test.sh unit` 全模块无回归
- [ ] NodeManager 公共 API 不变

## Blocked by

None — can start immediately.

## Notes

- `Connect` 当前 90 行混合：创建 SSH client → 建立端口转发 → 创建 WS proxy → 设置事件回调 → 处理断开清理
- 拆分后：`TunnelManager.Connect()` 返回 `net.Conn`，`ProxyManager` 用此 conn 建立 WS
