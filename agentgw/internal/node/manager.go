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

	"github.com/google/uuid"
	"github.com/phone-talk/agentgw/internal/deployer"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	"github.com/phone-talk/agentgw/internal/proxy"
	"github.com/phone-talk/agentgw/internal/tunnel"
	gossh "golang.org/x/crypto/ssh"
)

// resolveSSHKeyPath returns the first existing SSH private key from common locations.
func resolveSSHKeyPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return candidates[0] // fallback, will fail later with a clear error
}

// EventCallback is called when a node's agentd pushes an event.
type EventCallback func(nodeID string, event map[string]any)

// Manager tracks all configured nodes and their runtime state.
type Manager struct {
	mu        sync.RWMutex
	nodes     map[string]*Node
	store     *nodecfg.Store
	agentdBin []byte
	onEvent   EventCallback
	restartFn func(*Node, string) error
}

// NewManager creates a Manager backed by the given store.
// agentdBin is the embedded agentd binary (may be nil in tests).
func NewManager(store *nodecfg.Store, agentdBin []byte) *Manager {
	return &Manager{
		nodes:     make(map[string]*Node),
		store:     store,
		agentdBin: agentdBin,
	}
}

// OnEvent registers a callback for agentd push events (nodeId is injected into params).
func (m *Manager) OnEvent(cb EventCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEvent = cb
}

// SetRestartFunc overrides the restart implementation for tests.
func (m *Manager) SetRestartFunc(fn func(*Node, string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restartFn = fn
}

// LoadAll populates nodes from persisted config without writing back to disk.
func (m *Manager) LoadAll(entries []nodecfg.NodeEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range entries {
		if entry.ID == "" {
			entry.ID = uuid.New().String()
		}
		m.nodes[entry.ID] = &Node{
			ID:         entry.ID,
			Name:       entry.Name,
			Host:       entry.Host,
			SSHPort:    entry.SSHPort,
			AgentdPort: entry.AgentdPort,
			Token:      entry.Token,
			SSHKeyPath: entry.SSHKeyPath,
			SSHAlias:   entry.SSHAlias,
			status:     StatusDisconnected,
		}
	}
}

// Add creates a new node entry (not yet connected) and persists it.
func (m *Manager) Add(entry nodecfg.NodeEntry) (string, error) {
	if entry.ID == "" {
		entry.ID = uuid.New().String()
	}
	n := &Node{
		ID:         entry.ID,
		Name:       entry.Name,
		Host:       entry.Host,
		SSHPort:    entry.SSHPort,
		AgentdPort: entry.AgentdPort,
		Token:      entry.Token,
		SSHKeyPath: entry.SSHKeyPath,
		SSHAlias:   entry.SSHAlias,
		status:     StatusDisconnected,
	}
	m.mu.Lock()
	m.nodes[n.ID] = n
	m.mu.Unlock()

	entries := m.toEntries()
	if err := m.store.Save(entries); err != nil {
		return n.ID, fmt.Errorf("save nodes: %w", err)
	}
	return n.ID, nil
}

// Connect establishes an SSH tunnel + WS proxy to a node's agentd.
// For localhost/127.0.0.1, it connects directly without SSH tunnel.
func (m *Manager) Connect(id string) error {
	n := m.Get(id)
	if n == nil {
		return fmt.Errorf("node %q not found", id)
	}
	if existing := n.GetProxy(); existing != nil {
		_ = existing.Close()
		n.SetProxy(nil)
	}
	if existingTunnel := n.GetTunnel(); existingTunnel != nil {
		_ = existingTunnel.Close()
		n.SetTunnel(nil)
	}

	n.SetStatus(StatusConnecting)

	var wsURL string

	// For localhost, connect directly without SSH tunnel
	if n.Host == "localhost" || n.Host == "127.0.0.1" {
		wsURL = fmt.Sprintf("ws://%s:%d/ws", n.Host, n.AgentdPort)
	} else {
		tunCfg := tunnel.Config{
			SSHHost:    n.Host,
			SSHPort:    n.SSHPort,
			RemoteHost: "127.0.0.1",
			RemotePort: n.AgentdPort,
			SSHKeyPath: n.SSHKeyPath,
			SSHAlias:   n.SSHAlias,
		}

		// If SSHAlias is set, use it; otherwise use key-based auth
		if tunCfg.SSHAlias == "" {
			tunCfg.SSHKeyPath = resolveSSHKeyPath(tunCfg.SSHKeyPath)
		}

		tun, err := tunnel.New(tunCfg)
		if err != nil {
			n.SetStatus(StatusError)
			return fmt.Errorf("ssh tunnel: %w", err)
		}

		localPort, err := tun.LocalPort()
		if err != nil {
			tun.Close()
			n.SetStatus(StatusError)
			return fmt.Errorf("local port: %w", err)
		}
		n.SetTunnel(tun)

		wsURL = fmt.Sprintf("ws://127.0.0.1:%d/ws", localPort)
	}

	// Enable auto-reconnect for resilient connections
	p, err := proxy.NewWithReconnect(wsURL, n.Token, true)
	if err != nil {
		if tun := n.GetTunnel(); tun != nil {
			_ = tun.Close()
			n.SetTunnel(nil)
		}
		n.SetStatus(StatusError)
		return fmt.Errorf("ws proxy: %w", err)
	}

	m.mu.RLock()
	cb := m.onEvent
	m.mu.RUnlock()
	if cb != nil {
		p.OnEvent(func(ev map[string]any) {
			if ev == nil {
				ev = make(map[string]any)
			}
			params, ok := ev["params"].(map[string]any)
			if !ok {
				params = make(map[string]any)
				ev["params"] = params
			}
			params["nodeId"] = n.ID
			cb(n.ID, ev)
		})
	}
	p.OnDisconnect(func() {
		m.handleProxyDisconnect(n, p)
	})

	n.SetProxy(p)
	n.SetStatus(StatusConnected)
	log.Printf("node %q connected (%s)", n.Name, wsURL)
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

	if p := n.GetProxy(); p != nil {
		_ = p.Close()
		n.SetProxy(nil)
	}
	if tun := n.GetTunnel(); tun != nil {
		_ = tun.Close()
		n.SetTunnel(nil)
	}

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
	m.mu.Lock()
	n, ok := m.nodes[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("node %q not found", id)
	}
	n.Name = name
	m.mu.Unlock()
	return m.store.Save(m.toEntries())
}

// Remove disconnects and deletes a node, then persists the updated list.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	n, ok := m.nodes[id]
	if ok {
		if p := n.GetProxy(); p != nil {
			p.Close()
		}
		if tun := n.GetTunnel(); tun != nil {
			tun.Close()
		}
		delete(m.nodes, id)
	}
	m.mu.Unlock()

	return m.store.Save(m.toEntries())
}

// Get returns a node by ID or nil if not found.
func (m *Manager) Get(id string) *Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nodes[id]
}

// List returns a snapshot slice of all nodes.
func (m *Manager) List() []*Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, n)
	}
	return out
}

// ForwardCall sends a JSON-RPC call to a specific node's agentd via its proxy.
func (m *Manager) ForwardCall(nodeID, method string, params map[string]any, timeout time.Duration) (any, error) {
	n := m.Get(nodeID)
	if n == nil {
		return nil, fmt.Errorf("node %q not found", nodeID)
	}
	p := n.GetProxy()
	if p == nil {
		return nil, fmt.Errorf("node %q not connected", nodeID)
	}
	return p.Call(method, params, timeout)
}

func (m *Manager) handleProxyDisconnect(n *Node, disconnected *proxy.Proxy) {
	n.mu.Lock()
	if n.proxy != disconnected {
		n.mu.Unlock()
		return
	}
	n.proxy = nil
	oldTunnel := n.tunnel
	n.tunnel = nil
	n.status = StatusDisconnected
	n.mu.Unlock()

	if oldTunnel != nil {
		_ = oldTunnel.Close()
	}

	m.mu.RLock()
	cb := m.onEvent
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

// toEntries converts the in-memory node map to a slice for persistence.
func (m *Manager) toEntries() []nodecfg.NodeEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]nodecfg.NodeEntry, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, nodecfg.NodeEntry{
			ID:         n.ID,
			Name:       n.Name,
			Host:       n.Host,
			SSHPort:    n.SSHPort,
			AgentdPort: n.AgentdPort,
			SSHKeyPath: n.SSHKeyPath,
			Token:      n.Token,
			SSHAlias:   n.SSHAlias,
		})
	}
	return out
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
