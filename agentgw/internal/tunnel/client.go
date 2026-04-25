package tunnel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	grpcFrameHeader = 5
	maxFrameSize    = 4 * 1024 * 1024
	chromeUA        = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"
)

type Client struct {
	hubURL     string
	token      string
	localAddr  string
	localToken string
	mu         sync.Mutex
	cancel     context.CancelFunc
	done       chan struct{}
	wg         sync.WaitGroup
	reality    *RealityConfig
	httpClient *http.Client

	statusMu              sync.RWMutex
	connected             bool
	connectedAt           time.Time
	lastHandshakeDuration time.Duration
	lastHandshakeAt       time.Time
	lastCommunicationAt   time.Time
	lastDisconnectedAt    time.Time
	lastError             string
}

type StatusSnapshot struct {
	Connected             bool
	ConnectedAt           time.Time
	LastHandshakeDuration time.Duration
	LastHandshakeAt       time.Time
	LastCommunicationAt   time.Time
	LastDisconnectedAt    time.Time
	LastError             string
}

func NewClient(hubURL, token, localAddr, localToken string) *Client {
	return &Client{
		hubURL:     hubURL,
		token:      token,
		localAddr:  localAddr,
		localToken: localToken,
		done:       make(chan struct{}),
		httpClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
				ForceAttemptHTTP2:   true,
			},
			Timeout: 0, // no timeout for streaming
		},
	}
}

func (c *Client) Start() {
	c.wg.Add(1)
	defer c.wg.Done()

	backoff := time.Duration(0)
	const (
		initialBackoff = 3 * time.Second
		maxBackoff     = 60 * time.Second
	)

	for {
		select {
		case <-c.done:
			return
		default:
		}
		if err := c.runOnce(); err != nil {
			if backoff == 0 {
				backoff = initialBackoff
			} else {
				backoff = backoff * 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			jitter := time.Duration(rand.Int63n(int64(backoff) / 3))
			delay := backoff + jitter
			log.Printf("[Tunnel] error: %v, reconnecting in %v...", err, delay.Round(time.Millisecond))
			select {
			case <-c.done:
				return
			case <-time.After(delay):
			}
		} else {
			backoff = 0
		}
	}
}

func (c *Client) Stop() {
	close(c.done)
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

func (c *Client) Status() StatusSnapshot {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return StatusSnapshot{
		Connected:             c.connected,
		ConnectedAt:           c.connectedAt,
		LastHandshakeDuration: c.lastHandshakeDuration,
		LastHandshakeAt:       c.lastHandshakeAt,
		LastCommunicationAt:   c.lastCommunicationAt,
		LastDisconnectedAt:    c.lastDisconnectedAt,
		LastError:             c.lastError,
	}
}

func (c *Client) updateStatusConnected(handshakeDuration time.Duration) {
	now := time.Now()
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	c.connected = true
	c.connectedAt = now
	c.lastHandshakeDuration = handshakeDuration
	c.lastHandshakeAt = now
	c.lastError = ""
}

func (c *Client) updateLastCommunication() {
	c.statusMu.Lock()
	c.lastCommunicationAt = time.Now()
	c.statusMu.Unlock()
}

func (c *Client) updateStatusDisconnected(err error) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	c.connected = false
	c.lastDisconnectedAt = time.Now()
	if err != nil {
		c.lastError = err.Error()
	}
}

func (c *Client) runOnce() error {
	start := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	defer cancel()

	// Open server-streaming connection to hub (Register).
	// Hub streams app→gw messages down this response body.
	pr, pw := io.Pipe()
	registerURL := c.hubURL + "/api.v1.TunnelService/Register"
	req, err := http.NewRequestWithContext(ctx, "POST", registerURL, pr)
	if err != nil {
		pw.Close()
		return fmt.Errorf("build register request: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/grpc-web+proto")

	resp, err := c.getHTTPClient().Do(req)
	if err != nil {
		pw.Close()
		return fmt.Errorf("register stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		pw.Close()
		return fmt.Errorf("register status: %d", resp.StatusCode)
	}

	log.Printf("[Tunnel] gRPC-Web stream connected to hub %s", c.hubURL)

	// Connect to local agentgw WebSocket
	localConn, err := c.dialLocal()
	if err != nil {
		resp.Body.Close()
		pw.Close()
		return fmt.Errorf("dial local: %w", err)
	}

	handshakeDuration := time.Since(start)
	c.updateStatusConnected(handshakeDuration)
	c.updateLastCommunication()
	log.Printf("[Tunnel] bridged to local %s", c.localAddr)

	errCh := make(chan error, 2)

	// hub→local: read gRPC-Web frames from streaming response, forward to local WS
	go func() {
		defer resp.Body.Close()
		r := bufio.NewReader(resp.Body)
		for {
			payload, err := decodeFrame(r)
			if err != nil {
				if err == io.EOF || ctx.Err() != nil {
					errCh <- nil
				} else {
					errCh <- fmt.Errorf("hub→local read: %w", err)
				}
				return
			}
			log.Printf("[Tunnel] hub→local len=%d", len(payload))
			if err := localConn.WriteMessage(websocket.TextMessage, payload); err != nil {
				errCh <- fmt.Errorf("local write: %w", err)
				return
			}
			c.updateLastCommunication()
		}
	}()

	// local→hub: read from local WS, send as gRPC-Web frames via the request body pipe
	go func() {
		defer pw.Close()
		for {
			_, data, err := localConn.ReadMessage()
			if err != nil {
				errCh <- fmt.Errorf("local→hub read: %w", err)
				return
			}
			log.Printf("[Tunnel] local→hub len=%d", len(data))
			frame := encodeFrame(data)
			if _, err := pw.Write(frame); err != nil {
				errCh <- fmt.Errorf("hub write: %w", err)
				return
			}
			c.updateLastCommunication()
		}
	}()

	select {
	case err = <-errCh:
	case <-c.done:
		err = nil
	}

	cancel()
	localConn.Close()
	resp.Body.Close()
	pw.Close()
	log.Printf("[Tunnel] disconnected from hub")
	if err != nil {
		c.updateStatusDisconnected(err)
	} else {
		c.updateStatusDisconnected(nil)
	}
	return err
}

func (c *Client) getHTTPClient() *http.Client {
	c.mu.Lock()
	rcfg := c.reality
	c.mu.Unlock()

	if rcfg != nil {
		return c.realityHTTPClient(rcfg)
	}
	return c.httpClient
}

func (c *Client) setHeaders(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	c.mu.Lock()
	rcfg := c.reality
	c.mu.Unlock()
	if rcfg != nil {
		req.Header.Set("Origin", "https://"+rcfg.ServerName)
	} else {
		req.Header.Set("Origin", "https://"+hostFromURL(c.hubURL))
	}
}

func (c *Client) dialLocal() (*websocket.Conn, error) {
	url := "ws://" + c.localAddr + "/ws?token=" + c.localToken
	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 5 * time.Second
	wsConn, _, err := dialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	return wsConn, nil
}

func encodeFrame(payload []byte) []byte {
	frame := make([]byte, grpcFrameHeader+len(payload))
	frame[0] = 0x00 // data frame
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	return frame
}

func decodeFrame(r *bufio.Reader) ([]byte, error) {
	hdr := make([]byte, grpcFrameHeader)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	if hdr[0] == 0x80 {
		// trailers frame — stream ended
		return nil, io.EOF
	}
	length := binary.BigEndian.Uint32(hdr[1:5])
	if length > maxFrameSize {
		return nil, fmt.Errorf("frame too large: %d", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func hostFromURL(rawURL string) string {
	for i := 0; i < len(rawURL); i++ {
		if rawURL[i] == '/' && i+1 < len(rawURL) && rawURL[i+1] == '/' {
			rest := rawURL[i+2:]
			for j := 0; j < len(rest); j++ {
				if rest[j] == '/' || rest[j] == '?' {
					return rest[:j]
				}
			}
			return rest
		}
	}
	return rawURL
}

// sendToHub sends a single message to hub via POST (used for tunnel→app forwarding).
func (c *Client) sendToHub(ctx context.Context, data []byte) error {
	sendURL := c.hubURL + "/api.v1.TunnelService/Send"
	req, err := http.NewRequestWithContext(ctx, "POST", sendURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	resp, err := c.getHTTPClient().Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("send status: %d", resp.StatusCode)
	}
	return nil
}

// realityHTTPClient returns an HTTP client that dials through REALITY+uTLS.
func (c *Client) realityHTTPClient(cfg *RealityConfig) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialReality(ctx, addr, cfg)
			},
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		},
		Timeout: 0,
	}
}
