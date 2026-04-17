package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Port      int    `yaml:"port"`
	Token     string `yaml:"token"` // local agentgw /ws auth token
	NodesFile string `yaml:"nodes_file"`
	SSHKey    string `yaml:"ssh_key"` // path to default SSH private key
}

func Load(path string) (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	cfg := &Config{
		Port:      7374,
		NodesFile: filepath.Join(home, ".agentgw", "nodes.yaml"),
		SSHKey:    filepath.Join(home, ".ssh", "id_rsa"),
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		cfg.Token = randomToken()
		if err2 := os.MkdirAll(filepath.Dir(path), 0700); err2 != nil {
			return nil, fmt.Errorf("mkdir config dir: %w", err2)
		}
		out, err2 := yaml.Marshal(cfg)
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
	if err := yaml.Unmarshal(data, cfg); err != nil {
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
