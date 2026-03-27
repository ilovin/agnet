package ws_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/node"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	"github.com/phone-talk/agentgw/internal/ws"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := nodecfg.New(filepath.Join(t.TempDir(), "nodes.yaml"))
	mgr := node.NewManager(store, nil)
	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func dialWS(t *testing.T, ts *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	hdr := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial(u, hdr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func rpc(t *testing.T, conn *websocket.Conn, method string, params any) map[string]any {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	conn.WriteJSON(req)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	return resp
}

func TestAuthRejectsWrongToken(t *testing.T) {
	ts := newTestServer(t)
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	_, resp, _ := websocket.DefaultDialer.Dial(u, http.Header{"Authorization": {"Bearer wrong"}})
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %v", resp)
	}
}

func TestNodeList(t *testing.T) {
	ts := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")
	resp := rpc(t, conn, "node.list", nil)
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	result, ok := resp["result"].([]any)
	if !ok {
		result = []any{}
	}
	if len(result) != 0 {
		t.Errorf("expected empty node list, got %d", len(result))
	}
}

func TestNodeAdd(t *testing.T) {
	ts := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")
	resp := rpc(t, conn, "node.add", map[string]any{
		"name": "remote1", "host": "10.0.0.1",
		"sshPort": 22, "agentdPort": 7373, "token": "agentd-tok",
	})
	if resp["error"] != nil {
		t.Fatalf("node.add error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp["result"])
	}
	if result["nodeId"] == "" {
		t.Error("expected non-empty nodeId")
	}
}

func TestUnknownMethod(t *testing.T) {
	ts := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")
	resp := rpc(t, conn, "bogus.method", nil)
	if resp["error"] == nil {
		t.Error("expected error for unknown method")
	}
}
