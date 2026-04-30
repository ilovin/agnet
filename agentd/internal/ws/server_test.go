package ws_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/scanner"
	"github.com/phone-talk/agentd/internal/store"
	"github.com/phone-talk/agentd/internal/ws"
)

func newTestServer(t *testing.T) (*httptest.Server, *ws.Server) {
	t.Helper()
	s, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	mgr := agent.NewManager(s, t.TempDir())
	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { ts.Close(); s.Close() })
	return ts, srv
}

func dialWS(t *testing.T, ts *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial(u, header)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func readEvent(t *testing.T, conn *websocket.Conn, method string) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var resp map[string]any
		if err := conn.ReadJSON(&resp); err != nil {
			t.Fatalf("read event failed: %v", err)
		}
		gotMethod, _ := resp["method"].(string)
		if gotMethod != method {
			continue
		}
		return resp
	}
}

func writeCCSwitchFixture(t *testing.T, home string, providers []string, currentProviderID, runtimeProviderID string) {
	t.Helper()
	ccDir := filepath.Join(home, ".cc-switch")
	if err := os.MkdirAll(ccDir, 0o755); err != nil {
		t.Fatalf("mkdir cc-switch dir: %v", err)
	}
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir claude dir: %v", err)
	}

	dbPath := filepath.Join(ccDir, "cc-switch.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open cc-switch db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	initSQL := `CREATE TABLE IF NOT EXISTS providers (
		id TEXT NOT NULL,
		app_type TEXT NOT NULL,
		name TEXT NOT NULL,
		settings_config TEXT NOT NULL,
		website_url TEXT,
		category TEXT,
		created_at INTEGER,
		sort_index INTEGER,
		notes TEXT,
		icon TEXT,
		icon_color TEXT,
		meta TEXT NOT NULL DEFAULT '{}',
		is_current BOOLEAN NOT NULL DEFAULT 0,
		in_failover_queue BOOLEAN NOT NULL DEFAULT 0,
		cost_multiplier TEXT NOT NULL DEFAULT '1.0',
		limit_daily_usd TEXT,
		limit_monthly_usd TEXT,
		provider_type TEXT,
		PRIMARY KEY (id, app_type)
	)`
	if _, err := db.Exec(initSQL); err != nil {
		t.Fatalf("init cc-switch db: %v", err)
	}

	for i, id := range providers {
		isCurrent := 0
		if id == currentProviderID {
			isCurrent = 1
		}
		baseURL := fmt.Sprintf("https://%s.example.com", id)
		authToken := fmt.Sprintf("token-%s", id)
		model := fmt.Sprintf("model-%s", id)
		config := fmt.Sprintf(`{"env":{"ANTHROPIC_BASE_URL":"%s","ANTHROPIC_AUTH_TOKEN":"%s"},"model":"%s"}`,
			baseURL, authToken, model,
		)
		if _, err := db.Exec(
			`INSERT INTO providers (id, app_type, name, settings_config, sort_index, is_current) VALUES (?, ?, ?, ?, ?, ?)`,
			id, "claude", id, config, i, isCurrent,
		); err != nil {
			t.Fatalf("insert provider %s: %v", id, err)
		}
	}

	settings := map[string]any{}
	if runtimeProviderID != "" {
		settings = map[string]any{
			"env": map[string]any{
				"ANTHROPIC_BASE_URL":   fmt.Sprintf("https://%s.example.com", runtimeProviderID),
				"ANTHROPIC_AUTH_TOKEN": fmt.Sprintf("token-%s", runtimeProviderID),
			},
			"model": fmt.Sprintf("model-%s", runtimeProviderID),
		}
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
}

var rpcIDCounter int

func rpc(conn *websocket.Conn, method string, params any) map[string]any {
	rpcIDCounter++
	id := rpcIDCounter
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	_ = conn.WriteJSON(req)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var resp map[string]any
		if err := conn.ReadJSON(&resp); err != nil {
			return map[string]any{"error": map[string]any{"message": err.Error()}}
		}
		// Skip push events (no id field) and responses with mismatched ids
		respID, hasID := resp["id"]
		if !hasID {
			continue // This is a push event, skip it
		}
		if respFloat, ok := respID.(float64); ok && int(respFloat) == id {
			return resp
		}
	}
}

func rpcWithEvent(t *testing.T, conn *websocket.Conn, method string, params any, eventMethod string) (map[string]any, map[string]any) {
	t.Helper()
	rpcIDCounter++
	id := rpcIDCounter
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("write rpc failed: %v", err)
	}

	var resp map[string]any
	var event map[string]any
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for resp == nil || event == nil {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read rpc/event failed: %v", err)
		}
		if gotMethod, _ := msg["method"].(string); gotMethod == eventMethod {
			event = msg
			continue
		}
		respID, hasID := msg["id"]
		if !hasID {
			continue
		}
		if respFloat, ok := respID.(float64); ok && int(respFloat) == id {
			resp = msg
		}
	}
	return resp, event
}

func setupFakeClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "claude")
	script := `#!/bin/sh
input="$(cat)"
printf '{"type":"system","subtype":"init","session_id":"ses_fake"}\n'
if [ -n "$input" ]; then
  text=$(printf "%s" "$input" | tr -d '\n')
  printf '{"type":"stream_event","event":{"type":"message_start"}}\n'
  printf '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text_delta":"echo:%s"}}}\n' "$text"
  printf '{"type":"stream_event","event":{"type":"message_stop"}}\n'
fi
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestAuthRejectsInvalidToken(t *testing.T) {
	ts, _ := newTestServer(t)
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	header := http.Header{"Authorization": {"Bearer wrongtoken"}}
	_, resp, _ := websocket.DefaultDialer.Dial(u, header)
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got: %v", resp)
	}
}

func TestAgentList(t *testing.T) {
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.list", nil)
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result := resp["result"]
	b, _ := json.Marshal(result)
	var agents []any
	json.Unmarshal(b, &agents)
	if agents == nil {
		agents = []any{}
	}
	_ = agents
}

func TestAgentListKeepsDistinctPIDsForSharedSession(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	sessionFile := filepath.Join(t.TempDir(), "attached.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	if _, err := mgr.Attach(scanner.ProcessInfo{PID: 1001, Provider: "claude", WorkDir: t.TempDir(), SessionFile: sessionFile}); err != nil {
		t.Fatalf("attach first agent: %v", err)
	}
	if _, err := mgr.Attach(scanner.ProcessInfo{PID: 1002, Provider: "claude", WorkDir: t.TempDir(), SessionFile: sessionFile}); err != nil {
		t.Fatalf("attach second agent: %v", err)
	}

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.list", nil)
	if resp["error"] != nil {
		t.Fatalf("agent.list error: %v", resp["error"])
	}
	b, _ := json.Marshal(resp["result"])
	var agents []map[string]any
	if err := json.Unmarshal(b, &agents); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	seen := map[int]bool{}
	for _, ag := range agents {
		pid, _ := ag["pid"].(float64)
		seen[int(pid)] = true
		if got := ag["sessionId"]; got != "attached" {
			t.Fatalf("expected sessionId=attached, got %#v", got)
		}
	}
	if !seen[1001] || !seen[1002] {
		t.Fatalf("expected both pids to remain visible, got %#v", seen)
	}
}

func TestAgentCreateAndList(t *testing.T) {
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.create", map[string]any{
		"name":    "test",
		"cmd":     "echo",
		"args":    []string{"hello"},
		"workDir": t.TempDir(),
	})
	if resp["error"] != nil {
		t.Fatalf("create error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp["result"])
	}
	id, ok := result["id"].(string)
	if !ok || id == "" {
		t.Errorf("expected non-empty id in result")
	}

	listResp := rpc(conn, "agent.list", nil)
	b, _ := json.Marshal(listResp["result"])
	var agents []map[string]any
	json.Unmarshal(b, &agents)
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
}

func TestAgentListIncludesDerivedStateFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCCSwitchFixture(t, home, []string{"provider-a"}, "provider-a", "provider-a")

	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.create", map[string]any{
		"name":      "claude-agent",
		"provider":  "claude",
		"workDir":   t.TempDir(),
		"sessionId": "sess-123",
	})
	if resp["error"] != nil {
		t.Fatalf("create error: %v", resp["error"])
	}

	listResp := rpc(conn, "agent.list", nil)
	if listResp["error"] != nil {
		t.Fatalf("agent.list error: %v", listResp["error"])
	}
	b, _ := json.Marshal(listResp["result"])
	var agents []map[string]any
	if err := json.Unmarshal(b, &agents); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	ag := agents[0]
	if got := ag["sessionId"]; got != "sess-123" {
		t.Fatalf("expected sessionId sess-123, got %#v", got)
	}
	if got := ag["runtimeState"]; got != "exited" {
		t.Fatalf("expected runtimeState=exited, got %#v", got)
	}
	if got := ag["sessionState"]; got != "resumable" {
		t.Fatalf("expected sessionState=resumable, got %#v", got)
	}
	if got := ag["sessionControl"]; got != "rebindable" {
		t.Fatalf("expected sessionControl=rebindable, got %#v", got)
	}
	if got := ag["providerState"]; got != "synced" {
		t.Fatalf("expected providerState=synced, got %#v", got)
	}
	if got := ag["providerScope"]; got != "standalone" {
		t.Fatalf("expected providerScope=standalone, got %#v", got)
	}
	if got := ag["providerWriteMode"]; got != "writable" {
		t.Fatalf("expected providerWriteMode=writable, got %#v", got)
	}
	if got := ag["permissionMode"]; got != "bypassPermissions" {
		t.Fatalf("expected permissionMode=bypassPermissions, got %#v", got)
	}
}

func TestAgentListIncludesReadOnlyForAttachedAgents(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCCSwitchFixture(t, home, []string{"provider-a"}, "provider-a", "provider-a")
	sessionFile := filepath.Join(t.TempDir(), "attached.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	if _, err := mgr.Attach(scanner.ProcessInfo{
		PID:         123,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: sessionFile,
		Args:        []string{"--dangerously-skip-permissions"},
	}); err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.list", nil)
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}

	b, _ := json.Marshal(resp["result"])
	var agents []map[string]any
	if err := json.Unmarshal(b, &agents); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if got, _ := agents[0]["readOnly"].(bool); !got {
		t.Fatalf("expected readOnly=true, got %#v", agents[0]["readOnly"])
	}
	if got := agents[0]["sessionControl"]; got != "read_only" {
		t.Fatalf("expected sessionControl=read_only, got %#v", got)
	}
	if got := agents[0]["providerScope"]; got != "root" {
		t.Fatalf("expected providerScope=root, got %#v", got)
	}
	if got := agents[0]["providerWriteMode"]; got != "read_only" {
		t.Fatalf("expected providerWriteMode=read_only, got %#v", got)
	}
	if got := agents[0]["providerReadOnlyReason"]; got != "attached runtime cannot guarantee immediate provider switch" {
		t.Fatalf("unexpected providerReadOnlyReason: %#v", got)
	}
	if got := agents[0]["permissionMode"]; got != "bypassPermissions" {
		t.Fatalf("expected permissionMode=bypassPermissions, got %#v", got)
	}
}

func TestAgentStatusChangedEventIncludesDerivedFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCCSwitchFixture(t, home, []string{"provider-a"}, "provider-a", "provider-a")

	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	createResp := rpc(conn, "agent.create", map[string]any{
		"name":      "claude-agent",
		"provider":  "claude",
		"workDir":   t.TempDir(),
		"sessionId": "sess-evt",
	})
	if createResp["error"] != nil {
		t.Fatalf("create error: %v", createResp["error"])
	}
	createResult, ok := createResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected create result map, got %T", createResp["result"])
	}
	agentID, _ := createResult["id"].(string)
	_ = rpc(conn, "agent.list", nil)

	resp, event := rpcWithEvent(t, conn, "agent.rename", map[string]any{
		"agentId": agentID,
		"name":    "renamed-agent",
	}, "agent.status_changed")
	if resp["error"] != nil {
		t.Fatalf("rename error: %v", resp["error"])
	}

	params, ok := event["params"].(map[string]any)
	if !ok {
		t.Fatalf("expected params map, got %T", event["params"])
	}
	if got := params["agentId"]; got != agentID {
		t.Fatalf("expected agentId %s, got %#v", agentID, got)
	}
	if got := params["name"]; got != "renamed-agent" {
		t.Fatalf("expected name in event, got %#v", got)
	}
	if got := params["status"]; got != "idle" {
		t.Fatalf("expected status=idle, got %#v", got)
	}
	if got := params["sessionId"]; got != "sess-evt" {
		t.Fatalf("expected sessionId=sess-evt, got %#v", got)
	}
	if got, ok := params["pid"].(float64); !ok || got != 0 {
		t.Fatalf("expected pid=0 for inactive agent, got %#v", params["pid"])
	}
	if got := params["runtimeState"]; got != "exited" {
		t.Fatalf("expected runtimeState=exited, got %#v", got)
	}
	if got := params["sessionState"]; got != "resumable" {
		t.Fatalf("expected sessionState=resumable, got %#v", got)
	}
	if got := params["sessionControl"]; got != "rebindable" {
		t.Fatalf("expected sessionControl=rebindable, got %#v", got)
	}
	if got := params["providerState"]; got != "synced" {
		t.Fatalf("expected providerState=synced, got %#v", got)
	}
	if got := params["providerWriteMode"]; got != "writable" {
		t.Fatalf("expected providerWriteMode=writable, got %#v", got)
	}
	if got := params["permissionMode"]; got != "bypassPermissions" {
		t.Fatalf("expected permissionMode=bypassPermissions, got %#v", got)
	}
}

func TestAgentStatusChangedEventUsesSharedProviderCacheForManagerCallbacks(t *testing.T) {
	setupFakeClaude(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCCSwitchFixture(t, home, []string{"provider-a", "provider-b"}, "provider-a", "provider-a")

	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	args := []string{"-p", "--permission-mode", "bypassPermissions", "--output-format", "stream-json", "--include-partial-messages", "--verbose"}
	agentID, err := mgr.Create("claude-agent", "claude", "claude", args, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	listResp := rpc(conn, "agent.list", nil)
	if listResp["error"] != nil {
		t.Fatalf("agent.list error: %v", listResp["error"])
	}

	settings := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":   "https://provider-b.example.com",
			"ANTHROPIC_AUTH_TOKEN": "token-provider-b",
		},
		"model": "model-provider-b",
	}
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), data, 0o644); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}

	if err := mgr.StartInPlaceWithMessage(agentID, "claude", "claude", args, nil, "hello"); err != nil {
		t.Fatalf("start in place: %v", err)
	}

	event := readEvent(t, conn, "agent.status_changed")
	params, ok := event["params"].(map[string]any)
	if !ok {
		t.Fatalf("expected params map, got %T", event["params"])
	}
	if got := params["agentId"]; got != agentID {
		t.Fatalf("expected agentId %s, got %#v", agentID, got)
	}
	if got := params["providerState"]; got != "synced" {
		t.Fatalf("expected providerState=synced after cache refresh, got %#v", got)
	}
	if got := params["providerWriteMode"]; got != "writable" {
		t.Fatalf("expected providerWriteMode=writable, got %#v", got)
	}
}

func TestProviderListIncludesStateSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCCSwitchFixture(t, home, []string{"provider-a", "provider-b"}, "provider-a", "provider-b")

	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	createResp := rpc(conn, "agent.create", map[string]any{
		"name":      "claude-agent",
		"provider":  "claude",
		"workDir":   t.TempDir(),
		"sessionId": "sess-provider",
	})
	if createResp["error"] != nil {
		t.Fatalf("create error: %v", createResp["error"])
	}
	createResult, _ := createResp["result"].(map[string]any)
	agentID, _ := createResult["id"].(string)

	resp := rpc(conn, "provider.list", map[string]any{"agentId": agentID})
	if resp["error"] != nil {
		t.Fatalf("provider.list error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp["result"])
	}
	providers, ok := result["providers"].([]any)
	if !ok || len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %#v", result["providers"])
	}
	if got := result["current"]; got != "provider-a" {
		t.Fatalf("expected current provider-a, got %#v", got)
	}
	if got := result["runtimeProviderId"]; got != "provider-b" {
		t.Fatalf("expected runtimeProviderId provider-b, got %#v", got)
	}
	if got := result["providerState"]; got != "drifted" {
		t.Fatalf("expected providerState=drifted, got %#v", got)
	}
	if got := result["providerScope"]; got != "standalone" {
		t.Fatalf("expected providerScope=standalone, got %#v", got)
	}
	if got := result["providerWriteMode"]; got != "writable" {
		t.Fatalf("expected providerWriteMode=writable, got %#v", got)
	}
}

func TestProviderListWithoutAgentIDIsReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCCSwitchFixture(t, home, []string{"provider-a", "provider-b"}, "provider-a", "provider-b")

	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	createResp := rpc(conn, "agent.create", map[string]any{
		"name":      "claude-agent",
		"provider":  "claude",
		"workDir":   t.TempDir(),
		"sessionId": "sess-provider",
	})
	if createResp["error"] != nil {
		t.Fatalf("create error: %v", createResp["error"])
	}

	resp := rpc(conn, "provider.list", nil)
	if resp["error"] != nil {
		t.Fatalf("provider.list error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp["result"])
	}
	if got := result["providerWriteMode"]; got != "read_only" {
		t.Fatalf("expected providerWriteMode=read_only, got %#v", got)
	}
	if got := result["providerReadOnlyReason"]; got != "agentId is required to determine safe provider switching" {
		t.Fatalf("unexpected providerReadOnlyReason: %#v", got)
	}
}

func TestProviderSwitchRejectsReadOnlyAttachedAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCCSwitchFixture(t, home, []string{"provider-a", "provider-b"}, "provider-a", "provider-a")

	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	sessionFile := filepath.Join(t.TempDir(), "attached.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	ag, err := mgr.Attach(scanner.ProcessInfo{
		PID:         123,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "provider.switch", map[string]any{
		"agentId":    ag.ID,
		"providerId": "provider-b",
	})
	if resp["error"] == nil {
		t.Fatal("expected error for read-only attached agent")
	}
	errObj, _ := resp["error"].(map[string]any)
	if got := int(errObj["code"].(float64)); got != -32000 {
		t.Fatalf("expected -32000, got %d", got)
	}
	if got := errObj["message"]; got != "attached runtime cannot guarantee immediate provider switch" {
		t.Fatalf("unexpected error message: %#v", got)
	}
}

func TestProviderSwitchRejectsInheritedScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCCSwitchFixture(t, home, []string{"provider-a", "provider-b"}, "provider-a", "provider-a")

	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	workDir := t.TempDir()
	rootSessionFile := filepath.Join(t.TempDir(), "root-attached.jsonl")
	if err := os.WriteFile(rootSessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write root session file: %v", err)
	}
	if _, err := mgr.Attach(scanner.ProcessInfo{
		PID:         123,
		Provider:    "claude",
		WorkDir:     workDir,
		SessionFile: rootSessionFile,
	}); err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	childID, err := mgr.Create("child", "claude", "claude", []string{"--permission-mode", "bypassPermissions"}, workDir, nil)
	if err != nil {
		t.Fatalf("create child agent: %v", err)
	}
	if err := mgr.UpdateResumeSessionID(childID, "root-attached"); err != nil {
		t.Fatalf("update child resume session: %v", err)
	}

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "provider.switch", map[string]any{
		"agentId":    childID,
		"providerId": "provider-b",
	})
	if resp["error"] == nil {
		t.Fatal("expected error for inherited provider scope")
	}
	errObj, _ := resp["error"].(map[string]any)
	if got := errObj["message"]; got != "provider scope is inherited from root session" {
		t.Fatalf("unexpected error message: %#v", got)
	}
}

func TestConversationSendClaudeKeepsSameAgentID(t *testing.T) {
	setupFakeClaude(t)
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	createResp := rpc(conn, "agent.create", map[string]any{
		"name":     "claude-agent",
		"provider": "claude",
		"workDir":  t.TempDir(),
	})
	if createResp["error"] != nil {
		t.Fatalf("create error: %v", createResp["error"])
	}
	createResult, ok := createResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", createResp["result"])
	}
	agentID, ok := createResult["id"].(string)
	if !ok || agentID == "" {
		t.Fatalf("expected non-empty id, got %#v", createResult["id"])
	}

	sendResp := rpc(conn, "conversation.send", map[string]any{
		"agentId": agentID,
		"message": "hello from phone",
	})
	if sendResp["error"] != nil {
		t.Fatalf("send error: %v", sendResp["error"])
	}
	sendResult, ok := sendResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected send result map, got %T", sendResp["result"])
	}
	returnedID, ok := sendResult["id"].(string)
	if !ok || returnedID == "" {
		t.Fatalf("expected returned id, got %#v", sendResult["id"])
	}
	if returnedID != agentID {
		t.Fatalf("conversation.send returned different id: got %s want %s", returnedID, agentID)
	}

	deadline := time.Now().Add(2 * time.Second)
	foundUser := false
	foundAssistant := false
	var lastEvents []any

	for time.Now().Before(deadline) {
		historyResp := rpc(conn, "conversation.history", map[string]any{
			"agentId": agentID,
			"limit":   50,
		})
		if historyResp["error"] != nil {
			t.Fatalf("history error: %v", historyResp["error"])
		}
		historyResult, ok := historyResp["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected history result map, got %T", historyResp["result"])
		}
		rawEvents, ok := historyResult["events"].([]any)
		if !ok {
			t.Fatalf("expected events array, got %T", historyResult["events"])
		}

		lastEvents = rawEvents
		foundUser = false
		foundAssistant = false
		for _, raw := range rawEvents {
			eventMap, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			role, _ := eventMap["role"].(string)
			text, _ := eventMap["text"].(string)
			if role == "user" && text == "hello from phone" {
				foundUser = true
			}
			if role == "assistant" && strings.Contains(text, "echo:hello from phone") {
				foundAssistant = true
			}
		}

		if foundUser && foundAssistant {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !foundUser || !foundAssistant {
		t.Fatalf("expected user+assistant messages in history; foundUser=%v foundAssistant=%v events=%v", foundUser, foundAssistant, lastEvents)
	}
}

func TestConversationSendClaudeWithImagesPassesCleanFileArgs(t *testing.T) {
	setupFakeClaude(t)
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	createResp := rpc(conn, "agent.create", map[string]any{
		"name":    "img-agent",
		"cmd":     "claude",
		"workDir": t.TempDir(),
	})
	if createResp["error"] != nil {
		t.Fatalf("agent.create error: %v", createResp["error"])
	}
	createResult, ok := createResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", createResp["result"])
	}
	agentID, ok := createResult["id"].(string)
	if !ok || agentID == "" {
		t.Fatalf("expected non-empty id, got %#v", createResult["id"])
	}

	sendResp := rpc(conn, "conversation.send", map[string]any{
		"agentId": agentID,
		"message": "describe this",
		"images": []map[string]any{
			{"data": "iVBORw0KGgo=", "mimeType": "image/png"},
		},
	})
	if sendResp["error"] != nil {
		t.Fatalf("send error: %v", sendResp["error"])
	}

	// Verify history records the image
	deadline := time.Now().Add(2 * time.Second)
	var foundUser bool
	for time.Now().Before(deadline) {
		historyResp := rpc(conn, "conversation.history", map[string]any{
			"agentId": agentID,
			"limit":   50,
		})
		if historyResp["error"] != nil {
			t.Fatalf("history error: %v", historyResp["error"])
		}
		historyResult, _ := historyResp["result"].(map[string]any)
		rawEvents, _ := historyResult["events"].([]any)
		for _, raw := range rawEvents {
			eventMap, _ := raw.(map[string]any)
			role, _ := eventMap["role"].(string)
			text, _ := eventMap["text"].(string)
			imageCount, _ := eventMap["imageCount"].(float64)
			if role == "user" && text == "describe this" && imageCount == 1 {
				foundUser = true
				break
			}
		}
		if foundUser {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !foundUser {
		t.Fatalf("expected user message with imageCount=1 in history")
	}
}

func TestSessionCreateAndList(t *testing.T) {
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	createResp := rpc(conn, "session.create", map[string]any{
		"name":    "session-agent",
		"cmd":     "echo",
		"args":    []string{"hello"},
		"workDir": t.TempDir(),
	})
	if createResp["error"] != nil {
		t.Fatalf("session.create error: %v", createResp["error"])
	}
	result, ok := createResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", createResp["result"])
	}
	id, ok := result["id"].(string)
	if !ok || id == "" {
		t.Fatalf("expected non-empty id, got %#v", result["id"])
	}

	listResp := rpc(conn, "session.list", nil)
	if listResp["error"] != nil {
		t.Fatalf("session.list error: %v", listResp["error"])
	}
	listResult, ok := listResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected map result from session.list, got %T", listResp["result"])
	}
	if _, ok := listResult["processes"]; !ok {
		t.Fatalf("expected processes field in session.list result")
	}
	if _, ok := listResult["count"]; !ok {
		t.Fatalf("expected count field in session.list result")
	}
}

func TestConversationSendRejectsWatcherAttach(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	sessionFile := filepath.Join(t.TempDir(), "attached.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	ag, err := mgr.Attach(scanner.ProcessInfo{
		PID:         123,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "conversation.send", map[string]any{
		"agentId": ag.ID,
		"message": "hello",
	})
	if resp["error"] == nil {
		t.Fatal("expected error for read-only attached agent")
	}
	errObj := resp["error"].(map[string]any)
	if got := int(errObj["code"].(float64)); got != -32000 {
		t.Fatalf("expected -32000, got %d", got)
	}
}

func TestAgentRestartAttachedModelUpdateWritesSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	data, err := json.Marshal(map[string]any{
		"model": "claude-old",
		"env": map[string]any{"ANTHROPIC_MODEL": "claude-old"},
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	sessionFile := filepath.Join(t.TempDir(), "attached.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	ag, err := mgr.Attach(scanner.ProcessInfo{
		PID:         123,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.restart", map[string]any{
		"agentId": ag.ID,
		"model":   "claude-sonnet-4-6",
	})
	if resp["error"] != nil {
		t.Fatalf("agent.restart error: %v", resp["error"])
	}

	updated, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(updated, &settings); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	if got, _ := settings["model"].(string); got != "claude-sonnet-4-6" {
		t.Fatalf("settings.model = %q, want claude-sonnet-4-6", got)
	}
	envMap, _ := settings["env"].(map[string]any)
	if got, _ := envMap["ANTHROPIC_MODEL"].(string); got != "claude-sonnet-4-6" {
		t.Fatalf("settings.env.ANTHROPIC_MODEL = %q, want claude-sonnet-4-6", got)
	}
}

func TestAgentRestartAttachedOpenCodeUsesRestartInPlace(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	sessionFile := filepath.Join(t.TempDir(), "attached-opencode.json")
	if err := os.WriteFile(sessionFile, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	ag, err := mgr.Attach(scanner.ProcessInfo{
		PID:         234,
		Provider:    "opencode",
		WorkDir:     t.TempDir(),
		SessionFile: sessionFile,
		SessionID:   "ses_attached",
	})
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	ag.SetAttachInputRoute("tmux", false, "", "session:1.1")

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.restart", map[string]any{
		"agentId": ag.ID,
		"model":   "tb-api/claude-sonnet-4-6",
	})
	if resp["error"] != nil {
		t.Fatalf("agent.restart error: %v", resp["error"])
	}

	restarted := mgr.Get(ag.ID)
	if restarted == nil {
		t.Fatal("agent missing after restart")
	}
	if restarted.Provider != "opencode" {
		t.Fatalf("provider = %q, want opencode", restarted.Provider)
	}
	if filepath.Base(restarted.Cmd) != "opencode" {
		t.Fatalf("cmd = %q, want basename opencode", restarted.Cmd)
	}
	if len(restarted.Args) < 4 {
		t.Fatalf("args too short: %v", restarted.Args)
	}
	if restarted.Args[0] != "-s" {
		t.Fatalf("args[0] = %q, want -s (all=%v)", restarted.Args[0], restarted.Args)
	}
	if restarted.Args[1] == "" {
		t.Fatalf("args[1] should be non-empty session id (all=%v)", restarted.Args)
	}
	if restarted.Args[2] != "-m" {
		t.Fatalf("args[2] = %q, want -m (all=%v)", restarted.Args[2], restarted.Args)
	}
	if restarted.Args[3] != "tb-api/claude-sonnet-4-6" {
		t.Fatalf("args[3] = %q, want tb-api/claude-sonnet-4-6 (all=%v)", restarted.Args[3], restarted.Args)
	}
}

func TestAgentRestartAttachedOpenCodeWatcherModeAllowsModelSwitch(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	sessionFile := filepath.Join(t.TempDir(), "attached-opencode.json")
	if err := os.WriteFile(sessionFile, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	ag, err := mgr.Attach(scanner.ProcessInfo{
		PID:         345,
		Provider:    "opencode",
		WorkDir:     t.TempDir(),
		SessionFile: sessionFile,
		SessionID:   "ses_attached",
	})
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	ag.SetAttachInputRoute("watcher", true, "watch-only attach", "")

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.restart", map[string]any{
		"agentId": ag.ID,
		"model":   "tb-api/claude-sonnet-4-6",
	})
	if resp["error"] != nil {
		t.Fatalf("agent.restart error: %v", resp["error"])
	}

	restarted := mgr.Get(ag.ID)
	if restarted == nil {
		t.Fatal("agent missing after restart")
	}
	if restarted.Provider != "opencode" {
		t.Fatalf("provider = %q, want opencode", restarted.Provider)
	}
	if len(restarted.Args) < 4 {
		t.Fatalf("args too short: %v", restarted.Args)
	}
	if restarted.Args[0] != "-s" || restarted.Args[2] != "-m" || restarted.Args[3] != "tb-api/claude-sonnet-4-6" {
		t.Fatalf("unexpected args: %v", restarted.Args)
	}
}

func TestAgentRestartRejectsWatcherAttach(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	sessionFile := filepath.Join(t.TempDir(), "attached.jsonl")
	if err := os.WriteFile(sessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	ag, err := mgr.Attach(scanner.ProcessInfo{
		PID:         123,
		Provider:    "claude",
		WorkDir:     t.TempDir(),
		SessionFile: sessionFile,
	})
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.restart", map[string]any{
		"agentId": ag.ID,
	})
	if resp["error"] == nil {
		t.Fatal("expected error for read-only attached agent")
	}
	errObj := resp["error"].(map[string]any)
	if got := int(errObj["code"].(float64)); got != -32000 {
		t.Fatalf("expected -32000, got %d", got)
	}
}

func TestSessionAttachRequiresPidOrSessionID(t *testing.T) {
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "session.attach", map[string]any{})
	if resp["error"] == nil {
		t.Fatal("expected error")
	}
	errObj := resp["error"].(map[string]any)
	if got := int(errObj["code"].(float64)); got != -32602 {
		t.Fatalf("expected -32602, got %d", got)
	}
}

func TestSessionCatalogReturnsGroupedData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	macDir := filepath.Join(home, "Library", "Application Support", "opencode", "storage", "session_diff")
	if err := os.MkdirAll(macDir, 0o755); err != nil {
		t.Fatalf("mkdir mac path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(macDir, "ses_group.json"), []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	repoDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo dir: %v", err)
	}
	fallbackRepoDir := filepath.Join(t.TempDir(), "fallback-repo")
	if err := os.MkdirAll(fallbackRepoDir, 0o755); err != nil {
		t.Fatalf("mkdir fallback repo dir: %v", err)
	}
	claudeBin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(claudeBin, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	liveCmd := exec.Command(claudeBin)
	liveCmd.Dir = repoDir
	liveCmd.Env = append(os.Environ(), "HOME="+home)
	if err := liveCmd.Start(); err != nil {
		t.Fatalf("start fake claude: %v", err)
	}
	t.Cleanup(func() {
		if liveCmd.Process != nil {
			_ = liveCmd.Process.Kill()
			_, _ = liveCmd.Process.Wait()
		}
	})
	time.Sleep(150 * time.Millisecond)

	claudeProjectsDir := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(claudeProjectsDir, 0o755); err != nil {
		t.Fatalf("mkdir claude projects dir: %v", err)
	}
	claudeProjectDir := filepath.Join(claudeProjectsDir, strings.ReplaceAll(repoDir, "/", "-"))
	if err := os.MkdirAll(claudeProjectDir, 0o755); err != nil {
		t.Fatalf("mkdir claude project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeProjectDir, "sess-live.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write live claude file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeProjectDir, "sess-archived.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write archived claude file: %v", err)
	}
	fallbackProjectDir := filepath.Join(claudeProjectsDir, strings.ReplaceAll(fallbackRepoDir, "/", "-"))
	if err := os.MkdirAll(fallbackProjectDir, 0o755); err != nil {
		t.Fatalf("mkdir fallback claude project dir: %v", err)
	}
	fallbackSessionFile := filepath.Join(fallbackProjectDir, "sess-fallback.jsonl")
	if err := os.WriteFile(fallbackSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write fallback claude file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir claude sessions dir: %v", err)
	}
	pidMapPath := filepath.Join(home, ".claude", "sessions", strconv.Itoa(liveCmd.Process.Pid)+".json")
	if err := os.WriteFile(pidMapPath, []byte(`{"sessionId":"sess-live"}`), 0o644); err != nil {
		t.Fatalf("write live session pid map: %v", err)
	}

	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())
	managedSessionFile := filepath.Join(t.TempDir(), "managed-attached.jsonl")
	if err := os.WriteFile(managedSessionFile, []byte(""), 0o644); err != nil {
		t.Fatalf("write managed session file: %v", err)
	}
	if _, err := mgr.Attach(scanner.ProcessInfo{PID: os.Getpid(), Provider: "claude", WorkDir: t.TempDir(), SessionFile: managedSessionFile}); err != nil {
		t.Fatalf("attach managed agent: %v", err)
	}
	sharedSessionFile := filepath.Join(t.TempDir(), "sess-shared.jsonl")
	if err := os.WriteFile(sharedSessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write shared session file: %v", err)
	}
	if _, err := mgr.Attach(scanner.ProcessInfo{PID: 4001, Provider: "claude", WorkDir: repoDir, SessionFile: sharedSessionFile}); err != nil {
		t.Fatalf("attach first shared-session agent: %v", err)
	}
	if _, err := mgr.Attach(scanner.ProcessInfo{PID: 4002, Provider: "claude", WorkDir: repoDir, SessionFile: sharedSessionFile}); err != nil {
		t.Fatalf("attach second shared-session agent: %v", err)
	}
	teamChildPID := liveCmd.Process.Pid + 1
	fallbackPID := liveCmd.Process.Pid + 2
	mgr.SetScanExisting(func() ([]scanner.ProcessInfo, error) {
		return []scanner.ProcessInfo{
			{PID: liveCmd.Process.Pid, Provider: "claude", WorkDir: repoDir, SessionID: "sess-live", SessionFile: filepath.Join(claudeProjectDir, "sess-live.jsonl")},
			{PID: fallbackPID, Provider: "claude", WorkDir: fallbackRepoDir, SessionID: "sess-fallback", SessionFile: fallbackSessionFile},
			{PID: teamChildPID, Provider: "claude", WorkDir: repoDir},
		}, nil
	})
	server := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(server)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "session.catalog", nil)
	if resp["error"] != nil {
		t.Fatalf("session.catalog error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp["result"])
	}

	managed, ok := result["managed"].([]any)
	if !ok {
		t.Fatalf("expected managed array, got %T", result["managed"])
	}
	if len(managed) < 3 {
		t.Fatalf("expected at least 3 managed items, got %v", managed)
	}
	managedSharedPIDs := map[int]bool{}
	for _, raw := range managed {
		entry, _ := raw.(map[string]any)
		if entry["provider"] == "claude" && entry["sessionId"] == "sess-shared" {
			if pid, _ := entry["pid"].(float64); int(pid) > 0 {
				managedSharedPIDs[int(pid)] = true
			}
		}
	}
	if !managedSharedPIDs[4001] || !managedSharedPIDs[4002] {
		t.Fatalf("expected both shared-session PIDs in managed, got %v", managed)
	}

	attachable, ok := result["attachable"].([]any)
	if !ok {
		t.Fatalf("expected attachable array, got %T", result["attachable"])
	}
	foundLiveAttachable := false
	foundFallbackAttachable := false
	foundTeamChildAttachable := false
	for _, raw := range attachable {
		entry, _ := raw.(map[string]any)
		if entry["provider"] == "claude" && entry["sessionId"] == "sess-live" {
			foundLiveAttachable = true
		}
		if entry["provider"] == "claude" && entry["sessionId"] == "sess-fallback" {
			foundFallbackAttachable = true
		}
		if entry["provider"] == "claude" {
			if pid, _ := entry["pid"].(float64); int(pid) == teamChildPID {
				// B-001 fix: empty-sessionID claude candidates are now included
				// in attachable so they can be attached and managed.
				foundTeamChildAttachable = true
			}
		}
	}
	if !foundLiveAttachable {
		t.Fatalf("expected live claude session in attachable, got %v", attachable)
	}
	if !foundFallbackAttachable {
		t.Fatalf("expected fallback claude session in attachable, got %v", attachable)
	}
	if !foundTeamChildAttachable {
		t.Fatalf("expected team child claude session (empty sessionID) in attachable after B-001 fix, got %v", attachable)
	}

	files, ok := result["opencodeFiles"].([]any)
	if !ok {
		t.Fatalf("expected opencodeFiles array, got %T", result["opencodeFiles"])
	}
	found := false
	for _, raw := range files {
		entry, _ := raw.(map[string]any)
		if entry["id"] == "ses_group" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ses_group in opencodeFiles, got %v", files)
	}

	claudeFiles, ok := result["claudeFiles"].([]any)
	if !ok {
		t.Fatalf("expected claudeFiles array, got %T", result["claudeFiles"])
	}
	foundArchivedClaude := false
	for _, raw := range claudeFiles {
		entry, _ := raw.(map[string]any)
		if entry["id"] == "sess-live" {
			t.Fatalf("expected live claude session to be excluded from claudeFiles, got %v", claudeFiles)
		}
		if entry["id"] == "sess-fallback" {
			t.Fatalf("expected fallback live claude session to be excluded from claudeFiles, got %v", claudeFiles)
		}
		if entry["id"] == "sess-archived" {
			foundArchivedClaude = true
		}
	}
	if !foundArchivedClaude {
		t.Fatalf("expected archived claude session to remain in claudeFiles, got %v", claudeFiles)
	}
}

func TestAgentListExcludesSubAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}

	// Set up Claude project directory with main session and sub-agent session
	claudeProjectsDir := filepath.Join(home, ".claude", "projects")
	projectDir := filepath.Join(claudeProjectsDir, strings.ReplaceAll(workDir, "/", "-"))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	// Main session at project root
	if err := os.WriteFile(filepath.Join(projectDir, "agent-main-session.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write main session file: %v", err)
	}
	// Sub-agent session in subagents/
	subagentsDir := filepath.Join(projectDir, "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("mkdir subagents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subagentsDir, "agent-sub-abc.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write sub-agent session file: %v", err)
	}

	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())

	// Create main agent with agent- prefix session (should NOT be excluded — B-001 fix)
	mainAgentID, err := mgr.Create("main-agent", "claude", "claude", []string{"--dangerously-skip-permissions"}, workDir, nil)
	if err != nil {
		t.Fatalf("create main agent: %v", err)
	}
	if err := mgr.UpdateResumeSessionID(mainAgentID, "agent-main-session"); err != nil {
		t.Fatalf("update main agent resume session: %v", err)
	}

	// Create sub-agent with sub-agent session (should be excluded)
	subAgentID, err := mgr.Create("sub-agent", "claude", "claude", []string{"--dangerously-skip-permissions"}, workDir, nil)
	if err != nil {
		t.Fatalf("create sub-agent: %v", err)
	}
	if err := mgr.UpdateResumeSessionID(subAgentID, "agent-sub-abc"); err != nil {
		t.Fatalf("update sub-agent resume session: %v", err)
	}

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "agent.list", nil)
	if resp["error"] != nil {
		t.Fatalf("agent.list error: %v", resp["error"])
	}
	b, _ := json.Marshal(resp["result"])
	var agents []map[string]any
	if err := json.Unmarshal(b, &agents); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	var foundMain, foundSub bool
	for _, ag := range agents {
		id, _ := ag["id"].(string)
		if id == mainAgentID {
			foundMain = true
		}
		if id == subAgentID {
			foundSub = true
		}
	}
	if !foundMain {
		t.Fatalf("expected main agent (agent-main-session) to be present, got %v", agents)
	}
	if foundSub {
		t.Fatalf("expected sub-agent (agent-sub-abc) to be excluded, got %v", agents)
	}
}

func TestSessionCatalogExcludesSubAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}

	// Set up Claude project directory with main session and sub-agent session
	claudeProjectsDir := filepath.Join(home, ".claude", "projects")
	projectDir := filepath.Join(claudeProjectsDir, strings.ReplaceAll(workDir, "/", "-"))
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "agent-main-session.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write main session file: %v", err)
	}
	subagentsDir := filepath.Join(projectDir, "subagents")
	if err := os.MkdirAll(subagentsDir, 0o755); err != nil {
		t.Fatalf("mkdir subagents dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subagentsDir, "agent-sub-abc.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write sub-agent session file: %v", err)
	}

	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	mgr := agent.NewManager(s, t.TempDir())

	mainAgentID, err := mgr.Create("main-agent", "claude", "claude", []string{"--dangerously-skip-permissions"}, workDir, nil)
	if err != nil {
		t.Fatalf("create main agent: %v", err)
	}
	if err := mgr.UpdateResumeSessionID(mainAgentID, "agent-main-session"); err != nil {
		t.Fatalf("update main agent resume session: %v", err)
	}

	subAgentID, err := mgr.Create("sub-agent", "claude", "claude", []string{"--dangerously-skip-permissions"}, workDir, nil)
	if err != nil {
		t.Fatalf("create sub-agent: %v", err)
	}
	if err := mgr.UpdateResumeSessionID(subAgentID, "agent-sub-abc"); err != nil {
		t.Fatalf("update sub-agent resume session: %v", err)
	}

	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	conn := dialWS(t, ts, "testtoken")

	resp := rpc(conn, "session.catalog", nil)
	if resp["error"] != nil {
		t.Fatalf("session.catalog error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp["result"])
	}
	managed, ok := result["managed"].([]any)
	if !ok {
		t.Fatalf("expected managed array, got %T", result["managed"])
	}

	var foundMain, foundSub bool
	for _, raw := range managed {
		entry, _ := raw.(map[string]any)
		id, _ := entry["id"].(string)
		if id == mainAgentID {
			foundMain = true
		}
		if id == subAgentID {
			foundSub = true
		}
	}
	if !foundMain {
		t.Fatalf("expected main agent in managed list, got %v", managed)
	}
	if foundSub {
		t.Fatalf("expected sub-agent to be excluded from managed list, got %v", managed)
	}
}

func TestConversationKey(t *testing.T) {
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	createResp := rpc(conn, "agent.create", map[string]any{
		"name":    "cat-agent",
		"cmd":     "cat",
		"args":    []string{},
		"workDir": t.TempDir(),
	})
	if createResp["error"] != nil {
		t.Fatalf("create error: %v", createResp["error"])
	}
	result := createResp["result"].(map[string]any)
	agentID := result["id"].(string)

	okResp := rpc(conn, "conversation.key", map[string]any{
		"agentId": agentID,
		"key":     "enter",
	})
	if okResp["error"] != nil {
		t.Fatalf("conversation.key error: %v", okResp["error"])
	}

	invalidResp := rpc(conn, "conversation.key", map[string]any{
		"agentId": agentID,
		"key":     "unknown",
	})
	if invalidResp["error"] == nil {
		t.Fatal("expected error for unsupported key")
	}
	errObj, _ := invalidResp["error"].(map[string]any)
	if got := int(errObj["code"].(float64)); got != -32602 {
		t.Fatalf("expected -32602 for unsupported key, got %d", got)
	}
}
