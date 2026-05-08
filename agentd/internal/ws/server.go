package ws

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentd/internal/agent"
)

const (
	// pingInterval is how often the server sends a WebSocket ping to clients.
	pingInterval = 25 * time.Second
	// pongTimeout is how long the server waits for a pong before closing.
	pongTimeout = 60 * time.Second
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// client wraps a WebSocket connection with its own write mutex.
// gorilla/websocket connections are not safe for concurrent writes.
type client struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (c *client) writeJSON(v any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(v)
}

func (c *client) writePing() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
}

type providerDataCache struct {
	mu            sync.Mutex
	rows          []map[string]any
	resp          RPCResponse
	ok            bool
	currentID     string
	currentReason string
	runtimeID     string
	runtimeReason string
	at            time.Time
}

// Server is the WebSocket HTTP handler.
type Server struct {
	manager *agent.Manager
	token   string
	nodeID  string

	mu      sync.RWMutex
	clients map[*websocket.Conn]*client

	providerCache providerDataCache
	providerDBMu  sync.Mutex
}

func New(mgr *agent.Manager, token string, nodeID string) *Server {
	if nodeID == "" {
		nodeID = "local"
	}
	srv := &Server{
		manager:       mgr,
		token:         token,
		nodeID:        nodeID,
		clients:       make(map[*websocket.Conn]*client),
		providerCache: providerDataCache{},
	}
	// Wire PTY output → broadcast to all WS clients
	mgr.SetOnOutput(func(agentID string, data map[string]any) {
		// Handle message updates (streaming text that grew)
		if isUpdate, _ := data["_update"].(bool); isUpdate {
			params := map[string]any{
				"agentId": agentID,
				"msg_id":  data["msg_id"],
				"text":    data["text"],
				"seq":     data["seq"],
			}
			srv.broadcast(RPCEvent{
				JSONRPC: "2.0",
				Method:  "conversation.message_update",
				Params:  params,
			}, nil)
			return
		}
		params := map[string]any{
			"agentId": agentID,
			"role":    data["role"],
			"text":    data["text"],
		}
		// Pass through raw flag if present
		if raw, ok := data["raw"].(bool); ok {
			params["raw"] = raw
		}
		if seq, ok := data["seq"]; ok {
			params["seq"] = seq
		}
		if msgID, ok := data["msg_id"].(string); ok && msgID != "" {
			params["msg_id"] = msgID
		}
		srv.broadcast(RPCEvent{
			JSONRPC: "2.0",
			Method:  "conversation.message",
			Params:  params,
		}, nil)
	})

	// Wire agent status changes → broadcast to all WS clients
	mgr.SetOnStatusChange(func(agentID string, data map[string]any) {
		params, _ := data["params"].(map[string]any)
		status := any(nil)
		if params != nil {
			status = params["status"]
		}
		enriched := (&handler{server: srv}).statusChangedParams(agentID, status)
		if params != nil {
			if name, ok := params["name"]; ok {
				enriched["name"] = name
			}
		}
		srv.broadcast(RPCEvent{
			JSONRPC: "2.0",
			Method:  "agent.status_changed",
			Params:  enriched,
		}, nil)
	})
	return srv
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Auth: accept either Authorization header or ?token= query param
	// (Flutter mobile clients can't set custom WS headers)
	auth := r.Header.Get("Authorization")
	queryToken := r.URL.Query().Get("token")
	headerToken := strings.TrimPrefix(auth, "Bearer ")
	token := headerToken
	if token == "" {
		token = queryToken
	}
	if token != s.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	c := &client{conn: conn}

	// Set up pong handler: reset read deadline on each pong received.
	conn.SetReadDeadline(time.Now().Add(pongTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongTimeout))
		return nil
	})

	s.mu.Lock()
	s.clients[conn] = c
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	// Start ping ticker goroutine; stops when connection closes.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := c.writePing(); err != nil {
					log.Printf("ws ping: %v", err)
					conn.Close()
					return
				}
			}
		}
	}()

	h := &handler{server: s, conn: conn, self: c}
	h.loop()
}

// ClientCount returns the number of currently connected WebSocket clients.
func (s *Server) ClientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// broadcast sends an event to all connected clients except the excluded one.
// Pass nil for exclude to send to all clients.
func (s *Server) broadcast(ev RPCEvent, exclude *websocket.Conn) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for conn, c := range s.clients {
		if conn == exclude {
			continue
		}
		_ = c.writeJSON(ev)
	}
}
