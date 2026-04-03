package ws_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentd/internal/agent"
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

func TestConversationSend(t *testing.T) {
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	workDir := t.TempDir()
	createResp := rpc(conn, "agent.create", map[string]any{
		"name": "cat-agent", "cmd": "cat", "args": []string{},
		"workDir": workDir,
	})
	if createResp["error"] != nil {
		t.Fatalf("create error: %v", createResp["error"])
	}
	result := createResp["result"].(map[string]any)
	agentID := result["id"].(string)

	sendResp := rpc(conn, "conversation.send", map[string]any{
		"agentId": agentID,
		"message": "hello agent",
	})
	if sendResp["error"] != nil {
		t.Fatalf("send error: %v", sendResp["error"])
	}

	errResp := rpc(conn, "conversation.send", map[string]any{
		"agentId": "nonexistent",
		"message": "hello",
	})
	if errResp["error"] == nil {
		t.Error("expected error for non-existent agent")
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

	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	createResp := rpc(conn, "agent.create", map[string]any{
		"name":    "managed-1",
		"cmd":     "echo",
		"args":    []string{"hello"},
		"workDir": t.TempDir(),
	})
	if createResp["error"] != nil {
		t.Fatalf("agent.create error: %v", createResp["error"])
	}

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
	if len(managed) == 0 {
		t.Fatalf("expected at least 1 managed item")
	}

	if _, ok := result["attachable"].([]any); !ok {
		t.Fatalf("expected attachable array, got %T", result["attachable"])
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
