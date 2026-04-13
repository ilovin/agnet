package ws

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/node"
)

const (
	// pingInterval is how often the server sends a WebSocket ping to clients.
	pingInterval = 25 * time.Second
	// pongTimeout is how long the server waits for a pong before closing.
	pongTimeout = 60 * time.Second
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

// client wraps a WebSocket connection with its own write mutex.
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

type Server struct {
	manager   *node.Manager
	token     string
	mu        sync.RWMutex
	clients   map[*websocket.Conn]*client
	startTime time.Time
}

func New(mgr *node.Manager, token string) *Server {
	return &Server{
		manager:   mgr,
		token:     token,
		clients:   make(map[*websocket.Conn]*client),
		startTime: time.Now(),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) Broadcast(ev RPCEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.clients {
		_ = c.writeJSON(ev)
	}
}
