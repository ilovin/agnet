package hub

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Hub bridges agentapp connections to local agentgw via reverse tunnels.
type Hub struct {
	mu       sync.RWMutex
	tunnels  map[string]*websocket.Conn // userID -> agentgw reverse ws
	appConns map[string]*websocket.Conn // userID -> current app ws (for tracking)
	users    map[string]string          // userID -> password
}

func New(users map[string]string) *Hub {
	return &Hub{
		tunnels:  make(map[string]*websocket.Conn),
		appConns: make(map[string]*websocket.Conn),
		users:    users,
	}
}

// RegisterTunnel handles the outbound WebSocket from user-local agentgw.
func (h *Hub) RegisterTunnel(w http.ResponseWriter, r *http.Request) {
	userID, ok := h.auth(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("tunnel upgrade error: %v", err)
		return
	}
	defer conn.Close()

	h.mu.Lock()
	old := h.tunnels[userID]
	h.tunnels[userID] = conn
	h.mu.Unlock()
	if old != nil {
		old.Close()
	}

	log.Printf("[Hub] tunnel registered for user=%s", userID)

	// Keep connection alive; when closed, remove from map.
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return nil
	})

	// Send periodic pings so agentgw side doesn't idle-timeout.
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
					conn.Close()
					return
				}
			}
		}
	}()

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			log.Printf("[Hub] tunnel closed for user=%s: %v", userID, err)
			break
		}
	}
	close(done)

	h.mu.Lock()
	if h.tunnels[userID] == conn {
		delete(h.tunnels, userID)
	}
	h.mu.Unlock()
	log.Printf("[Hub] tunnel unregistered for user=%s", userID)
}

// BridgeApp handles the WebSocket connection from agentapp.
func (h *Hub) BridgeApp(w http.ResponseWriter, r *http.Request) {
	// Expected path: /ws/:userID
	userID := strings.TrimPrefix(r.URL.Path, "/ws/")
	if userID == "" {
		http.Error(w, "missing userID", http.StatusBadRequest)
		return
	}

	// In production, auth middleware (e.g. oauth2-proxy) runs before this.
	// For PoC we accept a query token or rely on upstream auth.

	h.mu.RLock()
	tunnel := h.tunnels[userID]
	h.mu.RUnlock()
	if tunnel == nil {
		http.Error(w, "agentgw offline", http.StatusBadGateway)
		return
	}

	appConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("app upgrade error: %v", err)
		return
	}
	defer appConn.Close()

	h.mu.Lock()
	oldApp := h.appConns[userID]
	h.appConns[userID] = appConn
	h.mu.Unlock()
	if oldApp != nil {
		oldApp.Close()
	}

	log.Printf("[Hub] app connected for user=%s", userID)

	// Bidirectional copy.
	errCh := make(chan error, 2)
	go func() {
		for {
			t, data, err := appConn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if err := tunnel.WriteMessage(t, data); err != nil {
				errCh <- err
				return
			}
		}
	}()
	go func() {
		for {
			t, data, err := tunnel.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if err := appConn.WriteMessage(t, data); err != nil {
				errCh <- err
				return
			}
		}
	}()

	<-errCh
	log.Printf("[Hub] app disconnected for user=%s", userID)

	h.mu.Lock()
	if h.appConns[userID] == appConn {
		delete(h.appConns, userID)
	}
	h.mu.Unlock()
}

func (h *Hub) auth(r *http.Request) (string, bool) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		userID = "default"
	}
	pass, ok := h.users[userID]
	if !ok {
		return "", false
	}
	if token != pass {
		return "", false
	}
	return userID, true
}
