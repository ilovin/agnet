package upgrade

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
)

type Asset struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type AgentAppAssets struct {
	APK *Asset `json:"apk"`
	IPA *Asset `json:"ipa"`
}

type Manifest struct {
	Package    string               `json:"package"`
	Version    string               `json:"version"`
	BuildDate  string               `json:"buildDate"`
	Components ManifestComponents   `json:"components"`
}

type ManifestComponents struct {
	AgentGW  []Asset        `json:"agentgw"`
	AgentD   []Asset        `json:"agentd"`
	AgentApp AgentAppAssets `json:"agentapp"`
}

type Client struct {
	ManifestURL string
	HTTPClient  *http.Client
}

func NewClient(manifestURL string) *Client {
	return &Client{ManifestURL: manifestURL, HTTPClient: http.DefaultClient}
}

func (c *Client) FetchManifest() (*Manifest, error) {
	if c.ManifestURL == "" {
		return nil, fmt.Errorf("manifest URL is empty")
	}
	resp, err := c.HTTPClient.Get(c.ManifestURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch manifest: http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	return &manifest, nil
}

func (m *Manifest) FindAsset(component string, osName string, arch string) *Asset {
	var assets []Asset
	switch component {
	case "agentgw":
		assets = m.Components.AgentGW
	case "agentd":
		assets = m.Components.AgentD
	default:
		return nil
	}
	for i := range assets {
		if assets[i].OS == osName && assets[i].Arch == arch {
			return &assets[i]
		}
	}
	return nil
}

func LocalRuntime() (string, string) {
	return runtime.GOOS, runtime.GOARCH
}
