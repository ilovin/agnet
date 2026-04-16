package tunnel

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Client maintains an outbound WebSocket to TunnelHub and bridges traffic
// to the local agentgw WebSocket server.
type Client struct {
	hubURL     string
	token      string
	localAddr  string
	localToken string
	mu         sync.Mutex
	conn       *websocket.Conn
	done       chan struct{}
	wg         sync.WaitGroup
}

// NewClient creates a tunnel client.
// hubURL: wss://hub.corp.com/tunnel/register?userId=alice
// token:  shared secret or JWT for tunnel auth
// localAddr: localhost:8383 (local agentgw ws address)
// localToken: auth token for local agentgw /ws endpoint
func NewClient(hubURL, token, localAddr, localToken string) *Client {
	return &Client{
		hubURL:     hubURL,
		token:      token,
		localAddr:  localAddr,
		localToken: localToken,
		done:       make(chan struct{}),
	}
}

// Start connects to the hub and begins forwarding. Blocks until Stop is called.
func (c *Client) Start() {
	c.wg.Add(1)
	defer c.wg.Done()
	for {
		select {
		case <-c.done:
			return
		default:
		}
		if err := c.runOnce(); err != nil {
			log.Printf("[Tunnel] error: %v, reconnecting in 5s...", err)
		}
		select {
		case <-c.done:
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// Stop shuts down the tunnel client.
func (c *Client) Stop() {
	close(c.done)
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close()
	}
	c.mu.Unlock()
	c.wg.Wait()
}

func (c *Client) runOnce() error {
	dialer := websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second

	headers := http.Header{}
	if c.token != "" {
		headers.Set("Authorization", "Bearer "+c.token)
	}

	url := c.hubURL
	if !strings.Contains(url, "token=") && c.token != "" {
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}
		url = url + sep + "token=" + c.token
	}

	conn, _, err := dialer.Dial(url, headers)
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	log.Printf("[Tunnel] connected to hub %s", c.hubURL)

	localConn, err := c.dialLocal()
	if err != nil {
		conn.Close()
		return err
	}

	log.Printf("[Tunnel] bridged to local %s", c.localAddr)

	errCh := make(chan error, 2)
	go func() {
		for {
			t, data, err := conn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if err := localConn.WriteMessage(t, data); err != nil {
				errCh <- err
				return
			}
		}
	}()
	go func() {
		for {
			t, data, err := localConn.ReadMessage()
			if err != nil {
				errCh <- err
				return
			}
			if err := conn.WriteMessage(t, data); err != nil {
				errCh <- err
				return
			}
		}
	}()

	select {
	case err = <-errCh:
	case <-c.done:
		err = nil
	}

	conn.Close()
	localConn.Close()
	log.Printf("[Tunnel] disconnected from hub")
	return err
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
