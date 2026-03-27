package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type rpcMsg struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method,omitempty"`
	Params  any            `json:"params,omitempty"`
	Result  any            `json:"result,omitempty"`
	Error   map[string]any `json:"error,omitempty"`
}

type pending struct {
	ch chan rpcMsg
}

// Proxy is a WebSocket JSON-RPC client connected to a remote agentd.
type Proxy struct {
	mu      sync.Mutex
	conn    *websocket.Conn
	pending map[float64]*pending
	nextID  float64

	eventMu sync.RWMutex
	onEvent func(map[string]any)
}

// New connects to the agentd WebSocket URL with the given token.
func New(url, token string) (*Proxy, error) {
	hdr := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	p := &Proxy{
		conn:    conn,
		pending: make(map[float64]*pending),
		nextID:  1,
	}
	go p.readLoop()
	return p, nil
}

func (p *Proxy) readLoop() {
	for {
		_, data, err := p.conn.ReadMessage()
		if err != nil {
			p.mu.Lock()
			for _, pend := range p.pending {
				close(pend.ch)
			}
			p.pending = make(map[float64]*pending)
			p.mu.Unlock()
			return
		}
		var msg rpcMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID != nil {
			id, ok := msg.ID.(float64)
			if !ok {
				continue
			}
			p.mu.Lock()
			pend, exists := p.pending[id]
			if exists {
				delete(p.pending, id)
				pend.ch <- msg
			}
			p.mu.Unlock()
		} else if msg.Method != "" {
			p.eventMu.RLock()
			cb := p.onEvent
			p.eventMu.RUnlock()
			if cb != nil {
				raw := map[string]any{
					"jsonrpc": msg.JSONRPC,
					"method":  msg.Method,
					"params":  msg.Params,
				}
				cb(raw)
			}
		}
	}
}

// OnEvent registers a callback for server-push events (no id).
func (p *Proxy) OnEvent(fn func(map[string]any)) {
	p.eventMu.Lock()
	defer p.eventMu.Unlock()
	p.onEvent = fn
}

// Call sends a JSON-RPC request and waits for a response.
func (p *Proxy) Call(method string, params any, timeout time.Duration) (any, error) {
	p.mu.Lock()
	id := p.nextID
	p.nextID++
	ch := make(chan rpcMsg, 1)
	p.pending[id] = &pending{ch: ch}
	req := rpcMsg{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	p.mu.Unlock()

	if err := p.conn.WriteJSON(req); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("write: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("connection closed")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error: %v", resp.Error)
		}
		return resp.Result, nil
	case <-time.After(timeout):
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("timeout after %v", timeout)
	}
}

// Send sends a JSON-RPC request without waiting for a response.
func (p *Proxy) Send(method string, params any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	req := rpcMsg{JSONRPC: "2.0", Method: method, Params: params}
	return p.conn.WriteJSON(req)
}

// Close closes the underlying WebSocket connection.
func (p *Proxy) Close() error {
	return p.conn.Close()
}
