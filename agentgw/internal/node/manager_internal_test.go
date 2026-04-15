package node

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	"github.com/phone-talk/agentgw/internal/proxy"
	"github.com/phone-talk/agentgw/internal/tunnel"
)

var managerTestUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func newManagerTestAgentd(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := managerTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestBuildRestartCommandExpandsHomeCandidates(t *testing.T) {
	cmd := buildRestartCommand("/opt/custom")
	if want := "\"$HOME/bin/agentd\""; !strings.Contains(cmd, want) {
		t.Fatalf("expected command to include %s, got %q", want, cmd)
	}
	if want := "\"$HOME/agentd\""; !strings.Contains(cmd, want) {
		t.Fatalf("expected command to include %s, got %q", want, cmd)
	}
}

func TestProxyCloseUpdatesNodeStatus(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(cfgPath)
	mgr := NewManager(store, nil)

	agentd := newManagerTestAgentd(t)
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
		Name: "local", Host: "127.0.0.1", SSHPort: 22, AgentdPort: port, Token: "tok",
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	events := make(chan map[string]any, 1)
	mgr.OnEvent(func(nodeID string, ev map[string]any) {
		if nodeID != id {
			return
		}
		if method, _ := ev["method"].(string); method == "node.status_changed" {
			events <- ev
		}
	})

	if err := mgr.Connect(id); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	n := mgr.Get(id)
	if got := n.GetStatus(); got != StatusConnected {
		t.Fatalf("expected connected status, got %s", got)
	}
	p := n.GetProxy()
	if p == nil {
		t.Fatal("expected proxy after connect")
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	waitFor(t, 2*time.Second, func() bool {
		return n.GetStatus() == StatusDisconnected && n.GetProxy() == nil
	})

	select {
	case ev := <-events:
		params, _ := ev["params"].(map[string]any)
		if params["nodeId"] != id {
			t.Fatalf("expected nodeId %q, got %v", id, params["nodeId"])
		}
		if params["status"] != string(StatusDisconnected) {
			t.Fatalf("expected disconnected event, got %v", params["status"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected disconnect event")
	}
}

func TestHandleProxyDisconnectClearsTunnelState(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(cfgPath)
	mgr := NewManager(store, nil)

	id, err := mgr.Add(nodecfg.NodeEntry{
		Name: "remote", Host: "10.0.0.1", SSHPort: 22, AgentdPort: 7373, Token: "tok",
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	n := mgr.Get(id)
	p := &proxy.Proxy{}
	tun := &tunnel.Tunnel{}
	n.proxy = p
	n.tunnel = tun
	n.status = StatusConnected

	mgr.handleProxyDisconnect(n, p)

	if got := n.GetStatus(); got != StatusDisconnected {
		t.Fatalf("expected disconnected status, got %s", got)
	}
	if n.GetProxy() != nil {
		t.Fatal("expected proxy to be cleared")
	}
	if n.tunnel != nil {
		t.Fatal("expected tunnel to be cleared")
	}
}
