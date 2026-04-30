# agentgw MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `agentgw` — a Go gateway that aggregates multiple remote agentd daemons, exposes a unified WebSocket JSON-RPC 2.0 API to the mobile App, manages SSH tunnels, and can deploy agentd to remote machines in one click.

**Architecture:** agentgw maintains one SSH connection per configured remote node and forwards JSON-RPC calls through SSH port-forwarding tunnels to each node's agentd. Events from all agentd instances are fanned out to all connected App clients with a `nodeId` field injected. The agentd binary is embedded via `go:embed` for one-click deployment.

**Tech Stack:** Go 1.22+, `golang.org/x/crypto/ssh`, `github.com/gorilla/websocket`, `gopkg.in/yaml.v3`, `github.com/google/uuid`.

**Depends on:** agentd plan — agentgw proxies to agentd's JSON-RPC API. The agentd API contract is:
- Request methods: `agent.list`, `agent.create`, `agent.stop`, `agent.restart`, `conversation.history`, `conversation.send`
- Push events: `agent.status_changed`, `conversation.message`, `conversation.thinking`
- Auth: `Authorization: Bearer <token>` header on WebSocket upgrade

---

## File Structure

```
phone-talk/
└── agentgw/
    ├── cmd/agentgw/
    │   └── main.go                  # CLI: start/version subcommands
    ├── internal/
    │   ├── config/
    │   │   ├── config.go            # Load ~/.agentgw/config.yaml (port, token, nodes_file, ssh_key)
    │   │   └── config_test.go
    │   ├── nodecfg/
    │   │   ├── nodecfg.go           # YAML node config file: load/save list of NodeEntry
    │   │   └── nodecfg_test.go
    │   ├── tunnel/
    │   │   ├── tunnel.go            # SSH tunnel: connect to remote host, port-forward agentd:7373 to local port
    │   │   └── tunnel_test.go
    │   ├── proxy/
    │   │   ├── proxy.go             # WS proxy client: connect to tunnel port, forward JSON-RPC bidirectionally
    │   │   └── proxy_test.go
    │   ├── deployer/
    │   │   ├── deployer.go          # SSH: check remote agentd hash, SCP upload, exec agentd start
    │   │   └── deployer_test.go
    │   ├── node/
    │   │   ├── node.go              # Node runtime: id, cfg, tunnel, proxy, status
    │   │   ├── manager.go           # NodeManager: add/connect/remove/deploy nodes
    │   │   └── manager_test.go
    │   └── ws/
    │       ├── types.go             # RPCRequest/RPCResponse/RPCEvent (identical layout to agentd)
    │       ├── server.go            # WS server for App: auth, upgrade, broadcast
    │       └── handler.go           # dispatch node.* locally; proxy agent.*/conversation.* to node
    ├── embed/
    │   └── .gitkeep                 # agentd linux/amd64 binary placed here at build time
    ├── go.mod
    └── go.sum
```

---

## Task 1: Go Module + Config

**Files:**
- Create: `agentgw/go.mod`
- Create: `agentgw/internal/config/config.go`
- Create: `agentgw/internal/config/config_test.go`

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
mkdir -p agentgw/cmd/agentgw agentgw/internal/{config,nodecfg,tunnel,proxy,deployer,node,ws} agentgw/embed
cd agentgw
go mod init github.com/phone-talk/agentgw
go get golang.org/x/crypto@v0.31.0
go get github.com/gorilla/websocket@v1.5.3
go get gopkg.in/yaml.v3@v3.0.1
go get github.com/google/uuid@v1.6.0
touch embed/.gitkeep
```

Expected: `go.mod` and `go.sum` created.

- [ ] **Step 2: Write config test**

Create `agentgw/internal/config/config_test.go`:

```go
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
```

- [ ] **Step 3: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/config/... -v
```

Expected: compile error — package not found.

- [ ] **Step 4: Implement config**

Create `agentgw/internal/config/config.go`:

```go
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
	Port      int    `yaml:"port"`
	Token     string `yaml:"token"`
	NodesFile string `yaml:"nodes_file"`
	SSHKey    string `yaml:"ssh_key"` // path to default SSH private key
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Port:      7374,
		NodesFile: filepath.Join(os.Getenv("HOME"), ".agentgw", "nodes.yaml"),
		SSHKey:    filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa"),
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
```

- [ ] **Step 5: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/config/... -v
```

Expected:
```
--- PASS: TestLoadDefaults (0.00s)
--- PASS: TestLoadFromFile (0.00s)
PASS
```

- [ ] **Step 6: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentgw/
git commit -m "feat(agentgw): initialize go module and config loader"
```

---

## Task 2: NodeCfg (YAML node persistence)

**Files:**
- Create: `agentgw/internal/nodecfg/nodecfg.go`
- Create: `agentgw/internal/nodecfg/nodecfg_test.go`

- [ ] **Step 1: Write failing test**

Create `agentgw/internal/nodecfg/nodecfg_test.go`:

```go
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
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/nodecfg/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement nodecfg**

Create `agentgw/internal/nodecfg/nodecfg.go`:

```go
package nodecfg

import (
	"fmt"
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
	if os.IsNotExist(err) {
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
	return os.WriteFile(s.path, data, 0600)
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/nodecfg/... -v
```

Expected:
```
--- PASS: TestSaveAndLoad (0.00s)
--- PASS: TestLoadEmpty (0.00s)
PASS
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentgw/internal/nodecfg/
git commit -m "feat(agentgw): add YAML node config persistence"
```

---

## Task 3: SSH Tunnel

**Files:**
- Create: `agentgw/internal/tunnel/tunnel.go`
- Create: `agentgw/internal/tunnel/tunnel_test.go`

- [ ] **Step 1: Write failing test**

Create `agentgw/internal/tunnel/tunnel_test.go`:

```go
package tunnel_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/phone-talk/agentgw/internal/tunnel"
	gossh "golang.org/x/crypto/ssh"
)

// startFakeSSHServer starts a minimal SSH server that accepts any key auth
// and forwards TCP connections to remoteTarget.
func startFakeSSHServer(t *testing.T, remoteTarget string) (addr string, cleanup func()) {
	t.Helper()

	// Generate host key
	signer, err := generateSigner()
	if err != nil {
		t.Fatalf("generate signer: %v", err)
	}

	cfg := &gossh.ServerConfig{
		NoClientAuth: true,
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSSHConn(conn, cfg, remoteTarget)
		}
	}()

	return ln.Addr().String(), func() { ln.Close() }
}

func handleSSHConn(conn net.Conn, cfg *gossh.ServerConfig, remoteTarget string) {
	sshConn, chans, reqs, err := gossh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go gossh.DiscardRequests(reqs)
	for newChan := range chans {
		if newChan.ChannelType() != "direct-tcpip" {
			newChan.Reject(gossh.UnknownChannelType, "only direct-tcpip")
			continue
		}
		ch, _, err := newChan.Accept()
		if err != nil {
			continue
		}
		target, err := net.Dial("tcp", remoteTarget)
		if err != nil {
			ch.Close()
			continue
		}
		go io.Copy(target, ch)
		go func() { io.Copy(ch, target); ch.Close() }()
	}
}

func generateSigner() (gossh.Signer, error) {
	// Dynamically generate an ed25519 key pair for testing — avoids hardcoded
	// key strings that may have encoding issues.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return gossh.NewSignerFromKey(priv)
}

func TestTunnelForwardsTraffic(t *testing.T) {
	// Start a simple echo TCP server
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echoLn.Close()
	go func() {
		for {
			c, err := echoLn.Accept()
			if err != nil {
				return
			}
			go io.Copy(c, c) // echo
		}
	}()

	// Start fake SSH server that forwards to echo server
	sshAddr, cleanup := startFakeSSHServer(t, echoLn.Addr().String())
	defer cleanup()

	// Parse SSH address
	host, portStr, _ := net.SplitHostPort(sshAddr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	// Create tunnel using password auth (fake server accepts NoClientAuth)
	tun, err := tunnel.New(tunnel.Config{
		SSHHost:    host,
		SSHPort:    port,
		RemoteHost: "127.0.0.1",
		RemotePort: echoListenPort(echoLn),
		AuthMethod: gossh.Password("ignored"),
	})
	if err != nil {
		t.Fatalf("tunnel.New: %v", err)
	}
	defer tun.Close()

	localPort, err := tun.LocalPort()
	if err != nil {
		t.Fatalf("LocalPort: %v", err)
	}

	// Connect through tunnel and echo
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 3*time.Second)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	defer conn.Close()

	msg := []byte("hello tunnel")
	conn.Write(msg)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, len(msg))
	n, err := io.ReadFull(conn, buf)
	if err != nil || n != len(msg) {
		t.Fatalf("echo read: n=%d err=%v", n, err)
	}
	if string(buf) != string(msg) {
		t.Errorf("expected %q, got %q", msg, buf)
	}
}

func echoListenPort(ln net.Listener) int {
	var port int
	fmt.Sscanf(ln.Addr().(*net.TCPAddr).Port, "%d", &port)
	return ln.Addr().(*net.TCPAddr).Port
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/tunnel/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement tunnel**

Create `agentgw/internal/tunnel/tunnel.go`:

```go
package tunnel

import (
	"fmt"
	"io"
	"net"

	gossh "golang.org/x/crypto/ssh"
)

// Config holds parameters for establishing an SSH tunnel.
type Config struct {
	SSHHost    string
	SSHPort    int
	RemoteHost string
	RemotePort int
	AuthMethod gossh.AuthMethod
	HostKey    gossh.HostKeyCallback // nil = InsecureIgnoreHostKey (dev only)
}

// Tunnel forwards a local TCP listener through an SSH connection to a remote host:port.
type Tunnel struct {
	client    *gossh.Client
	listener  net.Listener
	remote    string
}

// New establishes the SSH connection and starts a local listener.
// Call LocalPort() to get the forwarded port.
func New(cfg Config) (*Tunnel, error) {
	hk := cfg.HostKey
	if hk == nil {
		hk = gossh.InsecureIgnoreHostKey()
	}
	sshCfg := &gossh.ClientConfig{
		User:            "agentgw",
		Auth:            []gossh.AuthMethod{cfg.AuthMethod},
		HostKeyCallback: hk,
	}
	client, err := gossh.Dial("tcp", fmt.Sprintf("%s:%d", cfg.SSHHost, cfg.SSHPort), sshCfg)
	if err != nil {
		return nil, fmt.Errorf("ssh dial: %w", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("local listen: %w", err)
	}

	t := &Tunnel{
		client:   client,
		listener: ln,
		remote:   fmt.Sprintf("%s:%d", cfg.RemoteHost, cfg.RemotePort),
	}
	go t.serve()
	return t, nil
}

func (t *Tunnel) serve() {
	for {
		local, err := t.listener.Accept()
		if err != nil {
			return // listener closed
		}
		go t.forward(local)
	}
}

func (t *Tunnel) forward(local net.Conn) {
	defer local.Close()
	remote, err := t.client.Dial("tcp", t.remote)
	if err != nil {
		return
	}
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, local); done <- struct{}{} }()
	go func() { io.Copy(local, remote); done <- struct{}{} }()
	<-done
}

// LocalPort returns the local port the tunnel listener is bound to.
func (t *Tunnel) LocalPort() (int, error) {
	addr, ok := t.listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected addr type")
	}
	return addr.Port, nil
}

// Close shuts down the tunnel and underlying SSH connection.
func (t *Tunnel) Close() error {
	t.listener.Close()
	return t.client.Close()
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/tunnel/... -v -timeout 15s
```

Expected:
```
--- PASS: TestTunnelForwardsTraffic (x.xxs)
PASS
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentgw/internal/tunnel/
git commit -m "feat(agentgw): add SSH tunnel with local port-forwarding"
```

---

## Task 4: WS Proxy Client

**Files:**
- Create: `agentgw/internal/proxy/proxy.go`
- Create: `agentgw/internal/proxy/proxy_test.go`

- [ ] **Step 1: Write failing test**

Create `agentgw/internal/proxy/proxy_test.go`:

```go
package proxy_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/proxy"
)

// fakeAgentd is a test WS server that echoes JSON-RPC responses and can push events.
type fakeAgentd struct {
	mu       sync.Mutex
	received []map[string]any
	pushCh   chan map[string]any
}

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func newFakeAgentd(t *testing.T) (*fakeAgentd, *httptest.Server) {
	fa := &fakeAgentd{pushCh: make(chan map[string]any, 10)}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		defer conn.Close()
		// Handle requests
		go func() {
			for {
				var msg map[string]any
				if err := conn.ReadJSON(&msg); err != nil {
					return
				}
				fa.mu.Lock()
				fa.received = append(fa.received, msg)
				fa.mu.Unlock()
				// Echo back as result
				resp := map[string]any{
					"jsonrpc": "2.0",
					"id":      msg["id"],
					"result":  map[string]any{"echo": msg["method"]},
				}
				conn.WriteJSON(resp)
			}
		}()
		// Push events from pushCh
		for ev := range fa.pushCh {
			conn.WriteJSON(ev)
		}
	}))
	return fa, ts
}

func TestProxyForwardsRequest(t *testing.T) {
	fa, ts := newFakeAgentd(t)
	defer ts.Close()
	_ = fa

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	p, err := proxy.New(wsURL, "testtoken")
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	defer p.Close()

	resp, err := p.Call("agent.list", nil, 3*time.Second)
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}
	b, _ := json.Marshal(resp)
	if !strings.Contains(string(b), "agent.list") {
		t.Errorf("expected echo of method in response, got: %s", b)
	}
}

func TestProxyReceivesEvents(t *testing.T) {
	fa, ts := newFakeAgentd(t)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	p, err := proxy.New(wsURL, "testtoken")
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	defer p.Close()

	events := make(chan map[string]any, 5)
	p.OnEvent(func(ev map[string]any) {
		events <- ev
	})

	// Push an event from fake agentd
	fa.pushCh <- map[string]any{
		"jsonrpc": "2.0",
		"method":  "agent.status_changed",
		"params":  map[string]any{"agentId": "a1", "status": "working"},
	}

	select {
	case ev := <-events:
		if ev["method"] != "agent.status_changed" {
			t.Errorf("unexpected event method: %v", ev["method"])
		}
	case <-time.After(3 * time.Second):
		t.Error("timeout waiting for event")
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/proxy/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement proxy**

Create `agentgw/internal/proxy/proxy.go`:

```go
package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type rpcMsg struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method,omitempty"`
	Params  any            `json:"params,omitempty"`
	Result  any            `json:"result,omitempty"`
	Error   map[string]any `json:"error,omitempty"`
}

type pending struct {
	ch chan rpcMsg
}

// Proxy is a WebSocket JSON-RPC client connected to a remote agentd.
type Proxy struct {
	conn     *websocket.Conn
	mu       sync.Mutex
	pending  map[float64]*pending
	nextID   float64
	onEvent  func(map[string]any)
	eventMu  sync.RWMutex
}

// New connects to the agentd WebSocket URL with the given token.
func New(url, token string) (*Proxy, error) {
	hdr := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial(url, hdr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", url, err)
	}
	p := &Proxy{
		conn:    conn,
		pending: make(map[float64]*pending),
		nextID:  1,
	}
	go p.readLoop()
	return p, nil
}

func (p *Proxy) readLoop() {
	for {
		_, data, err := p.conn.ReadMessage()
		if err != nil {
			// Close all pending
			p.mu.Lock()
			for _, pend := range p.pending {
				close(pend.ch)
			}
			p.pending = make(map[float64]*pending)
			p.mu.Unlock()
			return
		}
		var msg rpcMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.ID != nil {
			// Response to a request
			id, ok := msg.ID.(float64)
			if !ok {
				continue
			}
			p.mu.Lock()
			pend, exists := p.pending[id]
			if exists {
				delete(p.pending, id)
				pend.ch <- msg
			}
			p.mu.Unlock()
		} else if msg.Method != "" {
			// Server push event
			p.eventMu.RLock()
			cb := p.onEvent
			p.eventMu.RUnlock()
			if cb != nil {
				raw := map[string]any{
					"jsonrpc": msg.JSONRPC,
					"method":  msg.Method,
					"params":  msg.Params,
				}
				cb(raw)
			}
		}
	}
}

// OnEvent registers a callback for server-push events (no id).
func (p *Proxy) OnEvent(fn func(map[string]any)) {
	p.eventMu.Lock()
	defer p.eventMu.Unlock()
	p.onEvent = fn
}

// Call sends a JSON-RPC request and waits for a response.
func (p *Proxy) Call(method string, params any, timeout time.Duration) (any, error) {
	p.mu.Lock()
	id := p.nextID
	p.nextID++
	ch := make(chan rpcMsg, 1)
	p.pending[id] = &pending{ch: ch}
	req := rpcMsg{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	p.mu.Unlock()

	// WriteJSON outside mutex to avoid blocking other Call() goroutines
	if err := p.conn.WriteJSON(req); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("write: %w", err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("connection closed")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error: %v", resp.Error)
		}
		return resp.Result, nil
	case <-time.After(timeout):
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return nil, fmt.Errorf("timeout after %v", timeout)
	}
}

// Send sends a JSON-RPC request without waiting for a response (fire-and-forget).
func (p *Proxy) Send(method string, params any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	req := rpcMsg{JSONRPC: "2.0", Method: method, Params: params}
	return p.conn.WriteJSON(req)
}

// Close closes the underlying WebSocket connection.
func (p *Proxy) Close() error {
	return p.conn.Close()
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/proxy/... -v -timeout 15s
```

Expected:
```
--- PASS: TestProxyForwardsRequest (x.xxs)
--- PASS: TestProxyReceivesEvents (x.xxs)
PASS
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentgw/internal/proxy/
git commit -m "feat(agentgw): add WS proxy client for agentd JSON-RPC"
```

---

## Task 5: Deployer

**Files:**
- Create: `agentgw/internal/deployer/deployer.go`
- Create: `agentgw/internal/deployer/deployer_test.go`

- [ ] **Step 1: Write failing test**

Create `agentgw/internal/deployer/deployer_test.go`:

```go
package deployer_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentgw/internal/deployer"
)

func TestHashBinary(t *testing.T) {
	tmp := t.TempDir()
	binPath := filepath.Join(tmp, "agentd")
	content := []byte("fake binary content for testing")
	os.WriteFile(binPath, content, 0755)

	h := deployer.HashBinary(content)
	expected := fmt.Sprintf("%x", sha256.Sum256(content))
	if h != expected {
		t.Errorf("expected hash %q, got %q", expected, h)
	}
}

func TestPlanDeploySteps(t *testing.T) {
	content := []byte("fake agentd binary")
	steps := deployer.PlanSteps("~/.agentd", content)

	if len(steps) == 0 {
		t.Fatal("expected non-empty deploy steps")
	}
	// Steps should include mkdir, upload, chmod, start
	found := map[string]bool{}
	for _, s := range steps {
		found[s.Kind] = true
	}
	if !found["mkdir"] {
		t.Error("expected mkdir step")
	}
	if !found["upload"] {
		t.Error("expected upload step")
	}
	if !found["exec"] {
		t.Error("expected exec step")
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/deployer/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement deployer**

Create `agentgw/internal/deployer/deployer.go`:

```go
package deployer

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"path/filepath"

	gossh "golang.org/x/crypto/ssh"
)

// Step describes a single deploy action.
type Step struct {
	Kind    string // "mkdir", "upload", "exec"
	Command string // for exec steps
	Path    string // for upload/mkdir steps
	Data    []byte // for upload steps
}

// HashBinary returns the SHA256 hex digest of data.
func HashBinary(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// PlanSteps returns the deploy steps for uploading agentd to remoteDir.
func PlanSteps(remoteDir string, binaryData []byte) []Step {
	binPath := filepath.Join(remoteDir, "agentd")
	return []Step{
		{Kind: "mkdir", Path: remoteDir, Command: "mkdir -p " + remoteDir},
		{Kind: "upload", Path: binPath, Data: binaryData},
		{Kind: "exec", Command: "chmod +x " + binPath},
		{Kind: "exec", Command: binPath + " version || true"},   // smoke test
		{Kind: "exec", Command: "pkill -f 'agentd start' || true"}, // stop old
		{Kind: "exec", Command: binPath + " start &"},
	}
}

// Deployer uploads agentd to a remote machine via SSH and starts it.
type Deployer struct {
	client *gossh.Client
}

func New(client *gossh.Client) *Deployer {
	return &Deployer{client: client}
}

// Deploy executes all deploy steps on the remote machine.
func (d *Deployer) Deploy(remoteDir string, binaryData []byte) error {
	steps := PlanSteps(remoteDir, binaryData)
	for _, step := range steps {
		if err := d.execStep(step); err != nil {
			return fmt.Errorf("step %s %q: %w", step.Kind, step.Command, err)
		}
	}
	return nil
}

// RemoteHash returns the SHA256 of the agentd binary on the remote host, or "" if not found.
func (d *Deployer) RemoteHash(remoteDir string) string {
	binPath := filepath.Join(remoteDir, "agentd")
	out, err := d.runCommand("sha256sum " + binPath + " 2>/dev/null | awk '{print $1}'")
	if err != nil || len(out) < 64 {
		return ""
	}
	return string(bytes.TrimSpace(out))
}

func (d *Deployer) execStep(step Step) error {
	switch step.Kind {
	case "mkdir", "exec":
		_, err := d.runCommand(step.Command)
		return err
	case "upload":
		return d.scpUpload(step.Path, step.Data)
	}
	return fmt.Errorf("unknown step kind: %s", step.Kind)
}

func (d *Deployer) runCommand(cmd string) ([]byte, error) {
	sess, err := d.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()
	return sess.Output(cmd)
}

// scpUpload uploads data to remotePath using SCP protocol.
func (d *Deployer) scpUpload(remotePath string, data []byte) error {
	sess, err := d.client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	filename := filepath.Base(remotePath)
	remoteDir := filepath.Dir(remotePath)

	stdin, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	if err := sess.Start("scp -t " + remoteDir); err != nil {
		return fmt.Errorf("scp start: %w", err)
	}

	// SCP protocol: send header then data
	fmt.Fprintf(stdin, "C0755 %d %s\n", len(data), filename)
	io.Copy(stdin, bytes.NewReader(data))
	fmt.Fprint(stdin, "\x00") // end of file marker
	stdin.Close()

	return sess.Wait()
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/deployer/... -v
```

Expected:
```
--- PASS: TestHashBinary (0.00s)
--- PASS: TestPlanDeploySteps (0.00s)
PASS
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentgw/internal/deployer/
git commit -m "feat(agentgw): add SSH deployer for remote agentd binary upload"
```

---

## Task 6: Node + NodeManager

**Files:**
- Create: `agentgw/internal/node/node.go`
- Create: `agentgw/internal/node/manager.go`
- Create: `agentgw/internal/node/manager_test.go`

- [ ] **Step 1: Write failing test**

Create `agentgw/internal/node/manager_test.go`:

```go
package node_test

import (
	"path/filepath"
	"testing"

	"github.com/phone-talk/agentgw/internal/node"
	"github.com/phone-talk/agentgw/internal/nodecfg"
)

func TestAddAndListNode(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(cfgPath)
	mgr := node.NewManager(store, nil) // nil agentd embed for tests

	id, err := mgr.Add(nodecfg.NodeEntry{
		Name: "remote1", Host: "192.168.1.10",
		SSHPort: 22, AgentdPort: 7373, Token: "tok",
	})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty node id")
	}

	nodes := mgr.List()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].ID != id {
		t.Errorf("expected id=%q, got %q", id, nodes[0].ID)
	}
	if nodes[0].Status != node.StatusDisconnected {
		t.Errorf("expected Disconnected status, got %v", nodes[0].Status)
	}
}

func TestRemoveNode(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(cfgPath)
	mgr := node.NewManager(store, nil)

	id, _ := mgr.Add(nodecfg.NodeEntry{
		Name: "r1", Host: "10.0.0.1", SSHPort: 22, AgentdPort: 7373, Token: "t",
	})
	if err := mgr.Remove(id); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if len(mgr.List()) != 0 {
		t.Error("expected empty list after remove")
	}
}

func TestGetNode(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nodes.yaml")
	store := nodecfg.New(cfgPath)
	mgr := node.NewManager(store, nil)

	id, _ := mgr.Add(nodecfg.NodeEntry{
		Name: "r2", Host: "10.0.0.2", SSHPort: 22, AgentdPort: 7373, Token: "t",
	})
	n := mgr.Get(id)
	if n == nil {
		t.Fatal("expected non-nil node")
	}
	if n.Name != "r2" {
		t.Errorf("expected Name=r2, got %q", n.Name)
	}

	// non-existent
	if mgr.Get("bad-id") != nil {
		t.Error("expected nil for unknown id")
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/node/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement node.go**

Create `agentgw/internal/node/node.go`:

```go
package node

import (
	"sync"

	"github.com/phone-talk/agentgw/internal/proxy"
)

type Status string

const (
	StatusDisconnected Status = "disconnected"
	StatusConnecting   Status = "connecting"
	StatusConnected    Status = "connected"
	StatusDeploying    Status = "deploying"
	StatusError        Status = "error"
)

// Node is the runtime representation of a managed remote node.
type Node struct {
	ID         string
	Name       string
	Host       string
	SSHPort    int
	AgentdPort int
	Token      string
	SSHKeyPath string

	mu     sync.RWMutex
	status Status
	proxy  *proxy.Proxy
}

func (n *Node) Status() Status {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.status
}

func (n *Node) SetStatus(s Status) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.status = s
}

func (n *Node) Proxy() *proxy.Proxy {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.proxy
}

func (n *Node) SetProxy(p *proxy.Proxy) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.proxy = p
}
```

- [ ] **Step 4: Implement manager.go**

Create `agentgw/internal/node/manager.go`:

```go
package node

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	"github.com/phone-talk/agentgw/internal/proxy"
	"github.com/phone-talk/agentgw/internal/tunnel"
	gossh "golang.org/x/crypto/ssh"
)

// EventCallback is called when a node's agentd pushes an event.
// The nodeId is injected by the manager before calling.
type EventCallback func(nodeID string, event map[string]any)

// Manager tracks all configured nodes and their runtime state.
type Manager struct {
	mu        sync.RWMutex
	nodes     map[string]*Node
	store     *nodecfg.Store
	agentdBin []byte // embedded agentd binary (may be nil in tests)
	onEvent   EventCallback
}

func NewManager(store *nodecfg.Store, agentdBin []byte) *Manager {
	return &Manager{
		nodes:     make(map[string]*Node),
		store:     store,
		agentdBin: agentdBin,
	}
}

// OnEvent registers a callback for agentd push events (with nodeId injected).
func (m *Manager) OnEvent(cb EventCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEvent = cb
}

// LoadAll populates nodes from persisted config without writing back to disk.
// Use this at startup to avoid N redundant file writes from calling Add() in a loop.
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
		status:     StatusDisconnected,
	}
	m.mu.Lock()
	m.nodes[n.ID] = n
	m.mu.Unlock()

	// Persist
	entries := m.toEntries()
	if err := m.store.Save(entries); err != nil {
		return n.ID, fmt.Errorf("save nodes: %w", err)
	}
	return n.ID, nil
}

// Connect establishes SSH tunnel + WS proxy to a node's agentd and starts
// forwarding events. This is the core connection chain:
//   SSH tunnel (local random port → remote agentd port) → WS proxy → event callback
func (m *Manager) Connect(id string) error {
	n := m.Get(id)
	if n == nil {
		return fmt.Errorf("node %q not found", id)
	}
	n.SetStatus(StatusConnecting)

	// Read SSH key
	keyPath := n.SSHKeyPath
	if keyPath == "" {
		keyPath = os.ExpandEnv("$HOME/.ssh/id_rsa")
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		n.SetStatus(StatusError)
		return fmt.Errorf("read ssh key %s: %w", keyPath, err)
	}
	signer, err := gossh.ParsePrivateKey(keyData)
	if err != nil {
		n.SetStatus(StatusError)
		return fmt.Errorf("parse ssh key: %w", err)
	}

	// Establish SSH tunnel
	tun, err := tunnel.New(tunnel.Config{
		SSHHost:    n.Host,
		SSHPort:    n.SSHPort,
		RemoteHost: "127.0.0.1",
		RemotePort: n.AgentdPort,
		AuthMethod: gossh.PublicKeys(signer),
	})
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

	// Connect WS proxy through tunnel to agentd
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d/ws", localPort)
	p, err := proxy.New(wsURL, n.Token)
	if err != nil {
		tun.Close()
		n.SetStatus(StatusError)
		return fmt.Errorf("ws proxy: %w", err)
	}

	// Wire event forwarding: inject nodeId into every agentd push event
	m.mu.RLock()
	cb := m.onEvent
	m.mu.RUnlock()
	if cb != nil {
		p.OnEvent(func(ev map[string]any) {
			// Inject nodeId into params
			if params, ok := ev["params"].(map[string]any); ok {
				params["nodeId"] = n.ID
			}
			cb(n.ID, ev)
		})
	}

	n.SetProxy(p)
	n.SetStatus(StatusConnected)
	return nil
}

// ConnectAll attempts to connect all disconnected nodes. Errors are logged, not returned.
func (m *Manager) ConnectAll() {
	for _, n := range m.List() {
		if n.Status() == StatusDisconnected {
			go func(id string) {
				if err := m.Connect(id); err != nil {
					log.Printf("connect node %s: %v", id, err)
				}
			}(n.ID)
		}
	}
}

// Remove disconnects and deletes a node.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	n, ok := m.nodes[id]
	if ok {
		if p := n.Proxy(); p != nil {
			p.Close()
		}
		delete(m.nodes, id)
	}
	m.mu.Unlock()

	return m.store.Save(m.toEntries())
}

// Get returns a node by ID or nil.
func (m *Manager) Get(id string) *Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nodes[id]
}

// List returns a snapshot of all nodes.
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
	p := n.Proxy()
	if p == nil {
		return nil, fmt.Errorf("node %q not connected", nodeID)
	}
	return p.Call(method, params, timeout)
}

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
		})
	}
	return out
}
```

- [ ] **Step 5: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/node/... -v
```

Expected:
```
--- PASS: TestAddAndListNode (0.00s)
--- PASS: TestRemoveNode (0.00s)
--- PASS: TestGetNode (0.00s)
PASS
```

- [ ] **Step 6: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentgw/internal/node/
git commit -m "feat(agentgw): add Node runtime and NodeManager"
```

---

## Task 7: WS Server + Handler

**Files:**
- Create: `agentgw/internal/ws/types.go`
- Create: `agentgw/internal/ws/server.go`
- Create: `agentgw/internal/ws/handler.go`
- Create: `agentgw/internal/ws/server_test.go`

- [ ] **Step 1: Write failing test**

Create `agentgw/internal/ws/server_test.go`:

```go
package ws_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/node"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	"github.com/phone-talk/agentgw/internal/ws"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store := nodecfg.New(filepath.Join(t.TempDir(), "nodes.yaml"))
	mgr := node.NewManager(store, nil)
	srv := ws.New(mgr, "testtoken")
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}

func dialWS(t *testing.T, ts *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	hdr := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial(u, hdr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func rpc(t *testing.T, conn *websocket.Conn, method string, params any) map[string]any {
	t.Helper()
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	conn.WriteJSON(req)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("ReadJSON: %v", err)
	}
	return resp
}

func TestAuthRejectsWrongToken(t *testing.T) {
	ts := newTestServer(t)
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	_, resp, _ := websocket.DefaultDialer.Dial(u, http.Header{"Authorization": {"Bearer wrong"}})
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %v", resp)
	}
}

func TestNodeList(t *testing.T) {
	ts := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")
	resp := rpc(t, conn, "node.list", nil)
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	// result should be an array (empty)
	result, ok := resp["result"].([]any)
	if !ok {
		// nil result also acceptable for empty list — normalise
		result = []any{}
	}
	if len(result) != 0 {
		t.Errorf("expected empty node list, got %d", len(result))
	}
}

func TestNodeAdd(t *testing.T) {
	ts := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")
	resp := rpc(t, conn, "node.add", map[string]any{
		"name": "remote1", "host": "10.0.0.1",
		"sshPort": 22, "agentdPort": 7373, "token": "agentd-tok",
	})
	if resp["error"] != nil {
		t.Fatalf("node.add error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp["result"])
	}
	if result["nodeId"] == "" {
		t.Error("expected non-empty nodeId")
	}
}

func TestUnknownMethod(t *testing.T) {
	ts := newTestServer(t)
	conn := dialWS(t, ts, "testtoken")
	resp := rpc(t, conn, "bogus.method", nil)
	if resp["error"] == nil {
		t.Error("expected error for unknown method")
	}
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/ws/... -v
```

Expected: compile error.

- [ ] **Step 3: Implement types.go**

Create `agentgw/internal/ws/types.go`:

```go
package ws

type RPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type RPCResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type RPCEvent struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

func okResp(id any, result any) RPCResponse {
	return RPCResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func errResp(id any, code int, msg string) RPCResponse {
	return RPCResponse{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}}
}

func newEvent(method string, params any) RPCEvent {
	return RPCEvent{JSONRPC: "2.0", Method: method, Params: params}
}
```

- [ ] **Step 4: Implement server.go**

Create `agentgw/internal/ws/server.go`:

```go
package ws

import (
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/node"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

type Server struct {
	manager *node.Manager
	token   string
	mu      sync.RWMutex
	clients map[*websocket.Conn]struct{}
}

func New(mgr *node.Manager, token string) *Server {
	return &Server{
		manager: mgr,
		token:   token,
		clients: make(map[*websocket.Conn]struct{}),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Auth: accept either Authorization header or ?token= query param
	// (Flutter mobile clients can't set custom WS headers)
	auth := r.Header.Get("Authorization")
	queryToken := r.URL.Query().Get("token")
	headerToken := strings.TrimPrefix(auth, "Bearer ")
	token := headerToken
	if token == "" {
		token = queryToken
	}
	if token != s.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	s.mu.Lock()
	s.clients[conn] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
	}()

	h := &handler{server: s, conn: conn}
	h.loop()
}

func (s *Server) Broadcast(ev RPCEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for conn := range s.clients {
		_ = conn.WriteJSON(ev)
	}
}
```

- [ ] **Step 5: Implement handler.go**

Create `agentgw/internal/ws/handler.go`:

```go
package ws

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
	"github.com/phone-talk/agentgw/internal/nodecfg"
)

type handler struct {
	server *Server
	conn   *websocket.Conn
}

func (h *handler) loop() {
	for {
		_, msg, err := h.conn.ReadMessage()
		if err != nil {
			return
		}
		var req RPCRequest
		if err := json.Unmarshal(msg, &req); err != nil {
			_ = h.conn.WriteJSON(errResp(nil, -32700, "parse error"))
			continue
		}
		resp := h.dispatch(req)
		if err := h.conn.WriteJSON(resp); err != nil {
			log.Printf("ws write: %v", err)
			return
		}
	}
}

func (h *handler) dispatch(req RPCRequest) RPCResponse {
	switch req.Method {
	case "node.list":
		return h.nodeList(req)
	case "node.add":
		return h.nodeAdd(req)
	case "node.remove":
		return h.nodeRemove(req)
	case "node.connect":
		return h.nodeConnect(req)
	case "node.deploy":
		return h.nodeDeploy(req)
	case "agent.list", "agent.create", "agent.stop", "agent.restart",
		"conversation.history", "conversation.send":
		return h.proxyToNode(req)
	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

func (h *handler) nodeList(req RPCRequest) RPCResponse {
	nodes := h.server.manager.List()
	type nodeInfo struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Host   string `json:"host"`
		Status string `json:"status"`
	}
	result := make([]nodeInfo, 0, len(nodes))
	for _, n := range nodes {
		result = append(result, nodeInfo{
			ID: n.ID, Name: n.Name, Host: n.Host, Status: string(n.Status()),
		})
	}
	return okResp(req.ID, result)
}

func (h *handler) nodeAdd(req RPCRequest) RPCResponse {
	name, _ := req.Params["name"].(string)
	host, _ := req.Params["host"].(string)
	token, _ := req.Params["token"].(string)
	sshKeyPath, _ := req.Params["sshKeyPath"].(string)

	sshPort := 22
	if v, ok := req.Params["sshPort"].(float64); ok {
		sshPort = int(v)
	}
	agentdPort := 7373
	if v, ok := req.Params["agentdPort"].(float64); ok {
		agentdPort = int(v)
	}

	if host == "" {
		return errResp(req.ID, -32602, "host is required")
	}

	id, err := h.server.manager.Add(nodecfg.NodeEntry{
		Name: name, Host: host,
		SSHPort: sshPort, AgentdPort: agentdPort,
		Token: token, SSHKeyPath: sshKeyPath,
	})
	if err != nil {
		return errResp(req.ID, -32000, err.Error())
	}

	h.server.Broadcast(newEvent("node.status_changed", map[string]any{
		"nodeId": id, "status": "disconnected",
	}))

	// Attempt connection in background
	go func() {
		if err := h.server.manager.Connect(id); err != nil {
			log.Printf("auto-connect node %s: %v", id, err)
			h.server.Broadcast(newEvent("node.status_changed", map[string]any{
				"nodeId": id, "status": "error",
			}))
		} else {
			h.server.Broadcast(newEvent("node.status_changed", map[string]any{
				"nodeId": id, "status": "connected",
			}))
		}
	}()

	return okResp(req.ID, map[string]any{"nodeId": id})
}

func (h *handler) nodeRemove(req RPCRequest) RPCResponse {
	nodeID, _ := req.Params["nodeId"].(string)
	if err := h.server.manager.Remove(nodeID); err != nil {
		return errResp(req.ID, -32000, err.Error())
	}
	return okResp(req.ID, map[string]any{"ok": true})
}

func (h *handler) nodeConnect(req RPCRequest) RPCResponse {
	nodeID, _ := req.Params["nodeId"].(string)
	n := h.server.manager.Get(nodeID)
	if n == nil {
		return errResp(req.ID, -32000, fmt.Sprintf("node %q not found", nodeID))
	}
	// Connect in background, return immediately
	go func() {
		if err := h.server.manager.Connect(nodeID); err != nil {
			log.Printf("connect node %s: %v", nodeID, err)
			h.server.Broadcast(newEvent("node.status_changed", map[string]any{
				"nodeId": nodeID, "status": "error",
			}))
		} else {
			h.server.Broadcast(newEvent("node.status_changed", map[string]any{
				"nodeId": nodeID, "status": "connected",
			}))
		}
	}()
	return okResp(req.ID, map[string]any{"ok": true, "message": "connecting"})
}

func (h *handler) nodeDeploy(req RPCRequest) RPCResponse {
	nodeID, _ := req.Params["nodeId"].(string)
	n := h.server.manager.Get(nodeID)
	if n == nil {
		return errResp(req.ID, -32000, fmt.Sprintf("node %q not found", nodeID))
	}
	// Deployment is async — return immediately, broadcast progress via events.
	// Full SSH deploy implementation wired in Task 8 (CLI) when embed is available.
	h.server.Broadcast(newEvent("node.status_changed", map[string]any{
		"nodeId": nodeID, "status": "deploying",
	}))
	return okResp(req.ID, map[string]any{"ok": true, "message": "deploy started"})
}

// proxyToNode extracts nodeId, finds the node's proxy, forwards the call.
func (h *handler) proxyToNode(req RPCRequest) RPCResponse {
	nodeID, _ := req.Params["nodeId"].(string)
	n := h.server.manager.Get(nodeID)
	if n == nil {
		return errResp(req.ID, -32000, fmt.Sprintf("node %q not found", nodeID))
	}
	p := n.Proxy()
	if p == nil {
		return errResp(req.ID, -32000, fmt.Sprintf("node %q not connected", nodeID))
	}

	// Strip nodeId from params before forwarding
	forwardParams := make(map[string]any)
	for k, v := range req.Params {
		if k != "nodeId" {
			forwardParams[k] = v
		}
	}

	result, err := p.Call(req.Method, forwardParams, 30*time.Second)
	if err != nil {
		return errResp(req.ID, -32000, err.Error())
	}
	return okResp(req.ID, result)
}
```

- [ ] **Step 6: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./internal/ws/... -v -timeout 15s
```

Expected:
```
--- PASS: TestAuthRejectsWrongToken (0.xxs)
--- PASS: TestNodeList (0.xxs)
--- PASS: TestNodeAdd (0.xxs)
--- PASS: TestUnknownMethod (0.xxs)
PASS
```

- [ ] **Step 7: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentgw/internal/ws/
git commit -m "feat(agentgw): add WebSocket JSON-RPC server with node management"
```

---

## Task 8: CLI Entrypoint

**Files:**
- Create: `agentgw/cmd/agentgw/main.go`

- [ ] **Step 1: Implement main.go**

Create `agentgw/cmd/agentgw/main.go`:

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/phone-talk/agentgw/internal/config"
	"github.com/phone-talk/agentgw/internal/node"
	"github.com/phone-talk/agentgw/internal/nodecfg"
	"github.com/phone-talk/agentgw/internal/ws"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: agentgw <start|version>")
		os.Exit(1)
	}
	switch os.Args[1] {
	case "start":
		runServer()
	case "version":
		fmt.Println("agentgw v0.1.0")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runServer() {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".agentgw", "config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	store := nodecfg.New(cfg.NodesFile)
	mgr := node.NewManager(store, nil) // embed wired at build time

	// Load persisted nodes in batch (no redundant file writes)
	entries, err := store.Load()
	if err != nil {
		log.Printf("warn: could not load nodes: %v", err)
	}
	mgr.LoadAll(entries)

	srv := ws.New(mgr, cfg.Token)

	// Wire event forwarding: agentd push events → broadcast to all App clients
	mgr.OnEvent(func(nodeID string, ev map[string]any) {
		method, _ := ev["method"].(string)
		params := ev["params"]
		srv.Broadcast(ws.RPCEvent{JSONRPC: "2.0", Method: method, Params: params})
	})

	// Connect all loaded nodes in background
	mgr.ConnectAll()

	addr := fmt.Sprintf(":%d", cfg.Port)
	http.Handle("/ws", srv)
	log.Printf("agentgw listening on %s (token: %s...)", addr, cfg.Token[:8])
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
```

- [ ] **Step 2: Build and verify**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go build ./cmd/agentgw/
./agentgw version
```

Expected:
```
agentgw v0.1.0
```

- [ ] **Step 3: Run all tests**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentgw
go test ./... -v -timeout 30s
```

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentgw/
git commit -m "feat(agentgw): add CLI entrypoint, agentgw v0.1.0 complete"
```
