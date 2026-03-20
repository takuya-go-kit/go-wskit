package wskit

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	DefaultBroadcastBuf   = 256
	DefaultRegisterBuf    = 64
	DefaultChannelTimeout = 5 * time.Second
)

type broadcastItem struct {
	data []byte
}

// HubOption configures a Hub.
type HubOption func(*Hub)

// WithRedis configures Redis Pub/Sub for multi-instance broadcast. If client is nil, Redis is disabled.
func WithRedis(client *redis.Client, channel string) HubOption {
	return func(h *Hub) {
		h.redisClient = client
		h.redisChannel = channel
	}
}

// WithBroadcastBuf sets the broadcast channel buffer size.
func WithBroadcastBuf(n int) HubOption {
	return func(h *Hub) {
		h.broadcastBuf = n
	}
}

// WithRegisterBuf sets the register/unregister channel buffer size.
func WithRegisterBuf(n int) HubOption {
	return func(h *Hub) {
		h.registerBuf = n
	}
}

// WithChannelTimeout sets the timeout for Register, Unregister, and Broadcast operations.
func WithChannelTimeout(d time.Duration) HubOption {
	return func(h *Hub) {
		h.channelTimeout = d
	}
}

// WithOnTimeout sets a callback when a channel operation times out (e.g. for logging).
func WithOnTimeout(fn func(op string)) HubOption {
	return func(h *Hub) {
		h.onTimeout = fn
	}
}

// OnConnect is called when a client is registered; use it to send a welcome message.
type OnConnect func(client *Client)

// WithOnConnect sets the callback invoked when a client registers.
func WithOnConnect(fn OnConnect) HubOption {
	return func(h *Hub) {
		h.onConnect = fn
	}
}

// Hub is the central dispatcher for WebSocket clients. Run one goroutine with Run(ctx).
type Hub struct {
	clients        map[*Client]bool
	broadcast      chan broadcastItem
	register       chan *Client
	unregister     chan *Client
	done           chan struct{}
	doneOnce       sync.Once
	clientCount    int64
	redisClient    *redis.Client
	redisChannel   string
	onTimeout      func(op string)
	onConnect      OnConnect
	broadcastBuf   int
	registerBuf    int
	channelTimeout time.Duration
}

// NewHub creates a Hub with the given options.
func NewHub(opts ...HubOption) *Hub {
	h := &Hub{
		clients:        make(map[*Client]bool),
		broadcastBuf:   DefaultBroadcastBuf,
		registerBuf:    DefaultRegisterBuf,
		channelTimeout: DefaultChannelTimeout,
	}
	for _, opt := range opts {
		opt(h)
	}
	h.broadcast = make(chan broadcastItem, h.broadcastBuf)
	h.register = make(chan *Client, h.registerBuf)
	h.unregister = make(chan *Client, h.registerBuf)
	h.done = make(chan struct{})
	return h
}

func (h *Hub) closeDone() {
	h.doneOnce.Do(func() { close(h.done) })
}

// Run runs the hub loop until ctx is cancelled. Closes all clients on exit.
func (h *Hub) Run(ctx context.Context) {
	defer h.closeDone()
	for {
		select {
		case <-ctx.Done():
			for client := range h.clients {
				delete(h.clients, client)
				client.sendClosed.Store(true)
				close(client.send)
				atomic.AddInt64(&h.clientCount, -1)
			}
			return
		case client := <-h.register:
			h.clients[client] = true
			atomic.AddInt64(&h.clientCount, 1)
			if h.onConnect != nil {
				h.onConnect(client)
			}
		case client := <-h.unregister:
			h.unregisterClient(client)
		case item := <-h.broadcast:
			h.broadcastToClients(item)
		}
	}
}

func (h *Hub) unregisterClient(client *Client) {
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		client.sendClosed.Store(true)
		close(client.send)
		atomic.AddInt64(&h.clientCount, -1)
	}
}

func (h *Hub) broadcastToClients(item broadcastItem) {
	for client := range h.clients {
		select {
		case client.send <- item.data:
		default:
		}
	}
}

func (h *Hub) sendWithTimeout(ch chan<- *Client, client *Client, op string) {
	t := time.NewTimer(h.channelTimeout)
	defer t.Stop()
	select {
	case ch <- client:
	case <-h.done:
	case <-t.C:
		if h.onTimeout != nil {
			h.onTimeout(op)
		}
	}
}

func (h *Hub) broadcastWithTimeout(data []byte) {
	t := time.NewTimer(h.channelTimeout)
	defer t.Stop()
	select {
	case h.broadcast <- broadcastItem{data: data}:
	case <-h.done:
	case <-t.C:
		if h.onTimeout != nil {
			h.onTimeout("broadcast")
		}
	}
}

// Register adds the client to the hub. Non-blocking with timeout.
func (h *Hub) Register(client *Client) {
	h.sendWithTimeout(h.register, client, "register")
}

// Unregister removes the client from the hub. Non-blocking with timeout.
func (h *Hub) Unregister(client *Client) {
	h.sendWithTimeout(h.unregister, client, "unregister")
}

// Broadcast sends data to all connected clients. Non-blocking with timeout.
func (h *Hub) Broadcast(data []byte) {
	h.broadcastWithTimeout(data)
}

// BroadcastEvent marshals event as JSON and broadcasts it. If Redis is configured, publishes to Redis first; on failure falls back to local Broadcast. Returns an error only when JSON marshaling fails.
func (h *Hub) BroadcastEvent(ctx context.Context, event any) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if h.redisClient != nil && h.redisChannel != "" {
		pubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := h.redisClient.Publish(pubCtx, h.redisChannel, data).Err()
		cancel()
		if err == nil {
			return nil
		}
	}
	h.Broadcast(data)
	return nil
}

// SubscribeToRedis subscribes to the hub's Redis channel and broadcasts received messages to all clients. Run in a goroutine; it returns when ctx is cancelled.
func (h *Hub) SubscribeToRedis(ctx context.Context) {
	if h.redisClient == nil || h.redisChannel == "" {
		return
	}
	pubsub := h.redisClient.Subscribe(ctx, h.redisChannel)
	defer func() { _ = pubsub.Close() }()
	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			h.Broadcast([]byte(msg.Payload))
		}
	}
}

// ClientCount returns the number of registered clients.
func (h *Hub) ClientCount() int {
	return int(atomic.LoadInt64(&h.clientCount))
}
