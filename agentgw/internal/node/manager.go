package node

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/phone-talk/agentgw/internal/deployer"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	gossh "golang.org/x/crypto/ssh"
)

// Manager tracks all configured nodes and their runtime state.
// It is a thin coordinator that delegates to Registry, TunnelManager, and ProxyManager.
type Manager struct {
	mu        sync.RWMutex
	store     *nodecfg.Store
	agentdBin []byte
	restartFn func(*Node, string) error

	registry  *Registry
	tunnelMgr *TunnelManager
	proxyMgr  *ProxyManager
}

// NewManager creates a Manager backed by the given store.
// agentdBin is the embedded agentd binary (may be nil in tests).
func NewManager(store *nodecfg.Store, agentdBin []byte) *Manager {
	m := &Manager{
		store:     store,
		agentdBin: agentdBin,
		registry:  NewRegistry(),
		tunnelMgr: NewTunnelManager(),
		proxyMgr:  NewProxyManager(),
	}
	m.proxyMgr.OnDisconnect(func(nodeID string) {
		m.handleProxyDisconnect(nodeID)
	})
	return m
}

// OnEvent registers a callback for agentd push events (nodeId is injected into params).
func (m *Manager) OnEvent(cb EventCallback) {
	m.proxyMgr.OnEvent(cb)
}

// SetRestartFunc overrides the restart implementation for tests.
func (m *Manager) SetRestartFunc(fn func(*Node, string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restartFn = fn
}

// SetAgentdBinary replaces the embedded agentd binary used by Deploy.
func (m *Manager) SetAgentdBinary(binary []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentdBin = binary
}

// LoadAll populates nodes from persisted config without writing back to disk.
func (m *Manager) LoadAll(entries []nodecfg.NodeEntry) {
	m.registry.LoadAll(entries)
}

// Add creates a new node entry (not yet connected) and persists it.
func (m *Manager) Add(entry nodecfg.NodeEntry) (string, error) {
	id, err := m.registry.Add(entry)
	if err != nil {
		return "", err
	}
	entries := m.registry.ToEntries()
	if err := m.store.Save(entries); err != nil {
		return id, fmt.Errorf("save nodes: %w", err)
	}
	return id, nil
}

// Connect establishes an SSH tunnel + WS proxy to a node's agentd.
// For localhost/127.0.0.1, it connects directly without SSH tunnel.
func (m *Manager) Connect(id string) error {
	n := m.Get(id)
	if n == nil {
		return fmt.Errorf("node %q not found", id)
	}

	// Clean up existing connections
	m.proxyMgr.Disconnect(n)
	m.tunnelMgr.Disconnect(n)

	n.SetStatus(StatusConnecting)

	wsURL, err := m.tunnelMgr.Connect(n)
	if err != nil {
		return err
	}

	if err := m.proxyMgr.Connect(n, wsURL); err != nil {
		m.tunnelMgr.Disconnect(n)
		return err
	}

	return nil
}

// ConnectAll attempts to connect all disconnected nodes in parallel.
// Retries up to 3 times with backoff for each node.
func (m *Manager) ConnectAll() {
	nodes := m.List()
	for _, n := range nodes {
		if n.GetStatus() == StatusDisconnected || n.GetStatus() == StatusError {
			go func(id string) {
				for attempt := 1; attempt <= 3; attempt++ {
					if err := m.Connect(id); err != nil {
						log.Printf("connect node %s (attempt %d/3): %v", id, attempt, err)
						if attempt < 3 {
							time.Sleep(time.Duration(attempt*3) * time.Second)
						}
						continue
					}
					return
				}
				log.Printf("connect node %s: failed after 3 attempts", id)
			}(n.ID)
		}
	}
}

// StartHealthCheck launches a background loop that retries connection to
// error/disconnected nodes every 30 seconds.
func (m *Manager) StartHealthCheck() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			for _, n := range m.List() {
				st := n.GetStatus()
				if st == StatusError || st == StatusDisconnected {
					go func(id, name string) {
						log.Printf("[health] retrying node %q (%s)", name, id[:8])
						if err := m.Connect(id); err != nil {
							log.Printf("[health] node %q still unreachable: %v", name, err)
						}
					}(n.ID, n.Name)
				}
			}
		}
	}()
}

// Deploy uploads the agentd binary to the remote node and starts it.
// It requires the node to have SSH credentials configured.
func (m *Manager) Deploy(id string, remoteDir string) error {
	n := m.Get(id)
	if n == nil {
		return fmt.Errorf("node %q not found", id)
	}
	if !n.IsLocal() {
		n.SetStatus(StatusDeploying)
	}

	m.mu.RLock()
	agentdBin := m.agentdBin
	m.mu.RUnlock()

	if len(agentdBin) == 0 {
		n.SetStatus(StatusError)
		return fmt.Errorf("no agentd binary embedded")
	}

	// Determine SSH user and key path
	sshUser := os.Getenv("USER")
	if sshUser == "" {
		sshUser = "root"
	}
	keyPath := resolveSSHKeyPath(n.SSHKeyPath)

	// Read SSH private key
	key, err := os.ReadFile(keyPath)
	if err != nil {
		n.SetStatus(StatusError)
		return fmt.Errorf("read ssh key: %w", err)
	}

	signer, err := gossh.ParsePrivateKey(key)
	if err != nil {
		n.SetStatus(StatusError)
		return fmt.Errorf("parse ssh key: %w", err)
	}

	// Create SSH client config
	config := &gossh.ClientConfig{
		User: sshUser,
		Auth: []gossh.AuthMethod{
			gossh.PublicKeys(signer),
		},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	// Connect to SSH server
	sshHost := fmt.Sprintf("%s:%d", n.Host, n.SSHPort)
	client, err := gossh.Dial("tcp", sshHost, config)
	if err != nil {
		n.SetStatus(StatusError)
		return fmt.Errorf("ssh connect: %w", err)
	}
	defer client.Close()

	// Create deployer and execute deployment
	d := deployer.New(client)
	if err := d.DeployWithToken(remoteDir, agentdBin, n.Token); err != nil {
		n.SetStatus(StatusError)
		return fmt.Errorf("deploy: %w", err)
	}

	return nil
}

// Restart restarts agentd on a remote node and reconnects the gateway proxy.
func (m *Manager) Restart(id string, remoteDir string) error {
	n := m.Get(id)
	if n == nil {
		return fmt.Errorf("node %q not found", id)
	}
	if !n.IsLocal() {
		n.SetStatus(StatusDeploying)
	}

	m.proxyMgr.Disconnect(n)
	m.tunnelMgr.Disconnect(n)

	m.mu.RLock()
	restartFn := m.restartFn
	m.mu.RUnlock()

	if restartFn != nil {
		if err := restartFn(n, remoteDir); err != nil {
			n.SetStatus(StatusError)
			return err
		}
	} else {
		if err := restartRemoteNode(n, remoteDir); err != nil {
			n.SetStatus(StatusError)
			return err
		}
	}

	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}
		if err := m.Connect(id); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("restart timed out")
	}
	n.SetStatus(StatusError)
	return fmt.Errorf("reconnect after restart: %w", lastErr)
}

// Rename updates the display name of a node and persists the change.
func (m *Manager) Rename(id, name string) error {
	if err := m.registry.Rename(id, name); err != nil {
		return err
	}
	return m.store.Save(m.registry.ToEntries())
}

// Remove disconnects and deletes a node, then persists the updated list.
func (m *Manager) Remove(id string) error {
	n := m.registry.Get(id)
	if n != nil {
		m.proxyMgr.Disconnect(n)
		m.tunnelMgr.Disconnect(n)
	}
	if err := m.registry.Remove(id); err != nil {
		return err
	}
	return m.store.Save(m.registry.ToEntries())
}

// Get returns a node by ID or nil if not found.
func (m *Manager) Get(id string) *Node {
	return m.registry.Get(id)
}

// List returns a snapshot slice of all nodes.
func (m *Manager) List() []*Node {
	return m.registry.List()
}

// ForwardCall sends a JSON-RPC call to a specific node's agentd via its proxy.
func (m *Manager) ForwardCall(nodeID, method string, params map[string]any, timeout time.Duration) (any, error) {
	n := m.Get(nodeID)
	if n == nil {
		return nil, fmt.Errorf("node %q not found", nodeID)
	}
	return m.proxyMgr.ForwardCall(n, method, params, timeout)
}

func (m *Manager) handleProxyDisconnect(nodeID string) {
	n := m.registry.Get(nodeID)
	if n == nil {
		return
	}

	m.tunnelMgr.Disconnect(n)

	m.mu.RLock()
	cb := m.proxyMgr.onEvent
	m.mu.RUnlock()
	if cb != nil {
		cb(n.ID, map[string]any{
			"jsonrpc": "2.0",
			"method":  "node.status_changed",
			"params": map[string]any{
				"nodeId": n.ID,
				"status": string(StatusDisconnected),
			},
		})
	}
}

func restartRemoteNode(n *Node, remoteDir string) error {
	if n.IsLocal() {
		return fmt.Errorf("local node restart is not supported")
	}

	cmd := exec.Command("ssh", sshArgsForNode(n, buildRestartCommand(remoteDir))...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ssh restart: %s", msg)
	}
	return nil
}

func sshArgsForNode(n *Node, remoteCommand string) []string {
	args := []string{
		"-o", "ConnectTimeout=5",
		"-o", "ServerAliveInterval=5",
	}
	if n.SSHKeyPath != "" {
		args = append(args, "-i", n.SSHKeyPath)
	}
	if n.SSHAlias != "" {
		args = append(args, n.SSHAlias, remoteCommand)
		return args
	}
	if n.SSHPort > 0 && n.SSHPort != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", n.SSHPort))
	}
	args = append(args, n.Host, remoteCommand)
	return args
}

func buildRestartCommand(remoteDir string) string {
	candidates := orderedRestartCandidates(remoteDir)
	quoted := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		quoted = append(quoted, restartCandidateShellWord(candidate))
	}
	return strings.Join([]string{
		"set -e",
		"found=''",
		"for bin in " + strings.Join(quoted, " ") + "; do if [ -x \"$bin\" ]; then found=\"$bin\"; break; fi; done",
		"if [ -z \"$found\" ]; then echo 'agentd binary not found in known locations' >&2; exit 127; fi",
		"pkill -f '[a]gentd start' 2>/dev/null || true",
		"sleep 1",
		"if command -v setsid >/dev/null 2>&1; then setsid \"$found\" start >/tmp/agentd.log 2>&1 < /dev/null & else nohup \"$found\" start >/tmp/agentd.log 2>&1 < /dev/null & fi",
	}, "; ")
}

func orderedRestartCandidates(remoteDir string) []string {
	candidates := make([]string, 0, 4)
	seen := make(map[string]struct{})
	add := func(path string) {
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		candidates = append(candidates, path)
	}
	if remoteDir != "" {
		add(filepath.Join(remoteDir, "agentd"))
	}
	add("$HOME/bin/agentd")
	add("$HOME/agentd")
	add("/opt/agentd/agentd")
	return candidates
}

func restartCandidateShellWord(candidate string) string {
	if strings.HasPrefix(candidate, "$HOME/") {
		return "\"$HOME/" + strings.TrimPrefix(candidate, "$HOME/") + "\""
	}
	return shellQuote(candidate)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
