package telemetry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewClient_DefaultURL(t *testing.T) {
	c := NewClient("", "test-install", "v0.1.0")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.apiURL != "https://api.ilovin.xyz/v1/telemetry" {
		t.Fatalf("expected default API URL, got %q", c.apiURL)
	}
	if c.installID != "test-install" {
		t.Fatalf("expected installID test-install, got %q", c.installID)
	}
	if c.version != "v0.1.0" {
		t.Fatalf("expected version v0.1.0, got %q", c.version)
	}
	if c.disabled {
		t.Fatal("expected client to be enabled by default")
	}
}

func TestNewClient_CustomURL(t *testing.T) {
	c := NewClient("https://custom.example.com/telemetry", "install-123", "v1.0.0")
	if c.apiURL != "https://custom.example.com/telemetry" {
		t.Fatalf("expected custom URL, got %q", c.apiURL)
	}
}

func TestNewClient_DisabledByEnv(t *testing.T) {
	os.Setenv("PHONETALK_DISABLE_TELEMETRY", "1")
	defer os.Unsetenv("PHONETALK_DISABLE_TELEMETRY")

	c := NewClient("", "test", "v0.1.0")
	if !c.disabled {
		t.Fatal("expected client to be disabled when env is set")
	}
}

func TestSetDisabled(t *testing.T) {
	c := NewClient("", "test", "v0.1.0")
	if c.disabled {
		t.Fatal("expected initially enabled")
	}

	c.SetDisabled(true)
	if !c.disabled {
		t.Fatal("expected disabled after SetDisabled(true)")
	}

	c.SetDisabled(false)
	if c.disabled {
		t.Fatal("expected enabled after SetDisabled(false)")
	}
}

func TestSendReport_Disabled(t *testing.T) {
	c := NewClient("", "test", "v0.1.0")
	c.SetDisabled(true)

	err := c.SendReport(ReportPayload{})
	if err != nil {
		t.Fatalf("expected no error when disabled, got %v", err)
	}
}

func TestSendReport_Success(t *testing.T) {
	var receivedBody []byte
	var receivedContentType string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	c := NewClient(server.URL, "install-123", "v0.1.0")
	c.httpClient = &http.Client{Timeout: 5 * time.Second}

	report := ReportPayload{
		NetworkMode: "tunnel",
		Components: ComponentStatus{
			AgentgwRunning: true,
			AgentdRunning:  false,
		},
	}

	err := c.SendReport(report)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if receivedContentType != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", receivedContentType)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("failed to decode received payload: %v", err)
	}

	if payload["installId"] != "install-123" {
		t.Fatalf("expected installId install-123, got %v", payload["installId"])
	}

	if payload["version"] != "v0.1.0" {
		t.Fatalf("expected version v0.1.0, got %v", payload["version"])
	}

	if payload["networkMode"] != "tunnel" {
		t.Fatalf("expected networkMode tunnel, got %v", payload["networkMode"])
	}

	platform, ok := payload["platform"].(string)
	if !ok || platform == "" {
		t.Fatalf("expected non-empty platform, got %v", payload["platform"])
	}

	arch, ok := payload["architecture"].(string)
	if !ok || arch == "" {
		t.Fatalf("expected non-empty architecture, got %v", payload["architecture"])
	}

	ts, ok := payload["timestamp"].(string)
	if !ok || ts == "" {
		t.Fatalf("expected non-empty timestamp, got %v", payload["timestamp"])
	}
}

func TestSendReport_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "test", "v0.1.0")
	c.httpClient = &http.Client{Timeout: 5 * time.Second}

	err := c.SendReport(ReportPayload{})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected error to contain 500, got %v", err)
	}
}

func TestSendReport_NetworkError(t *testing.T) {
	c := NewClient("http://localhost:1", "test", "v0.1.0")
	c.httpClient = &http.Client{Timeout: 1 * time.Second}

	err := c.SendReport(ReportPayload{})
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestSendStartupReport(t *testing.T) {
	var receivedPayload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-install", "v0.1.0")
	c.httpClient = &http.Client{Timeout: 5 * time.Second}

	err := c.SendStartupReport("tunnel")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if receivedPayload["networkMode"] != "tunnel" {
		t.Fatalf("expected networkMode tunnel, got %v", receivedPayload["networkMode"])
	}

	components, ok := receivedPayload["components"].(map[string]interface{})
	if !ok {
		t.Fatal("expected components to be an object")
	}

	if components["agentgwRunning"] != true {
		t.Fatalf("expected agentgwRunning true, got %v", components["agentgwRunning"])
	}
}

func TestSanitizeLog(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "short log unchanged",
			input:    "simple log line",
			expected: "simple log line",
		},
		{
			name:     "exactly 5000 chars",
			input:    strings.Repeat("a", 5000),
			expected: strings.Repeat("a", 5000),
		},
		{
			name:     "over 5000 truncated",
			input:    strings.Repeat("a", 6000),
			expected: strings.Repeat("a", 5000),
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeLog(tt.input)
			if result != tt.expected {
				t.Fatalf("expected length %d, got %d", len(tt.expected), len(result))
			}
		})
	}
}

func TestSanitizeLog_TruncatesCorrectly(t *testing.T) {
	input := strings.Repeat("x", 5000) + "tail"
	result := SanitizeLog(input)

	if len(result) != 5000 {
		t.Fatalf("expected length 5000 after truncation, got %d", len(result))
	}

	if !strings.HasSuffix(result, "tail") {
		t.Fatal("expected truncated string to keep the tail end")
	}
}

func TestReportPayload_JSONSerialization(t *testing.T) {
	report := ReportPayload{
		InstallID:    "install-123",
		Version:      "v0.1.0",
		Platform:     "darwin",
		Architecture: "arm64",
		Uptime:       3600,
		Timestamp:    time.Date(2026, 5, 8, 7, 0, 0, 0, time.UTC),
		Components: ComponentStatus{
			AgentgwRunning: true,
			AgentdRunning:  true,
		},
		NetworkMode: "tunnel",
		LogSnippet:  "test log",
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded ReportPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.InstallID != report.InstallID {
		t.Fatalf("installId mismatch")
	}
	if decoded.Version != report.Version {
		t.Fatalf("version mismatch")
	}
	if decoded.NetworkMode != report.NetworkMode {
		t.Fatalf("networkMode mismatch")
	}
	if decoded.Uptime != report.Uptime {
		t.Fatalf("uptime mismatch")
	}
	if !decoded.Components.AgentgwRunning {
		t.Fatal("agentgwRunning should be true")
	}
	if !decoded.Components.AgentdRunning {
		t.Fatal("agentdRunning should be true")
	}
}

func TestComponentStatus_JSON(t *testing.T) {
	cs := ComponentStatus{
		AgentgwRunning: true,
		AgentdRunning:  false,
	}

	data, err := json.Marshal(cs)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	expected := `{"agentgwRunning":true,"agentdRunning":false}`
	if string(data) != expected {
		t.Fatalf("expected %s, got %s", expected, string(data))
	}
}
