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
	// agentsByNode allows the test to return different agents per nodeId.
	// When non-nil, agent.list dispatches by params["nodeId"]; otherwise it
	// falls back to the legacy `agents` slice (single-node behaviour).
	agentsByNode  map[string][]map[string]any
	catalogByNode map[string]map[string]any
	t             *testing.T
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

// newMultiNodeMockServer mimics two connected nodes (local + oracle) so we
// can exercise the agentcli multi-node aggregation paths used by
// list-agents and dashboard.
func newMultiNodeMockServer(t *testing.T) *mockServer {
	return &mockServer{
		nodes: []map[string]any{
			{
				"id":     "node-local",
				"name":   "local",
				"host":   "localhost",
				"status": "connected",
			},
			{
				"id":     "node-oracle",
				"name":   "oracle",
				"host":   "oracle.example.com",
				"status": "connected",
			},
		},
		agentsByNode: map[string][]map[string]any{
			"node-local": {
				{
					"id":             "agent-local-1",
					"name":           "Local Claude",
					"provider":       "claude",
					"status":         "idle",
					"pid":            float64(11111),
					"workDir":        "/tmp/local",
					"projectName":    "local-proj",
					"runtimeState":   "live",
					"sessionState":   "active",
				},
			},
			"node-oracle": {
				{
					"id":             "agent-oracle-hermes",
					"name":           "hermes",
					"provider":       "opencode",
					"status":         "working",
					"pid":            float64(22222),
					"workDir":        "/srv/hermes",
					"projectName":    "hermes",
					"runtimeState":   "live",
					"sessionState":   "active",
				},
			},
		},
		catalogByNode: map[string]map[string]any{
			"node-local": {
				"managed": []any{
					map[string]any{
						"id":          "agent-local-1",
						"name":        "Local Claude",
						"provider":    "claude",
						"status":      "idle",
						"pid":         float64(11111),
						"workDir":     "/tmp/local",
						"projectName": "local-proj",
					},
				},
				"attachable": []any{},
			},
			"node-oracle": {
				"managed": []any{
					map[string]any{
						"id":          "agent-oracle-hermes",
						"name":        "hermes",
						"provider":    "opencode",
						"status":      "working",
						"pid":         float64(22222),
						"workDir":     "/srv/hermes",
						"projectName": "hermes",
					},
				},
				"attachable": []any{
					map[string]any{
						"provider":  "claude",
						"pid":       float64(33333),
						"sessionId": "sess-oracle-attach",
						"workDir":   "/srv/other",
					},
				},
			},
		},
		history: map[string]any{
			"events":   []any{},
			"lastSeq":  float64(0),
			"firstSeq": float64(0),
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
			nodeID := stringify(params["nodeId"])
			if nodeID == "" {
				respErr = &client.RPCError{Code: -32000, Message: "nodeId required"}
			} else if m.agentsByNode != nil {
				agents, ok := m.agentsByNode[nodeID]
				if !ok {
					respErr = &client.RPCError{Code: -32000, Message: "node not found: " + nodeID}
				} else {
					result = agents
				}
			} else {
				result = m.agents
			}
		case "session.catalog":
			nodeID := stringify(params["nodeId"])
			if nodeID == "" {
				respErr = &client.RPCError{Code: -32000, Message: "nodeId required"}
			} else if m.catalogByNode != nil {
				cat, ok := m.catalogByNode[nodeID]
				if !ok {
					respErr = &client.RPCError{Code: -32000, Message: "node not found: " + nodeID}
				} else {
					result = cat
				}
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

// TestListAgentsAggregatesAllNodes verifies that list-agents queries every
// connected node and surfaces both local and remote agents, annotating each
// row with the source node name.
func TestListAgentsAggregatesAllNodes(t *testing.T) {
	server := newMultiNodeMockServer(t)
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

	if !strings.Contains(out, "Local Claude") {
		t.Errorf("expected local agent name in output, got: %s", out)
	}
	if !strings.Contains(out, "hermes") {
		t.Errorf("expected oracle hermes agent in output, got: %s", out)
	}
	if !strings.Contains(out, "local") {
		t.Errorf("expected local node label in output, got: %s", out)
	}
	if !strings.Contains(out, "oracle") {
		t.Errorf("expected oracle node label in output, got: %s", out)
	}
	if !strings.Contains(out, "NODE") {
		t.Errorf("expected NODE column header in output, got: %s", out)
	}
}

// TestListAgentsJSONIncludesNodeName ensures the JSON output annotates each
// agent with nodeName so downstream consumers can distinguish source nodes.
func TestListAgentsJSONIncludesNodeName(t *testing.T) {
	server := newMultiNodeMockServer(t)
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
		t.Fatalf("expected 2 aggregated agents, got %d (%+v)", len(agents), agents)
	}

	gotNodes := map[string]bool{}
	for _, a := range agents {
		gotNodes[stringify(a["nodeName"])] = true
	}
	if !gotNodes["local"] {
		t.Errorf("expected agent annotated with nodeName=local, got %+v", agents)
	}
	if !gotNodes["oracle"] {
		t.Errorf("expected agent annotated with nodeName=oracle, got %+v", agents)
	}
}

// TestDashboardAggregatesAllNodes verifies that dashboard pulls
// session.catalog from every connected node.
func TestDashboardAggregatesAllNodes(t *testing.T) {
	server := newMultiNodeMockServer(t)
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

	if !strings.Contains(out, "Local Claude") {
		t.Errorf("expected local managed agent in dashboard, got: %s", out)
	}
	if !strings.Contains(out, "hermes") {
		t.Errorf("expected oracle managed agent in dashboard, got: %s", out)
	}
	if !strings.Contains(out, "sess-oracle-attach") {
		t.Errorf("expected oracle attachable session in dashboard, got: %s", out)
	}
}
