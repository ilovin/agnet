//go:build integration

package agentd_test

import (
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

// Integration test: real server, real echo process, end-to-end JSON-RPC
func TestEndToEnd(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mgr := agent.NewManager(s, t.TempDir())
	srv := ws.New(mgr, "e2etoken")

	ts := httptest.NewServer(srv)
	defer ts.Close()

	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(u, map[string][]string{
		"Authorization": {"Bearer e2etoken"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Create an echo agent
	req := map[string]any{
		"jsonrpc": "2.0", "id": 1,
		"method": "agent.create",
		"params": map[string]any{
			"name": "e2e-echo", "cmd": "echo",
			"args": []string{"integration test ok"}, "workDir": t.TempDir(),
		},
	}
	conn.WriteJSON(req)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var resp map[string]any
	conn.ReadJSON(&resp)
	if resp["error"] != nil {
		t.Fatalf("create error: %v", resp["error"])
	}

	// List and verify
	conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "agent.list", "params": nil})
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var listResp map[string]any
	conn.ReadJSON(&listResp)
	t.Logf("agent.list response: %v", listResp)
	if listResp["error"] != nil {
		t.Fatalf("list error: %v", listResp["error"])
	}
}
