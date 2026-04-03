package tunnel

import (
	"fmt"
	"net"
	"os/exec"

	gossh "golang.org/x/crypto/ssh"
)

// Config holds parameters for establishing an SSH tunnel.
type Config struct {
	SSHHost    string
	SSHPort    int
	RemoteHost string
	RemotePort int
	SSHKeyPath string
	SSHUser    string
	AuthMethod gossh.AuthMethod // optional auth method (if nil, uses SSHKeyPath)
	SSHAlias   string           // optional SSH config alias (e.g., "ws"), takes precedence over SSHHost
}

// Tunnel forwards a local TCP port to a remote host:port via ssh subprocess.
type Tunnel struct {
	cmd      *exec.Cmd
	listener net.Listener
	localPort int
}

// New picks a free local port, starts an ssh -L tunnel subprocess, and returns.
func New(cfg Config) (*Tunnel, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("local listen: %w", err)
	}
	localPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // release so ssh can use it

	remote := fmt.Sprintf("%s:%d", cfg.RemoteHost, cfg.RemotePort)
	fwd := fmt.Sprintf("%d:%s", localPort, remote)

	args := []string{
		"-N",
		"-L", fwd,
		"-o", "StrictHostKeyChecking=no",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30",
	}

	// Determine SSH target host
	host := cfg.SSHHost
	if cfg.SSHAlias != "" {
		// Use SSH config alias (e.g., "ws" from .ssh/config)
		host = cfg.SSHAlias
	} else {
		// Manual configuration
		if cfg.SSHKeyPath != "" {
			args = append(args, "-i", cfg.SSHKeyPath)
		}
		if cfg.SSHPort != 0 && cfg.SSHPort != 22 {
			args = append(args, "-p", fmt.Sprintf("%d", cfg.SSHPort))
		}
		if cfg.SSHUser != "" {
			host = cfg.SSHUser + "@" + host
		}
	}
	args = append(args, host)

	cmd := exec.Command("ssh", args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ssh start: %w", err)
	}

	// Wait for the tunnel port to become available (up to 10s)
	if err := waitPort(localPort, 10); err != nil {
		cmd.Process.Kill()
		return nil, fmt.Errorf("tunnel not ready: %w", err)
	}

	return &Tunnel{cmd: cmd, localPort: localPort}, nil
}

// LocalPort returns the local port the tunnel is bound to.
func (t *Tunnel) LocalPort() (int, error) {
	return t.localPort, nil
}

// Close kills the ssh subprocess.
func (t *Tunnel) Close() error {
	if t.cmd != nil && t.cmd.Process != nil {
		return t.cmd.Process.Kill()
	}
	return nil
}

func waitPort(port, maxSeconds int) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < maxSeconds*10; i++ {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			conn.Close()
			return nil
		}
		// sleep 100ms
		select {
		case <-make(chan struct{}):
		default:
		}
		// use time package via import would be cleaner, but avoid import cycle
		// simple busy-wait with syscall sleep
		waitMs(100)
	}
	return fmt.Errorf("port %d not open after %ds", port, maxSeconds)
}
