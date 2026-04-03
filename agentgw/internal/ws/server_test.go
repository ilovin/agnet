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
	"github.com/phone-talk/agentgw/internal/proxy"
	"github.com/phone-talk/agentgw/internal/ws"
)

type fakeAgentd struct {
	t        *testing.T
	upgrader websocket.Upgrader
	result   map[string]any
}

func newFakeAgentd(t *testing.T, result map[string]any) *httptest.Server {
	t.Helper()
	fa := &fakeAgentd{
		t:        t,
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		result:   result,
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := fa.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		for {
			var req map[string]any
			if err := conn.ReadJSON(&req); err != nil {
				return
			}

			method, _ := req["method"].(string)
			switch method {
			case "session.list":
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  fa.result,
				})
			case "session.catalog":
				managed := []any{}
				attachable := []any{}
				opencodeFiles := []any{}
				if v, ok := fa.result["managed"].([]any); ok {
					managed = v
				}
				if v, ok := fa.result["attachable"].([]any); ok {
					attachable = v
				}
				if v, ok := fa.result["opencodeFiles"].([]any); ok {
					opencodeFiles = v
				}
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result": map[string]any{
						"managed":       managed,
						"attachable":    attachable,
						"opencodeFiles": opencodeFiles,
					},
				})
			case "conversation.key":
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"result":  map[string]any{"ok": true},
				})
			default:
				_ = conn.WriteJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      req["id"],
					"error": map[string]any{
						"code":    -32601,
						"message": "method not found",
					},
				})
			}
		}
	}))

	t.Cleanup(ts.Close)
	return ts
}

func newTestServer(t *testing.T) (*httptest.Server, *node.Manager) {
	t.Helper()
	store := nodecfg.New(filepath.Join(t.TempDir(), "nodes.yaml"))
	mgr := node.NewManager(store, nil)
	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, mgr
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
	ts, _ := newTestServer(t)
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	_, resp, _ := websocket.DefaultDialer.Dial(u, http.Header{"Authorization": {"Bearer wrong"}})
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %v", resp)
	}
}

func TestNodeList(t *testing.T) {
	ts, _ := newTestServer(t)
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
	ts, _ := newTestServer(t)
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

func TestSessionListForwardsToNode(t *testing.T) {
	ts, mgr := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	id, err := mgr.Add(nodecfg.NodeEntry{Name: "n1", Host: "127.0.0.1", SSHPort: 22, AgentdPort: 7373, Token: "tok"})
	if err != nil {
		t.Fatalf("mgr.Add: %v", err)
	}

	agentd := newFakeAgentd(t, map[string]any{
		"processes": []any{map[string]any{"pid": 123.0, "sessionId": "s-1"}},
		"count":     1.0,
	})
	wsURL := "ws" + strings.TrimPrefix(agentd.URL, "http")
	p, err := proxy.New(wsURL, "tok")
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	mgr.Get(id).SetProxy(p)

	resp := rpc(t, conn, "session.list", map[string]any{"nodeId": id})
	if resp["error"] != nil {
		t.Fatalf("session.list error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp["result"])
	}
	processes, ok := result["processes"].([]any)
	if !ok || len(processes) != 1 {
		t.Fatalf("expected one process, got %#v", result["processes"])
	}
}

func TestSessionListAllReturnsItemsAndErrors(t *testing.T) {
	ts, mgr := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	connectedID, err := mgr.Add(nodecfg.NodeEntry{Name: "n1", Host: "127.0.0.1", SSHPort: 22, AgentdPort: 7373, Token: "tok"})
	if err != nil {
		t.Fatalf("mgr.Add connected node: %v", err)
	}
	disconnectedID, err := mgr.Add(nodecfg.NodeEntry{Name: "n2", Host: "127.0.0.2", SSHPort: 22, AgentdPort: 7373, Token: "tok"})
	if err != nil {
		t.Fatalf("mgr.Add disconnected node: %v", err)
	}

	agentd := newFakeAgentd(t, map[string]any{
		"processes": []any{
			map[string]any{"pid": 1001.0, "sessionId": "sess-a"},
			map[string]any{"pid": 1002.0, "sessionId": "sess-b"},
		},
		"count": 2.0,
	})
	wsURL := "ws" + strings.TrimPrefix(agentd.URL, "http")
	p, err := proxy.New(wsURL, "tok")
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	mgr.Get(connectedID).SetProxy(p)

	resp := rpc(t, conn, "session.list_all", nil)
	if resp["error"] != nil {
		t.Fatalf("session.list_all error: %v", resp["error"])
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp["result"])
	}

	items, ok := result["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", result["items"])
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["nodeId"] != connectedID {
			t.Fatalf("expected nodeId %q in all items, got %v", connectedID, item["nodeId"])
		}
	}

	errs, ok := result["errors"].([]any)
	if !ok {
		t.Fatalf("expected errors array, got %T", result["errors"])
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	errEntry, _ := errs[0].(map[string]any)
	if errEntry["nodeId"] != disconnectedID {
		t.Fatalf("expected error nodeId %q, got %v", disconnectedID, errEntry["nodeId"])
	}
}

func TestSessionCatalogAllReturnsGroupedData(t *testing.T) {
	ts, mgr := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	connectedID, err := mgr.Add(nodecfg.NodeEntry{Name: "n1", Host: "127.0.0.1", SSHPort: 22, AgentdPort: 7373, Token: "tok"})
	if err != nil {
		t.Fatalf("mgr.Add connected node: %v", err)
	}
	disconnectedID, err := mgr.Add(nodecfg.NodeEntry{Name: "n2", Host: "127.0.0.2", SSHPort: 22, AgentdPort: 7373, Token: "tok"})
	if err != nil {
		t.Fatalf("mgr.Add disconnected node: %v", err)
	}

	agentd := newFakeAgentd(t, map[string]any{
		"managed": []any{
			map[string]any{"id": "a1", "provider": "claude"},
		},
		"attachable": []any{
			map[string]any{"pid": 1001.0, "provider": "claude", "session": "ses_1"},
		},
		"opencodeFiles": []any{
			map[string]any{"id": "ses_op", "name": "ses_op"},
		},
	})
	wsURL := "ws" + strings.TrimPrefix(agentd.URL, "http")
	p, err := proxy.New(wsURL, "tok")
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	mgr.Get(connectedID).SetProxy(p)

	resp := rpc(t, conn, "session.catalog_all", nil)
	if resp["error"] != nil {
		t.Fatalf("session.catalog_all error: %v", resp["error"])
	}

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp["result"])
	}
	items, ok := result["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected 1 item, got %#v", result["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["nodeId"] != connectedID {
		t.Fatalf("expected nodeId %q, got %v", connectedID, item["nodeId"])
	}
	if managed, ok := item["managed"].([]any); !ok || len(managed) != 1 {
		t.Fatalf("expected managed len 1, got %#v", item["managed"])
	}
	if attachable, ok := item["attachable"].([]any); !ok || len(attachable) != 1 {
		t.Fatalf("expected attachable len 1, got %#v", item["attachable"])
	}
	if files, ok := item["opencodeFiles"].([]any); !ok || len(files) != 1 {
		t.Fatalf("expected opencodeFiles len 1, got %#v", item["opencodeFiles"])
	}

	errs, ok := result["errors"].([]any)
	if !ok || len(errs) != 1 {
		t.Fatalf("expected one error entry, got %#v", result["errors"])
	}
	errEntry, _ := errs[0].(map[string]any)
	if errEntry["nodeId"] != disconnectedID {
		t.Fatalf("expected error nodeId %q, got %v", disconnectedID, errEntry["nodeId"])
	}
}

func TestConversationKeyForwardsToNode(t *testing.T) {
	ts, mgr := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")

	id, err := mgr.Add(nodecfg.NodeEntry{Name: "n1", Host: "127.0.0.1", SSHPort: 22, AgentdPort: 7373, Token: "tok"})
	if err != nil {
		t.Fatalf("mgr.Add: %v", err)
	}

	agentd := newFakeAgentd(t, map[string]any{})
	wsURL := "ws" + strings.TrimPrefix(agentd.URL, "http")
	p, err := proxy.New(wsURL, "tok")
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	mgr.Get(id).SetProxy(p)

	resp := rpc(t, conn, "conversation.key", map[string]any{
		"nodeId":  id,
		"agentId": "a1",
		"key":     "enter",
	})
	if resp["error"] != nil {
		t.Fatalf("conversation.key error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok || result["ok"] != true {
		t.Fatalf("expected ok result, got %#v", resp["result"])
	}
}

func TestUnknownMethod(t *testing.T) {
	ts, _ := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")
	resp := rpc(t, conn, "bogus.method", nil)
	if resp["error"] == nil {
		t.Error("expected error for unknown method")
	}
}
