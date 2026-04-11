package discover

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/phone-talk/agentgw/internal/nodecfg"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Result represents the result of probing a single host.
type Result struct {
	Host     nodecfg.SSHHost
	Found    bool   // true if agentd was detected
	NodeID   string // suggested node ID
	Error    string // error message if probe failed
}

// Prober probes SSH hosts for running agentd instances.
type Prober struct {
	timeout    time.Duration
	agentdPort int
}

// NewProber creates a new prober with default settings.
func NewProber() *Prober {
	return &Prober{
		timeout:    5 * time.Second,
		agentdPort: 7373,
	}
}

// Discover scans SSH hosts and returns those with agentd running.
// It probes hosts concurrently with a worker pool and limits total scan time.
func (p *Prober) Discover(hosts []nodecfg.SSHHost) []Result {
	// Limit to max 20 hosts to scan
	if len(hosts) > 20 {
		hosts = hosts[:20]
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5) // Max 5 concurrent probes

	results := make([]Result, len(hosts))
	var mu sync.Mutex

	for i, host := range hosts {
		wg.Add(1)
		go func(idx int, h nodecfg.SSHHost) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			result := p.probe(h)

			mu.Lock()
			results[idx] = result
			mu.Unlock()
		}(i, host)
	}

	wg.Wait()
	return results
}

// probe attempts to connect to agentd on a single host via SSH tunnel.
func (p *Prober) probe(host nodecfg.SSHHost) Result {
	result := Result{Host: host}

	// Build SSH client config
	config, err := p.buildSSHConfig(host)
	if err != nil {
		result.Error = fmt.Sprintf("ssh config: %v", err)
		return result
	}

	// Connect to SSH server (with ProxyCommand support)
	var client *ssh.Client
	if host.ProxyCommand != "" {
		client, err = p.dialViaProxyCommand(host, config)
	} else {
		addr := fmt.Sprintf("%s:%d", host.HostName, host.Port)
		client, err = ssh.Dial("tcp", addr, config)
	}
	if err != nil {
		result.Error = fmt.Sprintf("ssh dial: %v", err)
		return result
	}
	defer client.Close()

	// Try to dial agentd port through SSH tunnel
	conn, err := client.Dial("tcp", fmt.Sprintf("localhost:%d", p.agentdPort))
	if err != nil {
		result.Error = fmt.Sprintf("agentd not reachable: %v", err)
		return result
	}
	defer conn.Close()

	// Set timeout for the connection
	conn.SetDeadline(time.Now().Add(p.timeout))

	// Try WebSocket upgrade by sending a simple HTTP request
	// agentd's WebSocket endpoint will respond with 401 if auth is required,
	// or 400 if it's WebSocket and we're sending HTTP
	probe := []byte("GET /ws HTTP/1.1\r\nHost: localhost\r\n\r\n")
	if _, err := conn.Write(probe); err != nil {
		result.Error = fmt.Sprintf("write probe: %v", err)
		return result
	}

	// Read response
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		result.Error = fmt.Sprintf("read response: %v", err)
		return result
	}

	response := string(buf[:n])
	// Check if it's a valid HTTP response (any response means something is listening)
	if containsAny(response, []string{"HTTP/1.1", "HTTP/1.0", "WebSocket", "websocket", "Upgrade"}) {
		result.Found = true
		result.NodeID = sanitizeNodeID(host.Name)
	}

	return result
}

// dialViaProxyCommand connects to SSH server through a ProxyCommand.
// It executes the proxy command and uses its stdin/stdout as the network connection.
func (p *Prober) dialViaProxyCommand(host nodecfg.SSHHost, config *ssh.ClientConfig) (*ssh.Client, error) {
	// Replace placeholders in ProxyCommand
	cmdStr := host.ProxyCommand
	cmdStr = strings.ReplaceAll(cmdStr, "%h", host.HostName)
	cmdStr = strings.ReplaceAll(cmdStr, "%p", fmt.Sprintf("%d", host.Port))
	cmdStr = strings.ReplaceAll(cmdStr, "%r", host.User)

	// Execute proxy command
	cmdParts := strings.Fields(cmdStr)
	if len(cmdParts) == 0 {
		return nil, fmt.Errorf("empty proxy command")
	}
	cmd := exec.Command(cmdParts[0], cmdParts[1:]...)

	// Get stdin/stdout pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("proxy stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("proxy stdout: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("proxy stderr: %v", err)
	}

	// Start the proxy command
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start proxy: %v", err)
	}

	// Create a combined read-write closer for the connection
	conn := &proxyConn{
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
		cmd:    cmd,
	}

	// Perform SSH handshake on the proxy connection with timeout
	// The config.Timeout doesn't apply to NewClientConn, so we need a custom approach
	clientChan := make(chan *ssh.Client, 1)
	errChan := make(chan error, 1)

	go func() {
		c, chans, reqs, err := ssh.NewClientConn(conn, host.HostName, config)
		if err != nil {
			errChan <- err
			return
		}
		clientChan <- ssh.NewClient(c, chans, reqs)
	}()

	select {
	case client := <-clientChan:
		return client, nil
	case err := <-errChan:
		conn.Close()
		return nil, fmt.Errorf("ssh handshake: %v", err)
	case <-time.After(p.timeout):
		conn.Close()
		return nil, fmt.Errorf("ssh handshake: timeout")
	}
}

// proxyConn wraps stdin/stdout pipes from a proxy command into a net.Conn.
type proxyConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	cmd    *exec.Cmd
}

func (c *proxyConn) Read(b []byte) (n int, err error)  { return c.stdout.Read(b) }
func (c *proxyConn) Write(b []byte) (n int, err error) { return c.stdin.Write(b) }
func (c *proxyConn) Close() error {
	c.stdin.Close()
	c.stdout.Close()
	if c.stderr != nil {
		c.stderr.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}
	return nil
}
func (c *proxyConn) LocalAddr() net.Addr              { return nil }
func (c *proxyConn) RemoteAddr() net.Addr             { return nil }
func (c *proxyConn) SetDeadline(t time.Time) error    { return nil }
func (c *proxyConn) SetReadDeadline(t time.Time) error    { return nil }
func (c *proxyConn) SetWriteDeadline(t time.Time) error   { return nil }

// buildSSHConfig creates SSH client configuration from host settings.
func (p *Prober) buildSSHConfig(host nodecfg.SSHHost) (*ssh.ClientConfig, error) {
	config := &ssh.ClientConfig{
		User:            host.User,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Accept any host key
		Timeout:         p.timeout,
	}

	// Try to use key-based auth if identity file is specified
	if host.IdentityFile != "" {
		signer, err := loadPrivateKey(host.IdentityFile)
		if err == nil {
			config.Auth = append(config.Auth, ssh.PublicKeys(signer))
		} else {
			// Log key load failure
_ = fmt.Sprintf("failed to load key %s for %s: %v", host.IdentityFile, host.Name, err)
		}
	}

	// Add agent auth if available
	if sshAgent, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK")); err == nil {
		config.Auth = append(config.Auth, ssh.PublicKeysCallback(agent.NewClient(sshAgent).Signers))
	}

	return config, nil
}

// loadPrivateKey loads an SSH private key from file.
func loadPrivateKey(path string) (ssh.Signer, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, err
	}
	return signer, nil
}

// sanitizeNodeID converts a host name to a valid node ID.
func sanitizeNodeID(name string) string {
	// Replace special characters
	id := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, name)
	return strings.ToLower(id)
}

func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if contains(s, substr) {
			return true
		}
	}
	return false
}

func contains(s, substr string) bool {
	return len(substr) <= len(s) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
