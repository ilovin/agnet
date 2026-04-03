package ws

import (
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentd/internal/agent"
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

// Server is the WebSocket HTTP handler.
type Server struct {
	manager *agent.Manager
	token   string

	mu      sync.RWMutex
	clients map[*websocket.Conn]*client
}

func New(mgr *agent.Manager, token string) *Server {
	srv := &Server{
		manager: mgr,
		token:   token,
		clients: make(map[*websocket.Conn]*client),
	}
	// Wire PTY output → broadcast to all WS clients
	mgr.SetOnOutput(func(agentID string, data map[string]any) {
		params := map[string]any{
			"agentId": agentID,
			"role":    data["role"],
			"text":    data["text"],
		}
		// Pass through raw flag if present
		if raw, ok := data["raw"].(bool); ok {
			params["raw"] = raw
		}
		srv.broadcast(RPCEvent{
			JSONRPC: "2.0",
			Method:  "conversation.message",
			Params:  params,
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

	s.mu.Lock()
	s.clients[conn] = c
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	h := &handler{server: s, conn: conn, self: c}
	h.loop()
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
