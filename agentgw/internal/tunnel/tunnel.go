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
	client   *gossh.Client
	listener net.Listener
	remote   string
}

// New establishes the SSH connection and starts a local listener.
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
			return
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
