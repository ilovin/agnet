package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// RPCRequest represents a JSON-RPC 2.0 request.
type RPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// RPCResponse represents a JSON-RPC 2.0 response.
type RPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC 2.0 error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// RPCEvent represents a JSON-RPC 2.0 event (notification).
type RPCEvent struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Client manages a WebSocket connection to the agent gateway.
type Client struct {
	url       string
	token     string
	conn      *websocket.Conn
	mu        sync.RWMutex
	idCounter int64

	// Event handlers
	handlers  map[string][]func(params any)
	handlerMu sync.RWMutex

	// Pending requests
	pending   map[float64]chan *RPCResponse
	pendingMu sync.Mutex

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a new CLI client.
func New(url, token string) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		url:      url,
		token:    token,
		handlers: make(map[string][]func(params any)),
		pending:  make(map[float64]chan *RPCResponse),
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
}

// Connect establishes a WebSocket connection to the gateway.
func (c *Client) Connect() error {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+c.token)

	conn, _, err := websocket.DefaultDialer.Dial(c.url, headers)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.url, err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	go c.readLoop()

	return nil
}

// Close closes the WebSocket connection.
func (c *Client) Close() error {
	c.cancel()
	
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn != nil {
		conn.Close()
	}

	<-c.done
	return nil
}

// Call sends an RPC request and waits for the response.
func (c *Client) Call(method string, params map[string]any) (*RPCResponse, error) {
	id := atomic.AddInt64(&c.idCounter, 1)
	
	req := RPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	respCh := make(chan *RPCResponse, 1)
	idKey := float64(id)
	
	c.pendingMu.Lock()
	c.pending[idKey] = respCh
	c.pendingMu.Unlock()

	if err := c.writeJSON(req); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, idKey)
		c.pendingMu.Unlock()
		return nil, err
	}

	select {
	case resp := <-respCh:
		return resp, nil
	case <-time.After(30 * time.Second):
		c.pendingMu.Lock()
		delete(c.pending, idKey)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("timeout waiting for response")
	case <-c.ctx.Done():
		return nil, fmt.Errorf("client closed")
	}
}

// OnEvent registers an event handler.
func (c *Client) OnEvent(method string, handler func(params any)) {
	c.handlerMu.Lock()
	defer c.handlerMu.Unlock()
	c.handlers[method] = append(c.handlers[method], handler)
}

// writeJSON sends a JSON message over the WebSocket.
func (c *Client) writeJSON(v any) error {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return fmt.Errorf("not connected")
	}

	return conn.WriteJSON(v)
}

// readLoop continuously reads messages from the WebSocket.
func (c *Client) readLoop() {
	defer close(c.done)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.mu.RLock()
		conn := c.conn
		c.mu.RUnlock()

		if conn == nil {
			return
		}

		msgType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return
			}
			select {
			case <-c.ctx.Done():
				return
			default:
				log.Printf("[Client] read error: %v", err)
				return
			}
		}

		if msgType != websocket.TextMessage {
			continue
		}

		// Try to parse as response first (has id field)
		var resp RPCResponse
		if err := json.Unmarshal(data, &resp); err == nil && resp.ID != nil {
			c.handleResponse(&resp)
			continue
		}

		// Try to parse as event (no id field, has method)
		var event RPCEvent
		if err := json.Unmarshal(data, &event); err == nil && event.Method != "" {
			c.handleEvent(&event)
			continue
		}

		log.Printf("[Client] unknown message: %s", string(data))
	}
}

func idToFloat64(id any) float64 {
	switch v := id.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	default:
		return 0
	}
}

// handleResponse routes a response to the pending request.
func (c *Client) handleResponse(resp *RPCResponse) {
	idKey := idToFloat64(resp.ID)
	if idKey == 0 {
		return
	}

	c.pendingMu.Lock()
	ch, ok := c.pending[idKey]
	if ok {
		delete(c.pending, idKey)
	}
	c.pendingMu.Unlock()

	if ok {
		ch <- resp
	}
}

// handleEvent dispatches an event to registered handlers.
func (c *Client) handleEvent(event *RPCEvent) {
	c.handlerMu.RLock()
	handlers := c.handlers[event.Method]
	c.handlerMu.RUnlock()

	for _, h := range handlers {
		go h(event.Params)
	}
}
