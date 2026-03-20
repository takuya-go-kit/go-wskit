package wskit

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	DefaultWriteWait      = 10 * time.Second
	DefaultPingInterval   = 30 * time.Second
	DefaultMaxMessageSize = 512
	DefaultSendBufSize    = 256
)

// ClientOption configures a Client.
type ClientOption func(*ClientConfig)

type ClientConfig struct {
	WriteWait      time.Duration
	PingInterval   time.Duration
	MaxMessageSize int64
	SendBufSize    int
}

func applyClientOptions(opts []ClientOption) ClientConfig {
	cfg := ClientConfig{
		WriteWait:      DefaultWriteWait,
		PingInterval:   DefaultPingInterval,
		MaxMessageSize: DefaultMaxMessageSize,
		SendBufSize:    DefaultSendBufSize,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithWriteWait sets the timeout for writing a message or ping.
func WithWriteWait(d time.Duration) ClientOption {
	return func(c *ClientConfig) {
		c.WriteWait = d
	}
}

// WithPingInterval sets the interval between ping frames.
func WithPingInterval(d time.Duration) ClientOption {
	return func(c *ClientConfig) {
		c.PingInterval = d
	}
}

// WithMaxMessageSize sets the maximum size of a single incoming message.
func WithMaxMessageSize(n int64) ClientOption {
	return func(c *ClientConfig) {
		c.MaxMessageSize = n
	}
}

// WithSendBufSize sets the send channel buffer size.
func WithSendBufSize(n int) ClientOption {
	return func(c *ClientConfig) {
		c.SendBufSize = n
	}
}

// Client represents a single WebSocket connection attached to a Hub.
type Client struct {
	hub        *Hub
	conn       *websocket.Conn
	send       chan []byte
	ctx        context.Context
	closeOnce  sync.Once
	writeWait  time.Duration
	pingInt    time.Duration
	sendClosed atomic.Bool
}

// NewClient creates a client for the given hub and connection. Call Register on the hub, then run ReadPump and WritePump in separate goroutines.
func NewClient(hub *Hub, conn *websocket.Conn, ctx context.Context, opts ...ClientOption) *Client {
	cfg := applyClientOptions(opts)
	if cfg.SendBufSize <= 0 {
		cfg.SendBufSize = DefaultSendBufSize
	}
	c := &Client{
		hub:       hub,
		conn:      conn,
		send:      make(chan []byte, cfg.SendBufSize),
		ctx:       ctx,
		writeWait: cfg.WriteWait,
		pingInt:   cfg.PingInterval,
	}
	if c.pingInt <= 0 {
		c.pingInt = DefaultPingInterval
	}
	if c.writeWait <= 0 {
		c.writeWait = DefaultWriteWait
	}
	conn.SetReadLimit(cfg.MaxMessageSize)
	return c
}

func (c *Client) closeConn() {
	c.closeOnce.Do(func() {
		_ = c.conn.Close(websocket.StatusNormalClosure, "")
	})
}

// Send enqueues data for writing. Non-blocking; returns false if the send buffer is full or the client is unregistered.
func (c *Client) Send(data []byte) (sent bool) {
	if c.sendClosed.Load() {
		return false
	}
	defer func() {
		if recover() != nil {
			sent = false
		}
	}()
	select {
	case c.send <- data:
		return true
	default:
		return false
	}
}

// SendErr is like Send but returns ErrHubStopped when the client is unregistered.
func (c *Client) SendErr(data []byte) error {
	if !c.Send(data) {
		return ErrHubStopped
	}
	return nil
}

// ReadPump reads messages from the connection until it closes or errors. On exit it unregisters the client and closes the connection. Run in a goroutine.
func (c *Client) ReadPump() {
	defer func() {
		c.hub.Unregister(c)
		c.closeConn()
	}()
	for {
		_, _, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}
	}
}

// WritePump writes messages from the send channel and sends ping frames at the configured interval. Run in a goroutine.
func (c *Client) WritePump() {
	ticker := time.NewTicker(c.pingInt)
	defer func() {
		ticker.Stop()
		c.closeConn()
	}()

	for {
		select {
		case message, ok := <-c.send:
			ctx, cancel := context.WithTimeout(c.ctx, c.writeWait)
			if !ok {
				cancel()
				c.closeConn()
				return
			}
			w, err := c.conn.Writer(ctx, websocket.MessageText)
			if err != nil {
				cancel()
				return
			}
			if _, err := w.Write(message); err != nil {
				cancel()
				return
			}
			if err := w.Close(); err != nil {
				cancel()
				return
			}
			cancel()
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(c.ctx, c.writeWait)
			if err := c.conn.Ping(ctx); err != nil {
				cancel()
				return
			}
			cancel()
		case <-c.ctx.Done():
			return
		}
	}
}
