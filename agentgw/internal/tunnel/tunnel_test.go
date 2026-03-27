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

func startFakeSSHServer(t *testing.T, remoteTarget string) (addr string, cleanup func()) {
	t.Helper()
	signer, err := generateSigner()
	if err != nil {
		t.Fatalf("generate signer: %v", err)
	}
	cfg := &gossh.ServerConfig{NoClientAuth: true}
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
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return gossh.NewSignerFromKey(priv)
}

func TestTunnelForwardsTraffic(t *testing.T) {
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
			go io.Copy(c, c)
		}
	}()

	sshAddr, cleanup := startFakeSSHServer(t, echoLn.Addr().String())
	defer cleanup()

	host, portStr, _ := net.SplitHostPort(sshAddr)
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	tun, err := tunnel.New(tunnel.Config{
		SSHHost:    host,
		SSHPort:    port,
		RemoteHost: "127.0.0.1",
		RemotePort: echoLn.Addr().(*net.TCPAddr).Port,
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
