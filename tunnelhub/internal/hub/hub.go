package hub

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/tunnelhub/internal/sso"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type tunnelEntry struct {
	conn    *websocket.Conn
	mu      sync.Mutex
	appConn *websocket.Conn
}

// Hub bridges agentapp connections to local agentgw via reverse tunnels.
type Hub struct {
	mu        sync.RWMutex
	tunnels   map[string]*tunnelEntry // userID -> agentgw reverse ws
	users     map[string]string       // userID -> password (local mode)
	validator *sso.Validator          // nil when OpenSSO is disabled
}

func New(users map[string]string, validator *sso.Validator) *Hub {
	return &Hub{
		tunnels:   make(map[string]*tunnelEntry),
		users:     users,
		validator: validator,
	}
}

// auth validates the request and returns the resolved userID.
func (h *Hub) auth(r *http.Request) (string, bool) {
	token := extractToken(r)
	if token == "" {
		return "", false
	}

	// OpenSSO mode takes precedence when configured.
	if h.validator != nil {
		profile, err := h.validator.Validate(token)
		if err != nil {
			log.Printf("[Hub] sso validation failed: %v", err)
			return "", false
		}
		return profile.UserID, true
	}

	// Fallback to local static user map (LAN / simple mode).
	userID := r.URL.Query().Get("userId")
	if userID == "" {
		userID = "default"
	}
	pass, ok := h.users[userID]
	if !ok || token != pass {
		return "", false
	}
	return userID, true
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

	entry := &tunnelEntry{conn: conn}

	h.mu.Lock()
	old := h.tunnels[userID]
	h.tunnels[userID] = entry
	h.mu.Unlock()
	if old != nil {
		old.conn.Close()
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

	// Single reader goroutine for the tunnel: forwards to active app conn.
	go h.tunnelReader(userID, entry, done)

	// Block until the tunnel connection closes (tunnelReader will close done).
	<-done

	h.mu.Lock()
	if h.tunnels[userID] == entry {
		delete(h.tunnels, userID)
	}
	h.mu.Unlock()
	log.Printf("[Hub] tunnel unregistered for user=%s", userID)
}

func (h *Hub) tunnelReader(userID string, entry *tunnelEntry, done chan struct{}) {
	defer close(done)
	for {
		mt, data, err := entry.conn.ReadMessage()
		if err != nil {
			return
		}
		entry.mu.Lock()
		appConn := entry.appConn
		entry.mu.Unlock()
		if appConn != nil {
			if err := appConn.WriteMessage(mt, data); err != nil {
				appConn.Close()
			}
		}
	}
}

// BridgeApp handles the WebSocket connection from agentapp.
func (h *Hub) BridgeApp(w http.ResponseWriter, r *http.Request) {
	// Expected path: /ws/:userID
	userID := strings.TrimPrefix(r.URL.Path, "/ws/")
	if userID == "" {
		http.Error(w, "missing userID", http.StatusBadRequest)
		return
	}

	token := extractToken(r)
	if token == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// OpenSSO mode
	if h.validator != nil {
		profile, err := h.validator.Validate(token)
		if err != nil {
			log.Printf("[Hub] app sso validation failed: %v", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if profile.UserID != userID {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	} else {
		// Local static mode
		pass, ok := h.users[userID]
		if !ok || token != pass {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	h.mu.RLock()
	entry := h.tunnels[userID]
	h.mu.RUnlock()
	if entry == nil {
		http.Error(w, "agentgw offline", http.StatusBadGateway)
		return
	}

	appConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("app upgrade error: %v", err)
		return
	}
	defer appConn.Close()

	entry.mu.Lock()
	oldApp := entry.appConn
	entry.appConn = appConn
	entry.mu.Unlock()
	if oldApp != nil {
		oldApp.Close()
	}

	log.Printf("[Hub] app connected for user=%s", userID)

	// Read from app and write to tunnel. tunnelReader handles tunnel -> app.
	for {
		t, data, err := appConn.ReadMessage()
		if err != nil {
			break
		}
		if err := entry.conn.WriteMessage(t, data); err != nil {
			break
		}
	}

	log.Printf("[Hub] app disconnected for user=%s", userID)

	entry.mu.Lock()
	if entry.appConn == appConn {
		entry.appConn = nil
	}
	entry.mu.Unlock()
}

func extractToken(r *http.Request) string {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return token
}
