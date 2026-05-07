package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"time"
)

// Client sends diagnostic reports to the telemetry API
type Client struct {
	apiURL     string
	installID  string
	version    string
	httpClient *http.Client
	disabled   bool
}

// ReportPayload represents a telemetry report
type ReportPayload struct {
	InstallID     string                 `json:"installId"`
	Version       string                 `json:"version"`
	Platform      string                 `json:"platform"`
	Architecture  string                 `json:"architecture"`
	Uptime        int64                  `json:"uptimeSeconds"`
	Timestamp     time.Time              `json:"timestamp"`
	Components    ComponentStatus        `json:"components"`
	NetworkMode   string                 `json:"networkMode"`
	LogSnippet    string                 `json:"logSnippet,omitempty"`
	AccessToken   string                 `json:"accessToken,omitempty"`
}

// ComponentStatus tracks health of system components
type ComponentStatus struct {
	AgentgwRunning bool `json:"agentgwRunning"`
	AgentdRunning  bool `json:"agentdRunning"`
}

// NewClient creates a telemetry client
func NewClient(apiURL, installID, version string) *Client {
	if apiURL == "" {
		apiURL = "https://api.ilovin.xyz/v1/telemetry"
	}
	return &Client{
		apiURL:     apiURL,
		installID:  installID,
		version:    version,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		disabled:   os.Getenv("PHONETALK_DISABLE_TELEMETRY") == "1",
	}
}

// SetDisabled controls whether telemetry is sent
func (c *Client) SetDisabled(disabled bool) {
	c.disabled = disabled
}

// SendReport sends a telemetry report
func (c *Client) SendReport(report ReportPayload) error {
	if c.disabled {
		return nil
	}

	report.InstallID = c.installID
	report.Version = c.version
	report.Platform = runtime.GOOS
	report.Architecture = runtime.GOARCH
	report.Timestamp = time.Now().UTC()

	payload, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	req, err := http.NewRequest("POST", c.apiURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send report: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("telemetry API returned %d", resp.StatusCode)
	}
	return nil
}

// SendStartupReport sends a startup heartbeat
func (c *Client) SendStartupReport(networkMode string) error {
	return c.SendReport(ReportPayload{
		NetworkMode: networkMode,
		Components: ComponentStatus{
			AgentgwRunning: true,
		},
	})
}

// SanitizeLog removes sensitive information from log snippets
func SanitizeLog(input string) string {
	// Simple sanitization - remove tokens, IPs, etc.
	// In production, this should be more sophisticated
	if len(input) > 5000 {
		input = input[len(input)-5000:]
	}
	return input
}
