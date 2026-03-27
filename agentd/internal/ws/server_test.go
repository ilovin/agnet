package ws_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func rpc(conn *websocket.Conn, method string, params any) map[string]any {
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	_ = conn.WriteJSON(req)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var resp map[string]any
	_ = conn.ReadJSON(&resp)
	return resp
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
