package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBuildRemoteQRURLDropsPortForDefaultHTTPS(t *testing.T) {
	got := buildRemoteQRURL("https://example.com:8443", "", "fengming.xie")
	want := "wss://example.com/ws/fengming.xie"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestBuildRemoteQRURLUsesAppURLHost(t *testing.T) {
	got := buildRemoteQRURL("https://hub.internal:9443", "https://example.com:443", "u1")
	want := "wss://example.com/ws/u1"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestBuildRemoteQRURLInvalidURLReturnsEmpty(t *testing.T) {
	if got := buildRemoteQRURL("https://example.com:8443", "://bad-url", "u1"); got != "" {
		t.Fatalf("expected empty URL, got %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	if got := formatDuration(5 * time.Second); got != "5s" {
		t.Fatalf("expected 5s, got %q", got)
	}
	if got := formatDuration(65 * time.Second); got != "1m5s" {
		t.Fatalf("expected 1m5s, got %q", got)
	}
	if got := formatDuration(3661 * time.Second); got != "1h1m1s" {
		t.Fatalf("expected 1h1m1s, got %q", got)
	}
}

func TestQRLocalEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	registerQRHandler(mux, 7374, "testtoken123", "", "", "", "")

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/qr.png?type=local")
	if err != nil {
		t.Fatalf("GET /qr.png?type=local: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected Content-Type image/png, got %q", got)
	}

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected CORS header *, got %q", got)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) == 0 {
		t.Fatalf("expected non-empty PNG body")
	}

	// Verify PNG magic bytes
	if len(body) < 8 || !bytes.Equal(body[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
		t.Fatalf("expected PNG magic bytes, got % x", body[:min(len(body), 8)])
	}
}

func TestQRLocalEndpointCORSOptions(t *testing.T) {
	mux := http.NewServeMux()
	registerQRHandler(mux, 7374, "tok", "", "", "", "")

	server := httptest.NewServer(mux)
	defer server.Close()

	req, err := http.NewRequest(http.MethodOptions, server.URL+"/qr.png?type=local", nil)
	if err != nil {
		t.Fatalf("create OPTIONS request: %v", err)
	}
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /qr.png: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("expected CORS header *, got %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatalf("expected Access-Control-Allow-Methods header")
	}
}

func TestQRRemoteEndpointWithTunnel(t *testing.T) {
	mux := http.NewServeMux()
	registerQRHandler(mux, 7374, "testtoken123", "https://hub.example.com", "tunneltoken", "https://app.example.com", "user1")

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/qr.png?type=remote")
	if err != nil {
		t.Fatalf("GET /qr.png?type=remote: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Fatalf("expected Content-Type image/png, got %q", got)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) == 0 {
		t.Fatalf("expected non-empty PNG body")
	}
}

func TestQRRemoteEndpointWithoutTunnel(t *testing.T) {
	mux := http.NewServeMux()
	registerQRHandler(mux, 7374, "testtoken123", "", "", "", "")

	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/qr.png?type=remote")
	if err != nil {
		t.Fatalf("GET /qr.png?type=remote: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", resp.StatusCode)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestOnEventCallbackSanitizesBeforeBroadcast guards a regression: the
// gateway main loop must call ws.SanitizeEventInPlace inside its
// mgr.OnEvent callback, so TUI box-drawing / control glyphs are
// stripped before the event reaches Flutter clients (which would
// render them as tofu).
//
// We verify this at the source level rather than via integration
// because wiring up a full proxy + node + WS roundtrip just to assert
// one function call would dwarf the unit tests in `internal/ws`.
func TestOnEventCallbackSanitizesBeforeBroadcast(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	hay := string(src)
	// Locate the OnEvent registration.
	idx := strings.Index(hay, "mgr.OnEvent(func(nodeID string, ev map[string]any)")
	if idx < 0 {
		t.Fatalf("expected OnEvent callback wiring in main.go")
	}
	// Slice the function body (closing of the closure is within ~1KB).
	end := idx + 1024
	if end > len(hay) {
		end = len(hay)
	}
	body := hay[idx:end]

	if !strings.Contains(body, "ws.SanitizeEventInPlace(ev)") {
		t.Fatalf("OnEvent callback in main.go must call ws.SanitizeEventInPlace(ev) before broadcasting; got:\n%s", body)
	}
	// And it must be invoked BEFORE the Broadcast call, otherwise the
	// untouched event is what reaches the client.
	sanitizeAt := strings.Index(body, "ws.SanitizeEventInPlace(ev)")
	broadcastAt := strings.Index(body, "srv.Broadcast(")
	if sanitizeAt < 0 || broadcastAt < 0 || sanitizeAt > broadcastAt {
		t.Fatalf("ws.SanitizeEventInPlace must be called before srv.Broadcast (sanitize@%d, broadcast@%d)", sanitizeAt, broadcastAt)
	}
}

func TestShowStatusPrintsTunnelTelemetry(t *testing.T) {
	origCfgPath := configLoadPath
	origHTTPGet := statusHTTPGet
	origStdout := os.Stdout
	origStderr := os.Stderr
	defer func() {
		configLoadPath = origCfgPath
		statusHTTPGet = origHTTPGet
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()

	tmp := t.TempDir()
	cfgPath := tmp + "/config.json"
	cfgJSON := `{"port":7374,"token":"tok","nodes":[]}` + "\n"
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	configLoadPath = cfgPath

	respBody := map[string]any{
		"version":       "v0.1.0",
		"uptimeSeconds": 15,
		"port":          7374,
		"clientCount":   1,
		"nodeCount":     0,
		"statusCounts":  map[string]any{"connected": 0},
		"nodes":         []any{},
		"tunnelUrl":     "https://hub.example",
		"tunnelStatus": map[string]any{
			"connected":               true,
			"connectedAt":             "2026-04-24T10:00:00Z",
			"lastHandshakeDurationMs": 321,
			"lastHandshakeAt":         "2026-04-24T10:00:00Z",
			"lastCommunicationAt":     "2026-04-24T10:00:03Z",
			"lastDisconnectedAt":      "0001-01-01T00:00:00Z",
			"lastError":               "",
		},
	}
	payload, err := json.Marshal(respBody)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	statusHTTPGet = func(url, token string) ([]byte, int, error) {
		return payload, 200, nil
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	showStatus()
	_ = w.Close()
	out := <-done

	checks := []string{
		"tunnel:   https://hub.example",
		"state:    connected",
		"handshake:321ms",
		"last comm:2026-04-24T10:00:03Z",
		"connected:2026-04-24T10:00:00Z",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}
