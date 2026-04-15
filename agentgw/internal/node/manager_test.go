package node_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/node"
	"github.com/phone-talk/agentgw/internal/nodecfg"
)

var testUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func newNodeTestAgentd(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var req map[string]any
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			_ = conn.WriteJSON(map[string]any{
				"jsonrpc": "2.0",
				"id":      req["id"],
				"result":  map[string]any{"ok": true},
			})
		}
	}))
}

func TestAddAndListNode(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(cfgPath)
	mgr := node.NewManager(store, nil) // nil agentd embed for tests

	id, err := mgr.Add(nodecfg.NodeEntry{
		Name: "remote1", Host: "192.168.1.10",
		SSHPort: 22, AgentdPort: 7373, Token: "tok",
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty node id")
	}

	nodes := mgr.List()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].ID != id {
		t.Errorf("expected id=%q, got %q", id, nodes[0].ID)
	}
	if nodes[0].GetStatus() != node.StatusDisconnected {
		t.Errorf("expected Disconnected status, got %v", nodes[0].GetStatus())
	}
}

func TestRemoveNode(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(cfgPath)
	mgr := node.NewManager(store, nil)

	id, _ := mgr.Add(nodecfg.NodeEntry{
		Name: "r1", Host: "10.0.0.1", SSHPort: 22, AgentdPort: 7373, Token: "t",
	})
	if err := mgr.Remove(id); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if len(mgr.List()) != 0 {
		t.Error("expected empty list after remove")
	}
}

func TestGetNode(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(cfgPath)
	mgr := node.NewManager(store, nil)

	id, _ := mgr.Add(nodecfg.NodeEntry{
		Name: "r2", Host: "10.0.0.2", SSHPort: 22, AgentdPort: 7373, Token: "t",
	})
	n := mgr.Get(id)
	if n == nil {
		t.Fatal("expected non-nil node")
	}
	if n.Name != "r2" {
		t.Errorf("expected Name=r2, got %q", n.Name)
	}

	// non-existent
	if mgr.Get("bad-id") != nil {
		t.Error("expected nil for unknown id")
	}
}

func TestRestartUsesRemoteDirInRestartHook(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(cfgPath)
	mgr := node.NewManager(store, nil)

	agentd := newNodeTestAgentd(t)
	defer agentd.Close()
	parsed, err := url.Parse(agentd.URL)
	if err != nil {
		t.Fatalf("parse fake agentd url: %v", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("parse fake agentd port: %v", err)
	}

	id, err := mgr.Add(nodecfg.NodeEntry{
		Name: "remote1", Host: "127.0.0.1", SSHPort: 22, AgentdPort: port, Token: "tok",
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	called := false
	mgr.SetRestartFunc(func(n *node.Node, remoteDir string) error {
		called = true
		if n.ID != id {
			t.Fatalf("expected node id %q, got %q", id, n.ID)
		}
		if remoteDir != "/custom/agentd" {
			t.Fatalf("expected remoteDir /custom/agentd, got %q", remoteDir)
		}
		return nil
	})

	if err := mgr.Restart(id, "/custom/agentd"); err != nil {
		t.Fatalf("Restart failed: %v", err)
	}
	if !called {
		t.Fatal("expected restart hook to be called")
	}
	if got := mgr.Get(id).GetStatus(); got != node.StatusConnected {
		t.Fatalf("expected connected status, got %s", got)
	}
}
