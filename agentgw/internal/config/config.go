package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/phone-talk/agentgw/internal/nodecfg"
)

type TunnelConfig struct {
	HubURL     string `json:"hub_url"`
	AppURL     string `json:"app_url"`
	RealitySNI string `json:"reality_sni"`
}

type UpgradeConfig struct {
	ManifestURL string `json:"manifest_url"`
}

type Config struct {
	Port      int                 `json:"port"`
	Token     string              `json:"token"` // local agentgw /ws auth token
	NodesFile string              `json:"nodes_file"`
	SSHKey    string              `json:"ssh_key"` // path to default SSH private key
	Tunnel    TunnelConfig        `json:"tunnel"`
	Upgrade   UpgradeConfig       `json:"upgrade"`
	Nodes     []nodecfg.NodeEntry `json:"nodes"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Port: 7374,
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		cfg.Token = randomToken()
		if err2 := os.MkdirAll(filepath.Dir(path), 0700); err2 != nil {
			return nil, fmt.Errorf("mkdir config dir: %w", err2)
		}
		out, err2 := json.MarshalIndent(cfg, "", "  ")
		if err2 != nil {
			return nil, fmt.Errorf("marshal default config: %w", err2)
		}
		if err2 := os.WriteFile(path, out, 0600); err2 != nil {
			return nil, fmt.Errorf("write default config: %w", err2)
		}
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Token == "" {
		cfg.Token = randomToken()
	}
	return cfg, nil
}

func randomToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
