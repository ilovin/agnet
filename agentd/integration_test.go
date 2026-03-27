//go:build integration

package agentd_test

import (
	"encoding/json"
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
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("write agent.create: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read agent.create response: %v", err)
	}
	if resp["error"] != nil {
		t.Fatalf("create error: %v", resp["error"])
	}

	// List and verify agent was created
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "agent.list", "params": nil}); err != nil {
		t.Fatalf("write agent.list: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var listResp map[string]any
	if err := conn.ReadJSON(&listResp); err != nil {
		t.Fatalf("read agent.list response: %v", err)
	}
	if listResp["error"] != nil {
		t.Fatalf("list error: %v", listResp["error"])
	}
	// Assert the created agent is in the list
	b, _ := json.Marshal(listResp["result"])
	var agents []map[string]any
	json.Unmarshal(b, &agents)
	if len(agents) == 0 {
		t.Error("expected at least 1 agent in list after create")
	}
}
