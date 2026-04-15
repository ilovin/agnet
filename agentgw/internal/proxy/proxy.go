package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	proxyPingInterval = 25 * time.Second
	proxyPongTimeout  = 60 * time.Second
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
	writeMu sync.Mutex   // serializes all conn.WriteJSON calls
	mu      sync.Mutex   // protects pending map and nextID
	conn    *websocket.Conn
	pending map[float64]*pending
	nextID  float64

	eventMu      sync.RWMutex
	onEvent      func(map[string]any)
	disconnectMu sync.RWMutex
	onDisconnect func()
	closeOnce    sync.Once

	// Reconnection fields
	url       string
	token     string
	reconnect bool
	quit      chan struct{}
}

// New connects to the agentd WebSocket URL with the given token.
func New(url, token string) (*Proxy, error) {
	return NewWithReconnect(url, token, false)
}

// NewWithReconnect connects to the agentd WebSocket URL with optional auto-reconnect.
func NewWithReconnect(url, token string, reconnect bool) (*Proxy, error) {
	hdr := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	p := &Proxy{
		conn:      conn,
		pending:   make(map[float64]*pending),
		nextID:    1,
		url:       url,
		token:     token,
		reconnect: reconnect,
		quit:      make(chan struct{}),
	}
	p.setupPingPong()
	go p.readLoop()
	go p.pingLoop()
	return p, nil
}

// setupPingPong configures pong handler and initial read deadline on the connection.
func (p *Proxy) setupPingPong() {
	p.conn.SetReadDeadline(time.Now().Add(proxyPongTimeout))
	p.conn.SetPongHandler(func(string) error {
		p.conn.SetReadDeadline(time.Now().Add(proxyPongTimeout))
		return nil
	})
}

// pingLoop sends periodic WebSocket pings to keep the connection alive.
func (p *Proxy) pingLoop() {
	ticker := time.NewTicker(proxyPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.quit:
			return
		case <-ticker.C:
			p.writeMu.Lock()
			err := p.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			p.writeMu.Unlock()
			if err != nil {
				log.Printf("proxy ping: %v", err)
				return
			}
		}
	}
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

			// Attempt reconnection if enabled
			if p.reconnect {
				if reconnectErr := p.tryReconnect(); reconnectErr == nil {
					continue // Successfully reconnected, continue reading
				}
			}

			p.disconnectMu.RLock()
			cb := p.onDisconnect
			p.disconnectMu.RUnlock()
			if cb != nil {
				cb()
			}
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

// OnDisconnect registers a callback that fires when the proxy read loop exits
// after the connection cannot be recovered.
func (p *Proxy) OnDisconnect(fn func()) {
	p.disconnectMu.Lock()
	defer p.disconnectMu.Unlock()
	p.onDisconnect = fn
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

	p.writeMu.Lock()
	err := p.conn.WriteJSON(req)
	p.writeMu.Unlock()
	if err != nil {
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
	req := rpcMsg{JSONRPC: "2.0", Method: method, Params: params}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return p.conn.WriteJSON(req)
}

// tryReconnect attempts to reconnect with exponential backoff.
func (p *Proxy) tryReconnect() error {
	maxRetries := 5
	baseDelay := time.Second

	for i := 0; i < maxRetries; i++ {
		select {
		case <-p.quit:
			return fmt.Errorf("reconnect aborted: proxy closed")
		default:
		}

		delay := baseDelay * time.Duration(i+1)
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}

		time.Sleep(delay)

		hdr := http.Header{"Authorization": {"Bearer " + p.token}}
		conn, _, err := websocket.DefaultDialer.Dial(p.url, hdr)
		if err != nil {
			continue // Try again
		}

		p.mu.Lock()
		p.conn = conn
		p.mu.Unlock()

		// Re-setup ping/pong on the new connection and restart ping loop
		p.setupPingPong()
		go p.pingLoop()

		return nil // Successfully reconnected
	}

	return fmt.Errorf("failed to reconnect after %d attempts", maxRetries)
}

// Close closes the underlying WebSocket connection.
func (p *Proxy) Close() error {
	var err error
	p.closeOnce.Do(func() {
		close(p.quit)
		err = p.conn.Close()
	})
	return err
}
