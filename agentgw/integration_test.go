//go:build integration

package agentgw_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/node"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	"github.com/phone-talk/agentgw/internal/ws"
)

// findAgentdBinary searches for a compiled agentd binary in common locations.
func findAgentdBinary() string {
	candidates := []string{
		"./agentd",
		"../agentd/agentd",
		"./agentd/agentd",
	}
	if ex, err := os.Executable(); err == nil {
		exDir := filepath.Dir(ex)
		candidates = append([]string{
			filepath.Join(exDir, "agentd"),
			filepath.Join(exDir, "..", "agentd", "agentd"),
			filepath.Join(exDir, "..", "..", "agentd", "agentd"),
		}, candidates...)
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			abs, _ := filepath.Abs(p)
			return abs
		}
	}
	return ""
}

// startAgentd starts the agentd binary on an ephemeral port with a temp data dir.
// It returns the process, port, and dataDir. The caller must call process.Kill().
func startAgentd(t *testing.T, token string) (*os.Process, int, string) {
	t.Helper()

	bin := findAgentdBinary()
	if bin == "" {
		t.Fatal("agentd binary not found; build it first with: cd agentd && go build -o agentd ./cmd/agentd")
	}

	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "config.json")

	// Reserve an ephemeral port first, then tell agentd to use it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	agentdPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cfg := map[string]any{
		"port":     agentdPort,
		"token":    token,
		"data_dir": dataDir,
	}
	cfgBytes, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configPath, cfgBytes, 0600); err != nil {
		t.Fatalf("write agentd config: %v", err)
	}

	cmd := exec.Command(bin, "start")
	cmd.Env = append(os.Environ(), "AGENTD_CONFIG="+configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start agentd: %v", err)
	}

	// Wait for agentd to be ready
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/status", agentdPort))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
	}

	return cmd.Process, agentdPort, dataDir
}

// rpcCall sends a JSON-RPC request over WebSocket and returns the matching response.
// It discards any server-push events (messages without a matching id) that arrive
// in between, since agentgw broadcasts status events asynchronously.
func rpcCall(t *testing.T, conn *websocket.Conn, id int, method string, params map[string]any) (map[string]any, error) {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := conn.WriteJSON(req); err != nil {
		return nil, fmt.Errorf("write %s: %w", method, err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		conn.SetReadDeadline(deadline)
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			return nil, fmt.Errorf("read %s response: %w", method, err)
		}
		// Match by id; skip events (which have no id)
		msgID, ok := msg["id"]
		if !ok {
			continue // event broadcast, keep reading
		}
		// id can be float64 or int depending on JSON unmarshaling
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
		// mismatched id (shouldn't happen), keep reading
	}
}

// TestAgentgwAgentdHandshake verifies the full agentgw <-> agentd handshake:
//   1. Start real agentd on ephemeral port with temp SQLite DB
//   2. Start agentgw and connect to agentd via node.add
//   3. Verify node.list shows node as "connected"
//   4. Verify agent.list proxies correctly from agentd through agentgw
func TestAgentgwAgentdHandshake(t *testing.T) {
	const token = "integration-test-token"

	// ── 1. Start agentd on ephemeral port ──────────────────────────────────
	agentdProc, agentdPort, _ := startAgentd(t, token)
	defer func() {
		agentdProc.Kill()
		agentdProc.Wait()
	}()

	t.Logf("agentd running on port %d", agentdPort)

	// ── 2. Start agentgw on ephemeral port ─────────────────────────────────
	nodesFile := filepath.Join(t.TempDir(), "nodes.json")
	if err := os.WriteFile(nodesFile, []byte("{}"), 0600); err != nil {
		t.Fatalf("write nodes file: %v", err)
	}

	nodeStore := nodecfg.New(nodesFile)
	nodeMgr := node.NewManager(nodeStore, nil)
	gwSrv := ws.New(nodeMgr, token)

	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("agentgw listen: %v", err)
	}
	defer gwLn.Close()

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/ws", gwSrv)
		if err := http.Serve(gwLn, mux); err != nil && !strings.Contains(err.Error(), "use of closed") {
			t.Logf("agentgw serve ended: %v", err)
		}
	}()

	gwPort := gwLn.Addr().(*net.TCPAddr).Port
	time.Sleep(100 * time.Millisecond)
	t.Logf("agentgw running on port %d", gwPort)

	// ── 3. Dial agentgw WebSocket ──────────────────────────────────────────
	gwWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", gwPort)
	gwConn, _, err := websocket.DefaultDialer.Dial(gwWSURL, map[string][]string{
		"Authorization": {"Bearer " + token},
	})
	if err != nil {
		t.Fatalf("dial agentgw ws: %v", err)
	}
	defer gwConn.Close()

	// ── 4. node.add: register agentd as a node ─────────────────────────────
	addResp, err := rpcCall(t, gwConn, 1, "node.add", map[string]any{
		"name":       "test-node",
		"host":       "127.0.0.1",
		"agentdPort": float64(agentdPort),
		"token":      token,
	})
	if err != nil {
		t.Fatalf("node.add failed: %v", err)
	}
	if addResp["error"] != nil {
		t.Fatalf("node.add error: %v", addResp["error"])
	}

	// Wait for postSend auto-connect to finish
	time.Sleep(800 * time.Millisecond)

	// ── 5. node.list: verify node status is "connected" ────────────────────
	listResp, err := rpcCall(t, gwConn, 2, "node.list", nil)
	if err != nil {
		t.Fatalf("node.list failed: %v", err)
	}
	if listResp["error"] != nil {
		t.Fatalf("node.list error: %v", listResp["error"])
	}

	resultBytes, _ := json.Marshal(listResp["result"])
	t.Logf("node.list raw result: %s", string(resultBytes))
	var nodes []map[string]any
	if err := json.Unmarshal(resultBytes, &nodes); err != nil {
		t.Fatalf("parse node.list result: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatalf("expected at least 1 node in node.list, got empty array")
	}

	foundConnected := false
	var nodeID string
	for _, n := range nodes {
		if status, ok := n["status"].(string); ok && status == "connected" {
			foundConnected = true
			nodeID, _ = n["id"].(string)
			break
		}
	}
	if !foundConnected {
		t.Fatalf("expected node status 'connected', got %+v", nodes)
	}
	if nodeID == "" {
		t.Fatal("connected node has empty id")
	}
	t.Logf("node %s is connected", nodeID)

	// ── 6. agent.list via agentgw proxy (should be empty initially) ────────
	agentListResp, err := rpcCall(t, gwConn, 3, "agent.list", map[string]any{
		"nodeId": nodeID,
	})
	if err != nil {
		t.Fatalf("agent.list proxy failed: %v", err)
	}
	if agentListResp["error"] != nil {
		t.Fatalf("agent.list proxy error: %v", agentListResp["error"])
	}

	var agentResult map[string]any
	agentResultBytes, _ := json.Marshal(agentListResp["result"])
	if err := json.Unmarshal(agentResultBytes, &agentResult); err != nil {
		var agents []any
		if err2 := json.Unmarshal(agentResultBytes, &agents); err2 != nil {
			t.Fatalf("parse agent.list result: %v", err)
		}
		agentResult = map[string]any{"agents": agents}
	}

	if _, ok := agentResult["agents"]; !ok {
		t.Fatalf("expected 'agents' key in agent.list result, got %+v", agentResult)
	}

	// ── 7. Create an agent on agentd directly and verify proxy again ───────
	agentdWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", agentdPort)
	agentdConn, _, err := websocket.DefaultDialer.Dial(agentdWSURL, map[string][]string{
		"Authorization": {"Bearer " + token},
	})
	if err != nil {
		t.Fatalf("dial agentd ws: %v", err)
	}
	defer agentdConn.Close()

	createResp, err := rpcCall(t, agentdConn, 10, "agent.create", map[string]any{
		"name":    "integration-echo",
		"cmd":     "echo",
		"args":    []string{"hello"},
		"workDir": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("agent.create failed: %v", err)
	}
	if createResp["error"] != nil {
		t.Fatalf("agent.create error: %v", createResp["error"])
	}

	// Now query via proxy again
	agentListResp2, err := rpcCall(t, gwConn, 4, "agent.list", map[string]any{
		"nodeId": nodeID,
	})
	if err != nil {
		t.Fatalf("agent.list proxy (2nd) failed: %v", err)
	}
	if agentListResp2["error"] != nil {
		t.Fatalf("agent.list proxy (2nd) error: %v", agentListResp2["error"])
	}

	var agentResult2 map[string]any
	agentResultBytes2, _ := json.Marshal(agentListResp2["result"])
	if err := json.Unmarshal(agentResultBytes2, &agentResult2); err != nil {
		var agents []any
		if err2 := json.Unmarshal(agentResultBytes2, &agents); err2 != nil {
			t.Fatalf("parse agent.list result (2nd): %v", err)
		}
		agentResult2 = map[string]any{"agents": agents}
	}

	agents, ok := agentResult2["agents"].([]any)
	if !ok {
		t.Fatalf("expected 'agents' array in agent.list result, got %+v", agentResult2)
	}
	if len(agents) == 0 {
		t.Fatal("expected at least 1 agent after create via proxy")
	}

	t.Logf("TEST-002 passed: %d node(s) connected, %d agent(s) proxied", len(nodes), len(agents))
}

// TestEndToEndSessionLifecycle verifies the complete session lifecycle through agentgw proxy:
//   1. Start real agentd on ephemeral port
//   2. Start agentgw, register agentd as a node
//   3. Create agent via agentgw proxy
//   4. Verify agent.list via proxy shows the new agent
//   5. Send conversation message via proxy
//   6. Verify conversation history via proxy
//   7. Stop agent via proxy
//   8. Remove agent via proxy
//   9. Verify agent is gone from agent.list via proxy
func TestEndToEndSessionLifecycle(t *testing.T) {
	const token = "e2e-lifecycle-token"

	// ── 1. Start agentd on ephemeral port ──────────────────────────────────
	agentdProc, agentdPort, _ := startAgentd(t, token)
	defer func() {
		agentdProc.Kill()
		agentdProc.Wait()
	}()

	t.Logf("agentd running on port %d", agentdPort)

	// ── 2. Start agentgw on ephemeral port ─────────────────────────────────
	nodesFile := filepath.Join(t.TempDir(), "nodes.json")
	if err := os.WriteFile(nodesFile, []byte("{}"), 0600); err != nil {
		t.Fatalf("write nodes file: %v", err)
	}

	nodeStore := nodecfg.New(nodesFile)
	nodeMgr := node.NewManager(nodeStore, nil)
	gwSrv := ws.New(nodeMgr, token)

	gwLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("agentgw listen: %v", err)
	}
	defer gwLn.Close()

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/ws", gwSrv)
		if err := http.Serve(gwLn, mux); err != nil && !strings.Contains(err.Error(), "use of closed") {
			t.Logf("agentgw serve ended: %v", err)
		}
	}()

	gwPort := gwLn.Addr().(*net.TCPAddr).Port
	time.Sleep(100 * time.Millisecond)
	t.Logf("agentgw running on port %d", gwPort)

	// ── 3. Dial agentgw WebSocket ──────────────────────────────────────────
	gwWSURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", gwPort)
	gwConn, _, err := websocket.DefaultDialer.Dial(gwWSURL, map[string][]string{
		"Authorization": {"Bearer " + token},
	})
	if err != nil {
		t.Fatalf("dial agentgw ws: %v", err)
	}
	defer gwConn.Close()

	// ── 4. Register agentd as a node ───────────────────────────────────────
	addResp, err := rpcCall(t, gwConn, 1, "node.add", map[string]any{
		"name":       "e2e-node",
		"host":       "127.0.0.1",
		"agentdPort": float64(agentdPort),
		"token":      token,
	})
	if err != nil {
		t.Fatalf("node.add failed: %v", err)
	}
	if addResp["error"] != nil {
		t.Fatalf("node.add error: %v", addResp["error"])
	}

	// Wait for auto-connect
	time.Sleep(800 * time.Millisecond)

	// Get node ID
	listResp, err := rpcCall(t, gwConn, 2, "node.list", nil)
	if err != nil {
		t.Fatalf("node.list failed: %v", err)
	}
	var nodes []map[string]any
	listBytes, _ := json.Marshal(listResp["result"])
	json.Unmarshal(listBytes, &nodes)

	var nodeID string
	for _, n := range nodes {
		if status, _ := n["status"].(string); status == "connected" {
			nodeID, _ = n["id"].(string)
			break
		}
	}
	if nodeID == "" {
		t.Fatal("no connected node found")
	}
	t.Logf("node %s connected", nodeID)

	// ── Step A: Create agent via proxy ─────────────────────────────────────
	createResp, err := rpcCall(t, gwConn, 10, "agent.create", map[string]any{
		"nodeId":  nodeID,
		"name":    "e2e-lifecycle-agent",
		"cmd":     "cat",
		"workDir": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("agent.create via proxy failed: %v", err)
	}
	if createResp["error"] != nil {
		t.Fatalf("agent.create via proxy error: %v", createResp["error"])
	}

	createBytes, _ := json.Marshal(createResp["result"])
	var createResult map[string]any
	json.Unmarshal(createBytes, &createResult)
	agentID, ok := createResult["id"].(string)
	if !ok || agentID == "" {
		t.Fatal("expected agent id in create response")
	}
	t.Logf("created agent %s via proxy", agentID)

	// ── Step B: Verify agent appears in list via proxy ─────────────────────
	agentListResp, err := rpcCall(t, gwConn, 11, "agent.list", map[string]any{
		"nodeId": nodeID,
	})
	if err != nil {
		t.Fatalf("agent.list via proxy failed: %v", err)
	}

	var agentListResult map[string]any
	agentListBytes, _ := json.Marshal(agentListResp["result"])
	if err := json.Unmarshal(agentListBytes, &agentListResult); err != nil {
		var agents []any
		if err2 := json.Unmarshal(agentListBytes, &agents); err2 != nil {
			t.Fatalf("parse agent.list result: %v", err)
		}
		agentListResult = map[string]any{"agents": agents}
	}

	agents, _ := agentListResult["agents"].([]any)
	found := false
	for _, raw := range agents {
		ag, _ := raw.(map[string]any)
		if id, _ := ag["id"].(string); id == agentID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("agent %s not found in agent.list via proxy", agentID)
	}
	t.Logf("agent %s confirmed in proxy list (%d agents)", agentID, len(agents))

	// ── Step C: Send message via proxy ─────────────────────────────────────
	sendResp, err := rpcCall(t, gwConn, 12, "conversation.send", map[string]any{
		"nodeId":  nodeID,
		"agentId": agentID,
		"message": "hello e2e test",
	})
	if err != nil {
		t.Fatalf("conversation.send via proxy failed: %v", err)
	}
	if sendResp["error"] != nil {
		t.Fatalf("conversation.send via proxy error: %v", sendResp["error"])
	}
	t.Logf("message sent to agent %s via proxy", agentID)

	// ── Step D: Verify conversation history via proxy ──────────────────────
	historyResp, err := rpcCall(t, gwConn, 13, "conversation.history", map[string]any{
		"nodeId":  nodeID,
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("conversation.history via proxy failed: %v", err)
	}
	if historyResp["error"] != nil {
		t.Fatalf("conversation.history via proxy error: %v", historyResp["error"])
	}

	historyBytes, _ := json.Marshal(historyResp["result"])
	var historyResult map[string]any
	json.Unmarshal(historyBytes, &historyResult)

	events, _ := historyResult["events"].([]any)
	if len(events) == 0 {
		t.Fatal("expected at least 1 event in conversation history after send via proxy")
	}
	t.Logf("conversation history has %d events via proxy", len(events))

	// ── Step E: Stop agent via proxy ───────────────────────────────────────
	stopResp, err := rpcCall(t, gwConn, 14, "agent.stop", map[string]any{
		"nodeId":  nodeID,
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("agent.stop via proxy failed: %v", err)
	}
	if stopResp["error"] != nil {
		t.Fatalf("agent.stop via proxy error: %v", stopResp["error"])
	}
	t.Logf("agent %s stopped via proxy", agentID)

	// Verify status is stopped
	agentListResp2, err := rpcCall(t, gwConn, 15, "agent.list", map[string]any{
		"nodeId": nodeID,
	})
	if err != nil {
		t.Fatalf("agent.list (post-stop) via proxy failed: %v", err)
	}

	var agentListResult2 map[string]any
	agentListBytes2, _ := json.Marshal(agentListResp2["result"])
	if err := json.Unmarshal(agentListBytes2, &agentListResult2); err != nil {
		var agents2 []any
		if err2 := json.Unmarshal(agentListBytes2, &agents2); err2 != nil {
			t.Fatalf("parse agent.list result (post-stop): %v", err)
		}
		agentListResult2 = map[string]any{"agents": agents2}
	}

	agents2, _ := agentListResult2["agents"].([]any)
	var stoppedAgent map[string]any
	for _, raw := range agents2 {
		ag, _ := raw.(map[string]any)
		if id, _ := ag["id"].(string); id == agentID {
			stoppedAgent = ag
			break
		}
	}
	if stoppedAgent == nil {
		t.Fatalf("agent %s missing from list after stop via proxy", agentID)
	}
	status, _ := stoppedAgent["status"].(string)
	if status != "stopped" {
		t.Fatalf("expected status 'stopped' after agent.stop via proxy, got %q", status)
	}

	// ── Step F: Remove agent via proxy ─────────────────────────────────────
	removeResp, err := rpcCall(t, gwConn, 16, "agent.remove", map[string]any{
		"nodeId":  nodeID,
		"agentId": agentID,
	})
	if err != nil {
		t.Fatalf("agent.remove via proxy failed: %v", err)
	}
	if removeResp["error"] != nil {
		t.Fatalf("agent.remove via proxy error: %v", removeResp["error"])
	}
	t.Logf("agent %s removed via proxy", agentID)

	// Verify agent is gone
	agentListResp3, err := rpcCall(t, gwConn, 17, "agent.list", map[string]any{
		"nodeId": nodeID,
	})
	if err != nil {
		t.Fatalf("agent.list (post-remove) via proxy failed: %v", err)
	}

	var agentListResult3 map[string]any
	agentListBytes3, _ := json.Marshal(agentListResp3["result"])
	if err := json.Unmarshal(agentListBytes3, &agentListResult3); err != nil {
		var agents3 []any
		if err2 := json.Unmarshal(agentListBytes3, &agents3); err2 != nil {
			t.Fatalf("parse agent.list result (post-remove): %v", err)
		}
		agentListResult3 = map[string]any{"agents": agents3}
	}

	agents3, _ := agentListResult3["agents"].([]any)
	for _, raw := range agents3 {
		ag, _ := raw.(map[string]any)
		if id, _ := ag["id"].(string); id == agentID {
			t.Fatalf("agent %s should have been removed from list via proxy", agentID)
		}
	}

	t.Logf("TEST-003 E2E session lifecycle passed: create -> list -> message -> history -> stop -> remove via agentgw proxy")
}
