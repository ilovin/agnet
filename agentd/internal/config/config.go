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
)

type Config struct {
	Port    int    `json:"port"`
	Token   string `json:"token"`
	DataDir string `json:"data_dir"`
	NodeID  string `json:"node_id"`
}

// Load reads config from path. If the file doesn't exist, returns defaults and
// writes the defaults to path so the user can edit it.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		cfg := &Config{
			Port:    7373,
			DataDir: filepath.Join(home, ".agentd", "data"),
			Token:   randomToken(),
			NodeID:  "local",
		}
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
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	cfg := &Config{
		Port:    7373,
		DataDir: filepath.Join(home, ".agentd", "data"),
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
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
