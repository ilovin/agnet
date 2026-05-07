package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rr := httptest.NewRecorder()

	healthHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", resp["status"])
	}

	if resp["version"] != version {
		t.Fatalf("expected version %q, got %v", version, resp["version"])
	}

	ts, ok := resp["timestamp"].(string)
	if !ok || ts == "" {
		t.Fatalf("expected non-empty timestamp, got %v", resp["timestamp"])
	}

	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}
}

func TestReleaseHandler(t *testing.T) {
	tmpDir := t.TempDir()
	manifestFile := filepath.Join(tmpDir, "manifest.json")
	manifestData := map[string]interface{}{
		"schemaVersion": 1,
		"version":       "v0.1.0",
		"package":       "phone-talk",
	}
	data, _ := json.Marshal(manifestData)
	os.WriteFile(manifestFile, data, 0644)

	oldManifestPath := manifestPath
	manifestPath = manifestFile
	defer func() { manifestPath = oldManifestPath }()

	req := httptest.NewRequest(http.MethodGet, "/v1/release/latest", nil)
	rr := httptest.NewRecorder()

	releaseHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["version"] != "v0.1.0" {
		t.Fatalf("expected version v0.1.0, got %v", resp["version"])
	}

	if acao := rr.Header().Get("Access-Control-Allow-Origin"); acao != "*" {
		t.Fatalf("expected CORS header *, got %q", acao)
	}
}

func TestReleaseHandler_NotFound(t *testing.T) {
	oldManifestPath := manifestPath
	manifestPath = "/nonexistent/path/manifest.json"
	defer func() { manifestPath = oldManifestPath }()

	req := httptest.NewRequest(http.MethodGet, "/v1/release/latest", nil)
	rr := httptest.NewRecorder()

	releaseHandler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rr.Code)
	}
}

func TestInstallScriptHandler(t *testing.T) {
	tmpDir := t.TempDir()
	scriptsDir := filepath.Join(tmpDir, "scripts")
	os.MkdirAll(scriptsDir, 0755)
	scriptFile := filepath.Join(scriptsDir, "install.sh")
	scriptContent := "#!/bin/bash\necho 'test install script'"
	os.WriteFile(scriptFile, []byte(scriptContent), 0755)

	req := httptest.NewRequest(http.MethodGet, "/v1/install.sh", nil)
	rr := httptest.NewRecorder()

	oldCwd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldCwd)

	installScriptHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "test install script") {
		t.Fatalf("expected install script content, got %q", body)
	}

	if ct := rr.Header().Get("Content-Type"); ct != "text/x-shellscript" {
		t.Fatalf("expected Content-Type text/x-shellscript, got %q", ct)
	}
}

func TestTelemetryHandler(t *testing.T) {
	payload := map[string]interface{}{
		"installId": "test-install-123",
		"version":   "v0.1.0",
		"platform":  "darwin-arm64",
	}
	data, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry", bytes.NewReader(data))
	rr := httptest.NewRecorder()

	telemetryHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp["ok"])
	}
}

func TestTelemetryHandler_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/telemetry", nil)
	rr := httptest.NewRecorder()

	telemetryHandler(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status %d, got %d", http.StatusMethodNotAllowed, rr.Code)
	}
}

func TestTelemetryHandler_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry", strings.NewReader("not json"))
	rr := httptest.NewRecorder()

	telemetryHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
}

func TestHealthHandler_TimestampFormat(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	rr := httptest.NewRecorder()

	healthHandler(rr, req)

	var resp map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &resp)

	ts, ok := resp["timestamp"].(string)
	if !ok {
		t.Fatal("timestamp not a string")
	}

	_, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("timestamp not valid RFC3339: %v", err)
	}
}
