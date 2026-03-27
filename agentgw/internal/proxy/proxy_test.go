package proxy_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/proxy"
)

type fakeAgentd struct {
	mu       sync.Mutex
	received []map[string]any
	pushCh   chan map[string]any
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func newFakeAgentd(t *testing.T) (*fakeAgentd, *httptest.Server) {
	fa := &fakeAgentd{pushCh: make(chan map[string]any, 10)}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()
		go func() {
			for {
				var msg map[string]any
				if err := conn.ReadJSON(&msg); err != nil {
					return
				}
				fa.mu.Lock()
				fa.received = append(fa.received, msg)
				fa.mu.Unlock()
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"result":  map[string]any{"echo": msg["method"]},
				}
				conn.WriteJSON(resp)
			}
		}()
		for ev := range fa.pushCh {
			conn.WriteJSON(ev)
		}
	}))
	return fa, ts
}

func TestProxyForwardsRequest(t *testing.T) {
	fa, ts := newFakeAgentd(t)
	defer ts.Close()
	_ = fa

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	p, err := proxy.New(wsURL, "testtoken")
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	defer p.Close()

	resp, err := p.Call("agent.list", nil, 3*time.Second)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}
	b, _ := json.Marshal(resp)
	if !strings.Contains(string(b), "agent.list") {
		t.Errorf("expected echo of method in response, got: %s", b)
	}
}

func TestProxyReceivesEvents(t *testing.T) {
	fa, ts := newFakeAgentd(t)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	p, err := proxy.New(wsURL, "testtoken")
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	defer p.Close()

	events := make(chan map[string]any, 5)
	p.OnEvent(func(ev map[string]any) {
		events <- ev
	})

	fa.pushCh <- map[string]any{
		"jsonrpc": "2.0",
		"method":  "agent.status_changed",
		"params":  map[string]any{"agentId": "a1", "status": "working"},
	}

	select {
	case ev := <-events:
		if ev["method"] != "agent.status_changed" {
			t.Errorf("unexpected event method: %v", ev["method"])
		}
	case <-time.After(3 * time.Second):
		t.Error("timeout waiting for event")
	}
}
