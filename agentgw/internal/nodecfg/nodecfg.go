package nodecfg

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// NodeEntry is a persisted node configuration entry.
type NodeEntry struct {
	ID         string `yaml:"id"`
	Name       string `yaml:"name"`
	Host       string `yaml:"host"`
	SSHPort    int    `yaml:"ssh_port"`
	AgentdPort int    `yaml:"agentd_port"`
	SSHKeyPath string `yaml:"ssh_key_path"`
	Token      string `yaml:"token"` // agentd bearer token
}

// Store persists NodeEntry list to a YAML file.
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
		return nil, fmt.Errorf("read nodes file: %w", err)
	}
	var entries []NodeEntry
	if err := yaml.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse nodes file: %w", err)
	}
	if entries == nil {
		entries = []NodeEntry{}
	}
	return entries, nil
}

func (s *Store) Save(entries []NodeEntry) error {
	data, err := yaml.Marshal(entries)
	if err != nil {
		return fmt.Errorf("marshal nodes: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("write nodes file: %w", err)
	}
	return nil
}
