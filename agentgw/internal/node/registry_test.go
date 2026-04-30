package node

import (
	"testing"

	"github.com/google/uuid"
	"github.com/phone-talk/agentgw/internal/nodecfg"
)

func TestRegistryAddAndGet(t *testing.T) {
	r := NewRegistry()

	entry := nodecfg.NodeEntry{Name: "remote1", Host: "192.168.1.10", SSHPort: 22, AgentdPort: 7373, Token: "tok"}
	id, err := r.Add(entry)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	n := r.Get(id)
	if n == nil {
		t.Fatal("expected non-nil node")
	}
	if n.Name != "remote1" {
		t.Errorf("expected Name=remote1, got %q", n.Name)
	}
	if n.GetStatus() != StatusDisconnected {
		t.Errorf("expected status disconnected, got %s", n.GetStatus())
	}
}

func TestRegistryAddAssignsID(t *testing.T) {
	r := NewRegistry()

	entry := nodecfg.NodeEntry{Name: "n1", Host: "10.0.0.1", Token: "t"}
	id, _ := r.Add(entry)
	if id == "" {
		t.Fatal("expected auto-generated id")
	}

	// Providing explicit ID should preserve it
	r2 := NewRegistry()
	explicit := uuid.New().String()
	id2, _ := r2.Add(nodecfg.NodeEntry{ID: explicit, Name: "n2", Host: "10.0.0.2", Token: "t"})
	if id2 != explicit {
		t.Fatalf("expected explicit id %q, got %q", explicit, id2)
	}
}

func TestRegistryGetMissing(t *testing.T) {
	r := NewRegistry()
	if r.Get("nonexistent") != nil {
		t.Error("expected nil for missing node")
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	r.Add(nodecfg.NodeEntry{Name: "a", Host: "1.1.1.1", Token: "t"})
	r.Add(nodecfg.NodeEntry{Name: "b", Host: "2.2.2.2", Token: "t"})

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(list))
	}
}

func TestRegistryListReturnsActualPointers(t *testing.T) {
	r := NewRegistry()
	id, _ := r.Add(nodecfg.NodeEntry{Name: "a", Host: "1.1.1.1", Token: "t"})

	list := r.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 node, got %d", len(list))
	}

	// List should return the actual node pointer so proxy/tunnel state is visible
	if list[0] != r.Get(id) {
		t.Error("List should return the same node pointer as Get")
	}
}

func TestRegistryRemove(t *testing.T) {
	r := NewRegistry()
	id, _ := r.Add(nodecfg.NodeEntry{Name: "r1", Host: "10.0.0.1", Token: "t"})

	if err := r.Remove(id); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if r.Get(id) != nil {
		t.Error("expected node to be removed")
	}
	if len(r.List()) != 0 {
		t.Error("expected empty list after remove")
	}
}

func TestRegistryRemoveMissing(t *testing.T) {
	r := NewRegistry()
	if err := r.Remove("missing"); err == nil {
		t.Error("expected error removing missing node")
	}
}

func TestRegistryRename(t *testing.T) {
	r := NewRegistry()
	id, _ := r.Add(nodecfg.NodeEntry{Name: "old", Host: "10.0.0.1", Token: "t"})

	if err := r.Rename(id, "new"); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}
	if r.Get(id).Name != "new" {
		t.Errorf("expected name new, got %q", r.Get(id).Name)
	}
}

func TestRegistryRenameMissing(t *testing.T) {
	r := NewRegistry()
	if err := r.Rename("missing", "x"); err == nil {
		t.Error("expected error renaming missing node")
	}
}

func TestRegistryLoadAll(t *testing.T) {
	r := NewRegistry()
	entries := []nodecfg.NodeEntry{
		{ID: "id-1", Name: "n1", Host: "1.1.1.1", Token: "t"},
		{ID: "id-2", Name: "n2", Host: "2.2.2.2", Token: "t"},
	}
	r.LoadAll(entries)

	if len(r.List()) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(r.List()))
	}
	if r.Get("id-1").Name != "n1" {
		t.Error("expected n1")
	}
	if r.Get("id-2").Name != "n2" {
		t.Error("expected n2")
	}
}

func TestRegistryLoadAllGeneratesIDs(t *testing.T) {
	r := NewRegistry()
	entries := []nodecfg.NodeEntry{
		{Name: "n1", Host: "1.1.1.1", Token: "t"},
	}
	r.LoadAll(entries)

	list := r.List()
	if len(list) != 1 {
		t.Fatal("expected 1 node")
	}
	if list[0].ID == "" {
		t.Error("expected auto-generated id for entry without ID")
	}
}

func TestRegistryToEntries(t *testing.T) {
	r := NewRegistry()
	r.Add(nodecfg.NodeEntry{Name: "n1", Host: "1.1.1.1", SSHPort: 22, AgentdPort: 7373, Token: "t", SSHKeyPath: "/key"})

	entries := r.ToEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "n1" {
		t.Errorf("expected Name=n1, got %q", entries[0].Name)
	}
	if entries[0].SSHPort != 22 {
		t.Errorf("expected SSHPort=22, got %d", entries[0].SSHPort)
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	done := make(chan struct{})

	go func() {
		for i := 0; i < 100; i++ {
			r.Add(nodecfg.NodeEntry{Name: "a", Host: "1.1.1.1", Token: "t"})
		}
		close(done)
	}()

	for i := 0; i < 100; i++ {
		r.List()
	}
	<-done

	if len(r.List()) != 100 {
		t.Fatalf("expected 100 nodes, got %d", len(r.List()))
	}
}
