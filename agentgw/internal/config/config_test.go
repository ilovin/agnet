package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentgw/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	tmp := t.TempDir()
	cfg, err := config.Load(filepath.Join(tmp, "config.json"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Port != 7374 {
		t.Errorf("expected port 7374, got %d", cfg.Port)
	}
	if cfg.Token == "" {
		t.Error("expected non-empty default token")
	}
}

func TestLoadFromFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	content := `{"port": 8080, "token": "mytoken"}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected 8080, got %d", cfg.Port)
	}
	if cfg.Token != "mytoken" {
		t.Errorf("expected mytoken, got %q", cfg.Token)
	}
}

func TestLoadNodesFromConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	content := `{"port": 8080, "token": "mytoken", "nodes": [{"id": "n1", "name": "remote1", "host": "192.168.1.10", "ssh_port": 22, "agentd_port": 7373}]}`
	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(cfg.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(cfg.Nodes))
	}
	if cfg.Nodes[0].Name != "remote1" {
		t.Errorf("expected remote1, got %q", cfg.Nodes[0].Name)
	}
}
