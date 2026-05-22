# Hermes Provider Implementation Plan

**Date**: 2026-05-21
**Branch**: worktree-hermes-support
**Status**: Ready for Development

---

## Architecture Decision

Hermes integrates as an **HTTP-based provider** that communicates with the Hermes Gateway API (OpenAI-compatible) at `127.0.0.1:8642`, rather than via PTY/stdin like Claude and OpenCode.

---

## Phase 1: Hermes HTTP Client (`agentd/internal/hermesclient/`)

### Files
- `agentd/internal/hermesclient/client.go`
- `agentd/internal/hermesclient/client_test.go`

### Types
```go
type Client struct {
    BaseURL    string           // default "http://127.0.0.1:8642"
    APIServerKey string         // from config or env
    HTTPClient *http.Client
}

func NewClient(baseURL, apiKey string) *Client
func (c *Client) ChatCompletion(ctx context.Context, sessionID string, messages []Message) (<-chan ResponseChunk, error)
func (c *Client) GetCapabilities(ctx context.Context) (*Capabilities, error)
func (c *Client) GetHistory(ctx context.Context, sessionID string) ([]ConversationEvent, error)
func (c *Client) IsHealthy(ctx context.Context) bool

type Message struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

type ResponseChunk struct {
    Text      string
    Done      bool
    SessionID string // from X-Hermes-Session-Id header
}
```

### SSE Decoding
- Parse `data:` lines from SSE stream
- Extract assistant content from OpenAI-compatible chunks
- Stream chunks to caller via channel

---

## Phase 2: Scanner Integration (`agentd/internal/scanner/`)

### Changes in `scanner.go`
1. `detectProvider()`: add `case strings.HasPrefix(base, "hermes")`
2. `hasAIAgentAncestor()`: add `|| parent.Provider == "hermes"`
3. `finalizeProcessScan()`: add hermes branch (no session file to find; leave SessionID empty)
4. `AttachReadOnlyReason()`: for hermes, always return "" (HTTP communication doesn't need tmux/PTY)

---

## Phase 3: AgentService (`agentd/internal/ws/agent_service.go`)

### Changes in `ResolveLaunch()`
Add `case "hermes":`:
- `resolvedCmd = s.FindExecutable("hermes")`
- `resolvedArgs = []string{"gateway", "run"}` (if auto-start)
- If sessionID provided: pass via env or future config

Note: Hermes gateway auto-start is Phase 2. For Phase 1, assume gateway is already running.

---

## Phase 4: ProcessManager (`agentd/internal/agent/process_manager.go`)

### Decision
Hermes **does not go through PTY spawn** in ProcessManager. Instead:
- If hermes gateway is already running: `Create` just records the agent with status=Idle
- If auto-start needed (Phase 2): start `hermes gateway run` via `pty.Spawn`

For Phase 1 MVP, hermes skips ProcessManager.Create entirely and goes straight to HTTP client setup in Manager.

---

## Phase 5: Handler WebSocket Methods (`agentd/internal/ws/handler.go`)

### conversation.send (line ~688)
Add hermes branch before the generic PTY path:
```go
if ag.Provider == "hermes" {
    return h.hermesSend(req, ag, message, imageFiles, imagePaths)
}
```

`hermesSend` method:
1. Record user message to EventBuffer
2. Build message history from EventBuffer
3. Call `hermesclient.ChatCompletion(ctx, sessionID, messages)`
4. Iterate SSE chunks, broadcast `conversation.message` events with `role=assistant`
5. Store final response in EventBuffer

### conversation.history (line ~900)
Add hermes branch:
```go
if ag.Provider == "hermes" {
    return h.hermesHistory(req, ag)
}
```

Load from `hermesclient.GetHistory()` or fall back to EventBuffer.

### session.attach
If hermes process found by scanner, create agent with provider="hermes" and discover gateway URL.

---

## Phase 6: Manager Integration (`agentd/internal/agent/manager.go`)

- Add `hermesClient *hermesclient.Client` field (lazy init)
- `Attach()` for hermes: set status=Idle, no PTY process
- `hermesClient()` accessor that creates client on first use

---

## Phase 7: Flutter App (`agentapp`)

- Add "hermes" to provider name display mapping
- No special UI needed if SSE events normalize to existing `conversation.message`

---

## File Change Summary

| File | Action | Lines |
|------|--------|-------|
| `agentd/internal/scanner/scanner.go` | Edit | +8 lines |
| `agentd/internal/ws/agent_service.go` | Edit | +10 lines |
| `agentd/internal/ws/handler.go` | Edit | +40 lines (hermesSend, hermesHistory) |
| `agentd/internal/ws/rpc_types.go` | None | Already generic enough |
| `agentd/internal/agent/manager.go` | Edit | +15 lines (hermes client accessor) |
| `agentd/internal/agent/process_manager.go` | Minimal | Skip PTY for hermes |
| `agentd/internal/hermesclient/client.go` | **New** | ~150 lines |
| `agentd/internal/hermesclient/client_test.go` | **New** | ~100 lines |
| `agentd/internal/ws/handler_hermes_test.go` | **New** | ~80 lines (integration) |
| `agentapp/lib/...` | Edit | Provider label |

---

## Test Strategy (TDD)

1. **hermesclient_test.go**: Mock HTTP server + SSE stream → verify chunk parsing
2. **scanner_test.go**: Test `detectProvider("hermes")` and `detectProvider("hermes-agent")`
3. **handler_hermes_test.go**: Integration test with mock gateway
4. Build: `scripts/build.sh`
5. Unit tests: `go test ./agentd/internal/hermesclient/...`

---

## Deployment Notes

1. Oracle machine needs agentd deployed first: `scripts/deploy.sh oracle`
2. Hermes gateway must be configured with `API_SERVER_HOST=0.0.0.0` + `API_SERVER_KEY`
3. If gateway not configured, agentd will display "gateway unreachable" status

---

## Phase Breakdown

- **Phase 1-2** (hermesclient + scanner): Core foundation, can be tested in isolation
- **Phase 3-5** (service + handler): Integration with existing agent flow
- **Phase 6-7** (manager + app): Polish and end-to-end
