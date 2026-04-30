package node

import (
	"fmt"
	"log"
	"time"

	"github.com/phone-talk/agentgw/internal/proxy"
)

// EventCallback is called when a node's agentd pushes an event.
type EventCallback func(nodeID string, event map[string]any)

// DisconnectCallback is called when a proxy disconnects.
type DisconnectCallback func(nodeID string)

// ProxyManager handles WebSocket proxy lifecycle.
type ProxyManager struct {
	onEvent      EventCallback
	onDisconnect DisconnectCallback
}

// NewProxyManager creates a ProxyManager.
func NewProxyManager() *ProxyManager {
	return &ProxyManager{}
}

// OnEvent registers a callback for agentd push events (nodeId is injected into params).
func (pm *ProxyManager) OnEvent(cb EventCallback) {
	pm.onEvent = cb
}

// OnDisconnect registers a callback that fires when a proxy disconnects.
func (pm *ProxyManager) OnDisconnect(cb DisconnectCallback) {
	pm.onDisconnect = cb
}

// Connect establishes a WS proxy to a node's agentd through the given wsURL.
func (pm *ProxyManager) Connect(n *Node, wsURL string) error {
	// Clean up any existing proxy
	if existing := n.GetProxy(); existing != nil {
		_ = existing.Close()
		n.SetProxy(nil)
	}

	// Enable auto-reconnect for resilient connections
	p, err := proxy.NewWithReconnect(wsURL, n.Token, true)
	if err != nil {
		n.SetStatus(StatusError)
		return fmt.Errorf("ws proxy: %w", err)
	}

	if pm.onEvent != nil {
		p.OnEvent(func(ev map[string]any) {
			if ev == nil {
				ev = make(map[string]any)
			}
			params, ok := ev["params"].(map[string]any)
			if !ok {
				params = make(map[string]any)
				ev["params"] = params
			}
			params["nodeId"] = n.ID
			pm.onEvent(n.ID, ev)
		})
	}
	p.OnDisconnect(func() {
		pm.handleDisconnect(n, p)
	})

	n.SetProxy(p)
	n.SetStatus(StatusConnected)
	log.Printf("node %q connected (%s)", n.Name, wsURL)
	return nil
}

// Disconnect closes the node's WS proxy and clears it.
func (pm *ProxyManager) Disconnect(n *Node) {
	if p := n.GetProxy(); p != nil {
		_ = p.Close()
		n.SetProxy(nil)
	}
}

// ForwardCall sends a JSON-RPC call to a specific node's agentd via its proxy.
func (pm *ProxyManager) ForwardCall(n *Node, method string, params map[string]any, timeout time.Duration) (any, error) {
	p := n.GetProxy()
	if p == nil {
		return nil, fmt.Errorf("node %q not connected", n.ID)
	}
	return p.Call(method, params, timeout)
}

func (pm *ProxyManager) handleDisconnect(n *Node, disconnected *proxy.Proxy) {
	n.mu.Lock()
	if n.proxy != disconnected {
		n.mu.Unlock()
		return
	}
	n.proxy = nil
	oldTunnel := n.tunnel
	n.tunnel = nil
	n.status = StatusDisconnected
	n.mu.Unlock()

	if oldTunnel != nil {
		_ = oldTunnel.Close()
	}

	if pm.onDisconnect != nil {
		pm.onDisconnect(n.ID)
	}
}
