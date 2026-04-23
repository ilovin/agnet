package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentd/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	tmp := t.TempDir()
	cfg, err := config.Load(filepath.Join(tmp, "config.json"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Port != 7373 {
		t.Errorf("expected port 7373, got %d", cfg.Port)
	}
	if cfg.Token == "" {
		t.Error("expected non-empty default token")
	}
	if cfg.DataDir == "" {
		t.Error("expected non-empty data dir")
	}
}

func TestLoadFromFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	content := `{"port": 9999, "token": "mytoken", "data_dir": "/tmp/agentd-data"}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("expected port 9999, got %d", cfg.Port)
	}
	if cfg.Token != "mytoken" {
		t.Errorf("expected token 'mytoken', got %q", cfg.Token)
	}
}

func TestLoadEmptyTokenGeneratesToken(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	content := `{"port": 8080}` // no token field
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Token == "" {
		t.Error("expected non-empty token when config has no token field")
	}
}

func TestLoadDefaultsWritesFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	_, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Error("expected config file to be written on first boot")
	}
}
