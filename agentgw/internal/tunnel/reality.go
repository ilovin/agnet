package tunnel

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

type RealityConfig struct {
	PublicKey  string
	ShortId   string
	ServerName string
}

func (c *Client) SetReality(cfg *RealityConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reality = cfg
}

func dialReality(ctx context.Context, addr string, cfg *RealityConfig) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
		port = "443"
	}

	serverPub, err := base64.RawStdEncoding.DecodeString(cfg.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}

	shortId, err := hex.DecodeString(cfg.ShortId)
	if err != nil {
		return nil, fmt.Errorf("decode short id: %w", err)
	}

	dialer := net.Dialer{Timeout: 10 * time.Second}
	rawConn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}

	uConn := utls.UClient(rawConn, &utls.Config{
		ServerName:         cfg.ServerName,
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1"},
	}, utls.HelloChrome_Auto)

	if err := uConn.BuildHandshakeState(); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("build handshake: %w", err)
	}

	hello := uConn.HandshakeState.Hello

	ks := uConn.HandshakeState.State13.KeyShareKeys
	if ks == nil || ks.Ecdhe == nil {
		rawConn.Close()
		return nil, fmt.Errorf("no ECDHE key in handshake state")
	}
	ephPriv := ks.Ecdhe.Bytes()

	sharedSecret, err := curve25519.X25519(ephPriv, serverPub)
	if err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("x25519: %w", err)
	}

	authKey := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, sharedSecret, hello.Random[:20], []byte("REALITY")), authKey); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("hkdf: %w", err)
	}

	block, _ := aes.NewCipher(authKey)
	aead, _ := cipher.NewGCM(block)

	plaintext := make([]byte, 16)
	binary.BigEndian.PutUint32(plaintext[4:8], uint32(time.Now().Unix()))
	copy(plaintext[8:16], shortId)

	nonce := hello.Random[20:32]

	// Wrap the raw connection to intercept the first TLS record (ClientHello)
	// and patch the sessionId with the encrypted REALITY auth payload.
	wrapper := &realityPatchConn{
		Conn:      rawConn,
		aead:      aead,
		nonce:     nonce,
		plaintext: plaintext,
		patched:   false,
	}

	// Replace the underlying connection so uTLS writes through our wrapper
	uConn.SetUnderlyingConn(wrapper)

	if err := uConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("reality handshake: %w", err)
	}

	return uConn, nil
}

// realityPatchConn wraps a net.Conn and patches the first TLS ClientHello
// record's sessionId field with the REALITY auth payload before sending.
type realityPatchConn struct {
	net.Conn
	aead      cipher.AEAD
	nonce     []byte
	plaintext []byte
	mu        sync.Mutex
	patched   bool
}

func (c *realityPatchConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	if c.patched {
		c.mu.Unlock()
		return c.Conn.Write(b)
	}

	// The first write from uTLS is the TLS record containing the ClientHello.
	// TLS record format: type(1) + version(2) + length(2) + payload
	// ClientHello payload: type(1) + len(3) + version(2) + random(32) + sessionIdLen(1) + sessionId(32) + ...
	// sessionId offset in the TLS record = 5 (record header) + 4 (handshake header) + 2 (version) + 32 (random) + 1 (sessionId len) = 44
	// sessionId is at bytes [44:76] in the TLS record

	const sessionIdStart = 5 + 4 + 2 + 32 + 1 // = 44
	const sessionIdEnd = sessionIdStart + 32    // = 76

	if len(b) >= sessionIdEnd && b[0] == 0x16 && b[5] == 0x01 && b[sessionIdStart-1] == 32 {
		// Build AAD: the handshake message (starting at offset 5) with sessionId zeroed
		handshakeMsg := make([]byte, len(b)-5)
		copy(handshakeMsg, b[5:])
		for i := 0; i < 32; i++ {
			handshakeMsg[sessionIdStart-5+i] = 0
		}

		ciphertext := c.aead.Seal(nil, c.nonce, c.plaintext, handshakeMsg)
		copy(b[sessionIdStart:sessionIdEnd], ciphertext)
		c.patched = true
	}
	c.mu.Unlock()

	return c.Conn.Write(b)
}
