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

// rpcCall sends a JSON-RPC request over WebSocket and returns the matching response.
// It discards any server-push events (messages without a matching id) that arrive
// in between, since agentd broadcasts status events asynchronously.
func rpcCall(t *testing.T, conn *websocket.Conn, id int, method string, params map[string]any) (map[string]any, error) {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := conn.WriteJSON(req); err != nil {
		return nil, err
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		conn.SetReadDeadline(deadline)
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			return nil, err
		}
		msgID, ok := msg["id"]
		if !ok {
			continue // event broadcast, keep reading
		}
		var msgIDInt int
		switch v := msgID.(type) {
		case float64:
			msgIDInt = int(v)
		case int:
			msgIDInt = v
		default:
			continue
		}
		if msgIDInt == id {
			return msg, nil
		}
	}
}

// Integration test: real server, real echo process, end-to-end JSON-RPC
func TestEndToEnd(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mgr := agent.NewManager(s, t.TempDir())
	srv := ws.New(mgr, "e2etoken", "e2enode")

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

	// Create an echo agent (use /bin/echo for reliability)
	createResp, err := rpcCall(t, conn, 1, "agent.create", map[string]any{
		"name": "e2e-echo", "cmd": "/bin/echo",
		"args": []string{"integration test ok"}, "workDir": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("agent.create failed: %v", err)
	}
	if createResp["error"] != nil {
		t.Fatalf("agent.create error: %v", createResp["error"])
	}

	// List and verify agent was created
	listResp, err := rpcCall(t, conn, 2, "agent.list", nil)
	if err != nil {
		t.Fatalf("agent.list failed: %v", err)
	}
	if listResp["error"] != nil {
		t.Fatalf("agent.list error: %v", listResp["error"])
	}
	// Assert the created agent is in the list
	b, _ := json.Marshal(listResp["result"])
	var agents []map[string]any
	json.Unmarshal(b, &agents)
	if len(agents) == 0 {
		t.Error("expected at least 1 agent in list after create")
	}
}

// TestSessionLifecycle tests the complete session lifecycle:
//   1. Create a new agent/session via agentd API
//   2. Verify agent appears in agent.list
//   3. Send a conversation message and verify it's recorded
//   4. Stop the agent and verify status change
//   5. Remove the agent and verify cleanup
func TestSessionLifecycle(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mgr := agent.NewManager(s, t.TempDir())
	srv := ws.New(mgr, "lifecycletoken", "lifecyclenode")

	ts := httptest.NewServer(srv)
	defer ts.Close()

	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(u, map[string][]string{
		"Authorization": {"Bearer lifecycletoken"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// ── Step 1: Create ─────────────────────────────────────────────────────
	createResp, err := rpcCall(t, conn, 1, "agent.create", map[string]any{
		"name":    "lifecycle-test",
		"cmd":     "cat", // cat echoes stdin back
		"workDir": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("agent.create failed: %v", err)
	}
	if createResp["error"] != nil {
		t.Fatalf("agent.create error: %v", createResp["error"])
	}

	resultBytes, _ := json.Marshal(createResp["result"])
	var createResult map[string]any
	if err := json.Unmarshal(resultBytes, &createResult); err != nil {
		t.Fatalf("parse agent.create result: %v", err)
	}
	agentID, ok := createResult["id"].(string)
	if !ok || agentID == "" {
		t.Fatal("expected agent id in create response")
	}
	t.Logf("created agent %s", agentID)

	// ── Step 2: Verify agent appears in list ───────────────────────────────
	listResp, err := rpcCall(t, conn, 2, "agent.list", nil)
	if err != nil {
		t.Fatalf("agent.list failed: %v", err)
	}
	if listResp["error"] != nil {
		t.Fatalf("agent.list error: %v", listResp["error"])
	}

	listBytes, _ := json.Marshal(listResp["result"])
	var agents []map[string]any
	if err := json.Unmarshal(listBytes, &agents); err != nil {
		t.Fatalf("parse agent.list result: %v", err)
	}

	found := false
	for _, ag := range agents {
		if id, _ := ag["id"].(string); id == agentID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("agent %s not found in agent.list", agentID)
	}
	t.Logf("agent %s confirmed in list (%d agents)", agentID, len(agents))

	// ── Step 3: Send message via conversation.send ─────────────────────────
	sendResp, err := rpcCall(t, conn, 3, "conversation.send", map[string]any{
		"agentId": agentID,
		"message": "hello lifecycle test",
	})
	if err != nil {
		t.Fatalf("conversation.send failed: %v", err)
	}
	if sendResp["error"] != nil {
		t.Fatalf("conversation.send error: %v", sendResp["error"])
	}
	t.Logf("message sent to agent %s", agentID)

	// ── Step 4: Verify conversation history ────────────────────────────────
	historyResp, err := rpcCall(t, conn, 4, "conversation.history", map[string]any{
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("conversation.history failed: %v", err)
	}
	if historyResp["error"] != nil {
		t.Fatalf("conversation.history error: %v", historyResp["error"])
	}

	historyBytes, _ := json.Marshal(historyResp["result"])
	var historyResult map[string]any
	if err := json.Unmarshal(historyBytes, &historyResult); err != nil {
		t.Fatalf("parse conversation.history result: %v", err)
	}

	events, _ := historyResult["events"].([]any)
	if len(events) == 0 {
		t.Fatal("expected at least 1 event in conversation history after send")
	}
	t.Logf("conversation history has %d events", len(events))

	// ── Step 5: Stop the agent ─────────────────────────────────────────────
	stopResp, err := rpcCall(t, conn, 5, "agent.stop", map[string]any{
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("agent.stop failed: %v", err)
	}
	if stopResp["error"] != nil {
		t.Fatalf("agent.stop error: %v", stopResp["error"])
	}
	t.Logf("agent %s stopped", agentID)

	// Verify agent status is stopped after stop
	listResp2, err := rpcCall(t, conn, 6, "agent.list", nil)
	if err != nil {
		t.Fatalf("agent.list (post-stop) failed: %v", err)
	}
	listBytes2, _ := json.Marshal(listResp2["result"])
	var agents2 []map[string]any
	json.Unmarshal(listBytes2, &agents2)

	var stoppedAgent map[string]any
	for _, ag := range agents2 {
		if id, _ := ag["id"].(string); id == agentID {
			stoppedAgent = ag
			break
		}
	}
	if stoppedAgent == nil {
		t.Fatalf("agent %s missing from list after stop", agentID)
	}
	status, _ := stoppedAgent["status"].(string)
	if status != "stopped" {
		t.Fatalf("expected status 'stopped' after agent.stop, got %q", status)
	}

	// ── Step 6: Remove the agent ───────────────────────────────────────────
	removeResp, err := rpcCall(t, conn, 7, "agent.remove", map[string]any{
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("agent.remove failed: %v", err)
	}
	if removeResp["error"] != nil {
		t.Fatalf("agent.remove error: %v", removeResp["error"])
	}
	t.Logf("agent %s removed", agentID)

	// Verify agent is gone from list
	listResp3, err := rpcCall(t, conn, 8, "agent.list", nil)
	if err != nil {
		t.Fatalf("agent.list (post-remove) failed: %v", err)
	}
	listBytes3, _ := json.Marshal(listResp3["result"])
	var agents3 []map[string]any
	json.Unmarshal(listBytes3, &agents3)

	for _, ag := range agents3 {
		if id, _ := ag["id"].(string); id == agentID {
			t.Fatalf("agent %s should have been removed from list", agentID)
		}
	}

	t.Logf("TEST-003 agentd session lifecycle passed: create -> list -> message -> history -> stop -> remove")
}
