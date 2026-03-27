package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Port    int    `yaml:"port"`
	Token   string `yaml:"token"`
	DataDir string `yaml:"data_dir"`
}

// Load reads config from path. If the file doesn't exist, returns defaults and
// writes the defaults to path so the user can edit it.
func Load(path string) (*Config, error) {
	cfg := &Config{
		Port:    7373,
		DataDir: filepath.Join(os.Getenv("HOME"), ".agentd", "data"),
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg.Token = randomToken()
		if err2 := os.MkdirAll(filepath.Dir(path), 0700); err2 != nil {
			return nil, fmt.Errorf("mkdir config dir: %w", err2)
		}
		out, _ := yaml.Marshal(cfg)
		_ = os.WriteFile(path, out, 0600)
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
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
