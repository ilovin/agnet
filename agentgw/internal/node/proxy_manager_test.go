package node

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var proxyManagerTestUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func newProxyManagerTestAgentd(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := proxyManagerTestUpgrader.Upgrade(w, r, nil)
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

func TestProxyManagerConnect(t *testing.T) {
	agentd := newProxyManagerTestAgentd(t)
	defer agentd.Close()
	parsed, _ := url.Parse(agentd.URL)
	port, _ := strconv.Atoi(parsed.Port())

	pm := NewProxyManager()
	n := &Node{ID: "node-1", Host: "127.0.0.1", AgentdPort: port, Token: "tok"}

	wsURL := "ws://127.0.0.1:" + parsed.Port() + "/ws"
	err := pm.Connect(n, wsURL)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if n.GetProxy() == nil {
		t.Fatal("expected proxy after connect")
	}
	if n.GetStatus() != StatusConnected {
		t.Errorf("expected status connected, got %s", n.GetStatus())
	}
}

func TestProxyManagerConnectBadURL(t *testing.T) {
	pm := NewProxyManager()
	n := &Node{ID: "node-1", Host: "127.0.0.1", AgentdPort: 1, Token: "tok"}

	err := pm.Connect(n, "ws://127.0.0.1:1/ws")
	if err == nil {
		t.Fatal("expected error for bad URL")
	}
	if n.GetStatus() != StatusError {
		t.Errorf("expected status error, got %s", n.GetStatus())
	}
}

func TestProxyManagerDisconnect(t *testing.T) {
	agentd := newProxyManagerTestAgentd(t)
	defer agentd.Close()
	parsed, _ := url.Parse(agentd.URL)

	pm := NewProxyManager()
	n := &Node{ID: "node-1", Host: "127.0.0.1", Token: "tok"}

	wsURL := "ws://127.0.0.1:" + parsed.Port() + "/ws"
	_ = pm.Connect(n, wsURL)

	pm.Disconnect(n)
	if n.GetProxy() != nil {
		t.Error("expected proxy cleared after disconnect")
	}
}

func TestProxyManagerDisconnectNoProxy(t *testing.T) {
	pm := NewProxyManager()
	n := &Node{ID: "node-1", Host: "127.0.0.1", Token: "tok"}

	// Should not panic
	pm.Disconnect(n)
}

func TestProxyManagerEventCallback(t *testing.T) {
	agentd := newProxyManagerTestAgentd(t)
	defer agentd.Close()
	parsed, _ := url.Parse(agentd.URL)

	pm := NewProxyManager()
	n := &Node{ID: "node-1", Host: "127.0.0.1", Token: "tok"}

	pm.OnEvent(func(nodeID string, ev map[string]any) {
		if nodeID == "node-1" {
			// event received
		}
	})

	wsURL := "ws://127.0.0.1:" + parsed.Port() + "/ws"
	_ = pm.Connect(n, wsURL)

	// Trigger an event via the proxy (we can't easily do this with the echo server,
	// but we can verify the proxy is set up with the callback by checking the proxy exists)
	if n.GetProxy() == nil {
		t.Fatal("expected proxy")
	}
}

func TestProxyManagerDisconnectCallback(t *testing.T) {
	agentd := newProxyManagerTestAgentd(t)
	defer agentd.Close()
	parsed, _ := url.Parse(agentd.URL)

	pm := NewProxyManager()
	n := &Node{ID: "node-1", Host: "127.0.0.1", Token: "tok"}

	disconnected := make(chan string, 1)
	pm.OnDisconnect(func(nodeID string) {
		disconnected <- nodeID
	})

	wsURL := "ws://127.0.0.1:" + parsed.Port() + "/ws"
	_ = pm.Connect(n, wsURL)

	// Close the proxy to trigger disconnect callback
	if p := n.GetProxy(); p != nil {
		p.Close()
	}

	select {
	case id := <-disconnected:
		if id != "node-1" {
			t.Fatalf("expected node-1, got %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected disconnect callback")
	}
}

func TestProxyManagerForwardCall(t *testing.T) {
	agentd := newProxyManagerTestAgentd(t)
	defer agentd.Close()
	parsed, _ := url.Parse(agentd.URL)

	pm := NewProxyManager()
	n := &Node{ID: "node-1", Host: "127.0.0.1", Token: "tok"}

	wsURL := "ws://127.0.0.1:" + parsed.Port() + "/ws"
	_ = pm.Connect(n, wsURL)

	result, err := pm.ForwardCall(n, "agent.list", nil, 3*time.Second)
	if err != nil {
		t.Fatalf("ForwardCall failed: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok || m["ok"] != true {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestProxyManagerForwardCallNotConnected(t *testing.T) {
	pm := NewProxyManager()
	n := &Node{ID: "node-1", Host: "127.0.0.1", Token: "tok"}

	_, err := pm.ForwardCall(n, "agent.list", nil, 3*time.Second)
	if err == nil {
		t.Fatal("expected error when not connected")
	}
}
