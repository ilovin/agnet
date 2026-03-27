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

// Server is the WebSocket HTTP handler.
type Server struct {
	manager *agent.Manager
	token   string

	mu      sync.RWMutex
	clients map[*websocket.Conn]struct{}
}

func New(mgr *agent.Manager, token string) *Server {
	return &Server{
		manager: mgr,
		token:   token,
		clients: make(map[*websocket.Conn]struct{}),
	}
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

	s.mu.Lock()
	s.clients[conn] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	h := &handler{server: s, conn: conn}
	h.loop()
}

// broadcast sends an event to all connected clients except the excluded one.
// Pass nil for exclude to send to all clients.
func (s *Server) broadcast(ev RPCEvent, exclude *websocket.Conn) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for conn := range s.clients {
		if conn == exclude {
			continue
		}
		_ = conn.WriteJSON(ev)
	}
}
