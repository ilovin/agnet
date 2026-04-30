---
id: i-007
type: architecture
priority: medium
status: pending
owner: —
worktree: arch-006-jsonrpc-types
---

# JSON-RPC 类型安全

## Parent

架构重构批次3 — 来自 `docs/issues/README.md`

## What to build

将手动构造的 `map[string]any` JSON-RPC 调用替换为类型安全结构，消除 57 处 `map[string]any` 和 22/26 处 `client.call('method', {...})` 的字符串类型风险。

## Vertical slice

- **Schema**: Typed request/response structs（Go + Dart）
- **API**: 类型化 client，编译时检查方法名和参数
- **UI**: 无直接变化（编译时更安全）
- **Tests**: 类型错误应在编译阶段捕获，无需额外 runtime 测试

## Scope

### Backend (Go)
- `agentd/internal/ws/types.go`: 为每个 RPC 方法定义 Request/Response struct
- Handler dispatch table 从 string switch 改为 typed method map
- 参数解析从 `params.(map[string]any)` 改为 `json.Unmarshal` 到 struct

### Gateway (Go)
- `agentgw/internal/ws/types.go`: 同样的类型化改造

### Frontend (Dart)
- `lib/models/rpc_types.dart`: 为每个 RPC 定义 request/response 类
- `lib/services/` 中使用 typed client 替代 `client.call('method', {...})`

## Acceptance criteria

- [ ] 所有 RPC 方法都有对应的 Go Request/Response struct
- [ ] 所有 RPC 方法都有对应的 Dart request/response 类
- [ ] 消除 `agentd/internal/ws/handler.go` 中所有 `map[string]any` 参数构造
- [ ] 消除 `agentapp` 中所有 `client.call('method', {...})` 调用
- [ ] `./scripts/build.sh go` 编译通过
- [ ] `./scripts/test.sh unit` Go 全模块 PASS
- [ ] `./scripts/test.sh flutter` Flutter 全模块 PASS
- [ ] `cd agentapp && flutter analyze` 无新增错误

## Blocked by

None — can start immediately.

## Notes

- 两种方案待决策（HITL）：
  1. **手写 structs** — 简单直接，维护成本高
  2. **代码生成** — 从 schema 定义生成 Go+Dart，前期投入大
- 建议先做手写 structs（i-007 v1），后续迭代到代码生成（i-007 v2）
- 与 i-003 WS Service 层配合更好 — Service 层用 typed struct，handler 只做序列化
