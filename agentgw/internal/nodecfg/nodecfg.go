package nodecfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// NodeEntry is a persisted node configuration entry.
type NodeEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Host       string `json:"host"`
	SSHPort    int    `json:"ssh_port"`
	AgentdPort int    `json:"agentd_port"`
	SSHKeyPath string `json:"ssh_key_path"`
	Token      string `json:"token"`     // agentd bearer token
	SSHAlias   string `json:"ssh_alias"` // SSH config alias (e.g. "ws"), takes precedence over Host
}

// Store persists NodeEntry list to a JSON file.
// Supports two file layouts:
//   - Legacy: direct JSON array (e.g. nodes.json)
//   - Unified: a mapping with a "nodes" key (e.g. config.json)
type Store struct {
	path string
}

func New(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Load() ([]NodeEntry, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return []NodeEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	// Try unified format first: { "nodes": [...] }
	var wrapper struct {
		Nodes []NodeEntry `json:"nodes"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil {
		if wrapper.Nodes != nil {
			return wrapper.Nodes, nil
		}
		// wrapper.Nodes is nil — could be config.json without nodes key, or empty file.
		// Try legacy format (direct array) as fallback.
		var entries []NodeEntry
		if err2 := json.Unmarshal(data, &entries); err2 == nil {
			return entries, nil
		}
		return []NodeEntry{}, nil
	}

	// Unified format failed, try legacy format.
	var entries []NodeEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse nodes: %w", err)
	}
	if entries == nil {
		entries = []NodeEntry{}
	}
	return entries, nil
}

func (s *Store) Save(entries []NodeEntry) error {
	raw, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read file: %w", err)
	}

	var doc map[string]json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &doc); err != nil {
			// Legacy format (direct array) or corrupted — start fresh with mapping.
			doc = make(map[string]json.RawMessage)
		}
	} else {
		doc = make(map[string]json.RawMessage)
	}

	// Encode entries as a json.RawMessage.
	encoded, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("marshal entries: %w", err)
	}
	doc["nodes"] = encoded

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(s.path, out, 0600)
}
