package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentgw/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	tmp := t.TempDir()
	cfg, err := config.Load(filepath.Join(tmp, "config.yaml"))
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Port != 7374 {
		t.Errorf("expected port 7374, got %d", cfg.Port)
	}
	if cfg.Token == "" {
		t.Error("expected non-empty default token")
	}
	if cfg.NodesFile == "" {
		t.Error("expected non-empty nodes_file")
	}
}

func TestLoadFromFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.yaml")
	content := "port: 8080\ntoken: \"mytoken\"\nnodes_file: \"/tmp/nodes.yaml\"\n"
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
	if cfg.NodesFile != "/tmp/nodes.yaml" {
		t.Errorf("expected /tmp/nodes.yaml, got %q", cfg.NodesFile)
	}
}
