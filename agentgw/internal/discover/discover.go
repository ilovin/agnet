package discover

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/phone-talk/agentgw/internal/nodecfg"
	"golang.org/x/crypto/ssh"
	"net"

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

	// Connect to SSH server
	addr := fmt.Sprintf("%s:%d", host.HostName, host.Port)
	client, err := ssh.Dial("tcp", addr, config)
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
