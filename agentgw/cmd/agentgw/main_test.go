package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBuildRemoteQRURLDropsPortForDefaultHTTPS(t *testing.T) {
	got := buildRemoteQRURL("https://ilovin.xyz:8443", "", "fengming.xie")
	want := "wss://ilovin.xyz/ws/fengming.xie"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestBuildRemoteQRURLUsesAppURLHost(t *testing.T) {
	got := buildRemoteQRURL("https://hub.internal:9443", "https://ilovin.xyz:443", "u1")
	want := "wss://ilovin.xyz/ws/u1"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestBuildRemoteQRURLInvalidURLReturnsEmpty(t *testing.T) {
	if got := buildRemoteQRURL("https://ilovin.xyz:8443", "://bad-url", "u1"); got != "" {
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
