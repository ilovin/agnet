package nodecfg_test

import (
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentgw/internal/nodecfg"
)

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(path)

	entries := []nodecfg.NodeEntry{
		{ID: "n1", Name: "remote1", Host: "192.168.1.10", SSHPort: 22, AgentdPort: 7373, SSHKeyPath: "~/.ssh/id_rsa"},
		{ID: "n2", Name: "remote2", Host: "10.0.0.5", SSHPort: 2222, AgentdPort: 7373, SSHKeyPath: "~/.ssh/id_ed25519"},
	}

	if err := store.Save(entries); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(loaded))
	}
	if loaded[0].ID != "n1" || loaded[0].Host != "192.168.1.10" {
		t.Errorf("unexpected first entry: %+v", loaded[0])
	}
	if loaded[1].SSHPort != 2222 {
		t.Errorf("expected ssh port 2222, got %d", loaded[1].SSHPort)
	}
}

func TestLoadEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(path)

	entries, err := store.Load()
	if err != nil {
		t.Fatalf("Load on missing file failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty, got %d entries", len(entries))
	}
}
