package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentcli/internal/client"
	"github.com/spf13/cobra"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type mockServer struct {
	agents      []map[string]any
	nodes       []map[string]any
	catalog     map[string]any
	history     map[string]any
	t           *testing.T
}

func newMockServer(t *testing.T) *mockServer {
	return &mockServer{
		agents: []map[string]any{
			{
				"id":       "agent-1",
				"name":     "Test Agent 1",
				"provider": "claude",
				"status":   "working",
				"pid":      float64(12345),
				"workDir":  "/tmp/test1",
				"projectName": "test1",
				"hasHistory": true,
				"readOnly": false,
				"runtimeState": "live",
				"sessionState": "active",
				"providerState": "ok",
				"permissionMode": "auto",
			},
			{
				"id":       "agent-2",
				"name":     "Test Agent 2",
				"provider": "opencode",
				"status":   "idle",
				"pid":      float64(12346),
				"workDir":  "/tmp/test2",
				"projectName": "test2",
				"hasHistory": false,
				"readOnly": false,
				"runtimeState": "standby",
				"sessionState": "standby",
				"providerState": "drifted",
				"permissionMode": "bypassPermissions",
			},
		},
		nodes: []map[string]any{
			{
				"id":     "node-local",
				"name":   "local",
				"host":   "localhost",
				"status": "connected",
			},
		},
		catalog: map[string]any{
			"managed": []any{
				map[string]any{
					"id":        "agent-1",
					"name":      "Test Agent 1",
					"provider":  "claude",
					"status":    "working",
					"pid":       float64(12345),
					"workDir":   "/tmp/test1",
					"projectName": "test1",
					"sessionId": "sess-1",
				},
			},
			"attachable": []any{
				map[string]any{
					"provider":  "claude",
					"pid":       float64(99999),
					"sessionId": "sess-new",
					"workDir":   "/tmp/new",
				},
			},
		},
		history: map[string]any{
			"events": []any{
				map[string]any{"seq": float64(1), "role": "user", "text": "hello"},
				map[string]any{"seq": float64(2), "role": "assistant", "text": "hi there"},
			},
			"lastSeq":  float64(2),
			"firstSeq": float64(1),
		},
		t: t,
	}
}

func (m *mockServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	for {
		var req map[string]any
		if err := conn.ReadJSON(&req); err != nil {
			return
		}

		method := req["method"].(string)
		id := req["id"]
		var params map[string]any
		if p, ok := req["params"].(map[string]any); ok {
			params = p
		}

		var result any
		var respErr *client.RPCError

		switch method {
		case "node.list":
			result = m.nodes
		case "agent.list":
			if stringify(params["nodeId"]) == "" {
				respErr = &client.RPCError{Code: -32000, Message: "nodeId required"}
			} else {
				result = m.agents
			}
		case "session.catalog":
			if stringify(params["nodeId"]) == "" {
				respErr = &client.RPCError{Code: -32000, Message: "nodeId required"}
			} else {
				result = m.catalog
			}
		case "conversation.history":
			if stringify(params["nodeId"]) == "" {
				respErr = &client.RPCError{Code: -32000, Message: "nodeId required"}
			} else if stringify(params["agentId"]) == "" {
				respErr = &client.RPCError{Code: -32602, Message: "agentId required"}
			} else {
				result = m.history
			}
		case "conversation.send":
			if stringify(params["nodeId"]) == "" {
				respErr = &client.RPCError{Code: -32000, Message: "nodeId required"}
			} else {
				result = map[string]any{"ok": true}
			}
		default:
			respErr = &client.RPCError{Code: -32601, Message: "method not found: " + method}
		}

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
		}
		if respErr != nil {
			resp["error"] = map[string]any{"code": respErr.Code, "message": respErr.Message}
		} else {
			resp["result"] = result
		}

		if err := conn.WriteJSON(resp); err != nil {
			return
		}
	}
}

func setupTestCLI(t *testing.T, server *mockServer) (*cobra.Command, *bytes.Buffer, *httptest.Server) {
	var buf bytes.Buffer

	ts := httptest.NewServer(server)

	wsURL := "ws://" + strings.TrimPrefix(ts.URL, "http://") + "/ws"
	t.Setenv("AGENTGW_URL", wsURL)
	t.Setenv("AGENTGW_TOKEN", "test-token")

	cmd := NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	return cmd, &buf, ts
}

func captureStdout(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestListAgents(t *testing.T) {
	server := newMockServer(t)
	cmd, _, ts := setupTestCLI(t, server)
	defer ts.Close()
	defer func() {
		if cliClient != nil {
			cliClient.Close()
		}
	}()

	cmd.SetArgs([]string{"list-agents"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if !strings.Contains(out, "working") {
		t.Errorf("expected 'working' in output, got: %s", out)
	}
	if !strings.Contains(out, "idle") {
		t.Errorf("expected 'idle' in output, got: %s", out)
	}
	if !strings.Contains(out, "Test Agent 1") {
		t.Errorf("expected agent name in output, got: %s", out)
	}
}

func TestDashboard(t *testing.T) {
	server := newMockServer(t)
	cmd, _, ts := setupTestCLI(t, server)
	defer ts.Close()
	defer func() {
		if cliClient != nil {
			cliClient.Close()
		}
	}()

	cmd.SetArgs([]string{"dashboard"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if !strings.Contains(out, "Dashboard") {
		t.Errorf("expected 'Dashboard' in output, got: %s", out)
	}
	if !strings.Contains(out, "Managed Agents") {
		t.Errorf("expected 'Managed Agents' in output, got: %s", out)
	}
	if !strings.Contains(out, "Attachable Processes") {
		t.Errorf("expected 'Attachable Processes' in output, got: %s", out)
	}
	if !strings.Contains(out, "Test Agent 1") {
		t.Errorf("expected agent name in output, got: %s", out)
	}
}

func TestAgentStatus(t *testing.T) {
	server := newMockServer(t)
	cmd, _, ts := setupTestCLI(t, server)
	defer ts.Close()
	defer func() {
		if cliClient != nil {
			cliClient.Close()
		}
	}()

	cmd.SetArgs([]string{"agent-status", "agent-1"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if !strings.Contains(out, "Test Agent 1") {
		t.Errorf("expected agent name in output, got: %s", out)
	}
	if !strings.Contains(out, "working") {
		t.Errorf("expected 'working' status in output, got: %s", out)
	}
	if !strings.Contains(out, "PID:") {
		t.Errorf("expected PID info in output, got: %s", out)
	}
}

func TestAgentStatusNotFound(t *testing.T) {
	server := newMockServer(t)
	cmd, _, ts := setupTestCLI(t, server)
	defer ts.Close()
	defer func() {
		if cliClient != nil {
			cliClient.Close()
		}
	}()

	cmd.SetArgs([]string{"agent-status", "nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestHistory(t *testing.T) {
	server := newMockServer(t)
	cmd, _, ts := setupTestCLI(t, server)
	defer ts.Close()
	defer func() {
		if cliClient != nil {
			cliClient.Close()
		}
	}()

	cmd.SetArgs([]string{"history", "--agent-id", "agent-1", "--limit", "5"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in history, got: %s", out)
	}
	if !strings.Contains(out, "hi there") {
		t.Errorf("expected 'hi there' in history, got: %s", out)
	}
}

func TestHistoryRequiresAgentID(t *testing.T) {
	server := newMockServer(t)
	cmd, _, ts := setupTestCLI(t, server)
	defer ts.Close()
	defer func() {
		if cliClient != nil {
			cliClient.Close()
		}
	}()

	cmd.SetArgs([]string{"history"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing agent-id")
	}
	if !strings.Contains(err.Error(), "agent-id") {
		t.Errorf("expected agent-id error, got: %v", err)
	}
}

func TestListNodes(t *testing.T) {
	server := newMockServer(t)
	cmd, _, ts := setupTestCLI(t, server)
	defer ts.Close()
	defer func() {
		if cliClient != nil {
			cliClient.Close()
		}
	}()

	cmd.SetArgs([]string{"list-nodes"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if !strings.Contains(out, "local") {
		t.Errorf("expected 'local' node in output, got: %s", out)
	}
}

func TestJSONOutput(t *testing.T) {
	server := newMockServer(t)
	cmd, _, ts := setupTestCLI(t, server)
	defer ts.Close()
	defer func() {
		if cliClient != nil {
			cliClient.Close()
		}
	}()

	cmd.SetArgs([]string{"--json", "list-agents"})
	out := captureStdout(func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	var agents []map[string]any
	if err := json.Unmarshal([]byte(out), &agents); err != nil {
		t.Fatalf("unmarshal JSON: %v\noutput: %s", err, out)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}
}
