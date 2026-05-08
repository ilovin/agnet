//go:build integration

package ws

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/scanner"
	"github.com/phone-talk/agentd/internal/store"
)

// testHandler creates a handler backed by the given server for direct dispatch testing.
func testHandler(srv *Server) *handler {
	return &handler{server: srv, service: NewAgentService()}
}

// TestAgentListShowsHasHistoryAfterAttach validates the full end-to-end pipeline
// for the "暂无对话" fix (commit 7b52763):
//   1. A .jsonl file with conversation history exists
//   2. Manager attaches to a Claude process and loads the JSONL history
//   3. WS handler agent.list returns HasHistory=true and LastMessageTime > 0
//   4. WS handler conversation.history returns events with timestamps
//
func TestAgentListShowsHasHistoryAfterAttach(t *testing.T) {
	// 1. Setup: create a temp directory with a .jsonl file containing messages
	tmpDir := t.TempDir()
	sessionFile := filepath.Join(tmpDir, "session.jsonl")

	// Write Claude JSONL lines: assistant message + user message
	content := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from assistant"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Hello from user"}]}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	// 2. Create a real Manager with a temp SQLite store
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	mgr := agent.NewManager(s, t.TempDir())

	// 3. Attach a fake Claude process (use own PID so validateProcess passes)
	info := scanner.ProcessInfo{
		Provider:    "claude",
		PID:         os.Getpid(),
		SessionFile: sessionFile,
		WorkDir:     tmpDir,
	}
	ag, err := mgr.Attach(info)
	if err != nil {
		t.Fatalf("attach failed: %v", err)
	}

	// 4. Create a WS handler with the manager via a test Server
	srv := New(mgr, "testtoken")
	h := testHandler(srv)

	// 5. Call agent.list via the handler
	req := RPCRequest{Method: "agent.list", ID: "1"}
	resp := h.agentList(req)
	if resp.Error != nil {
		t.Fatalf("agent.list error: %v", resp.Error)
	}

	// 6. Assert: response contains the agent with HasHistory=true
	b, _ := json.Marshal(resp.Result)
	var agents []map[string]any
	if err := json.Unmarshal(b, &agents); err != nil {
		t.Fatalf("unmarshal agent.list result: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	agentResult := agents[0]
	if hasHistory, _ := agentResult["hasHistory"].(bool); !hasHistory {
		t.Fatalf("expected HasHistory=true after loading .jsonl history, got %v", agentResult["hasHistory"])
	}
	lastMsgTime, _ := agentResult["lastMessageTime"].(float64)
	if lastMsgTime <= 0 {
		t.Fatalf("expected LastMessageTime > 0, got %v", lastMsgTime)
	}

	// 7. Call conversation.history
	req2 := RPCRequest{Method: "conversation.history", ID: "2"}
	resp2 := h.conversationHistory(req2, ConversationHistoryParams{AgentID: ag.ID})
	if resp2.Error != nil {
		t.Fatalf("conversation.history error: %v", resp2.Error)
	}

	// 8. Assert: history contains events with timestamp
	result, ok := resp2.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected conversation.history result map, got %T", resp2.Result)
	}
	var rawEvents []any
	switch v := result["events"].(type) {
	case []any:
		rawEvents = v
	case []map[string]any:
		rawEvents = make([]any, len(v))
		for i, e := range v {
			rawEvents[i] = e
		}
	default:
		t.Fatalf("expected events array, got %T", result["events"])
	}
	if len(rawEvents) < 2 {
		t.Fatalf("expected at least 2 events (assistant + user), got %d", len(rawEvents))
	}

	for i, raw := range rawEvents {
		ev, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("expected event map at index %d, got %T", i, raw)
		}
		timestamp, hasTimestamp := ev["timestamp"]
		if !hasTimestamp {
			t.Fatalf("expected every event to have timestamp, missing at index %d", i)
		}
		var ts int64
		switch v := timestamp.(type) {
		case float64:
			ts = int64(v)
		case int64:
			ts = v
		default:
			t.Fatalf("expected timestamp to be numeric at index %d, got %T", i, timestamp)
		}
		if ts <= 0 {
			t.Fatalf("expected timestamp > 0 at index %d, got %d", i, ts)
		}
	}
}
