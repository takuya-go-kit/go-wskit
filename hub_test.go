package wskit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func startTestHub(t *testing.T, opts ...HubOption) (*Hub, context.CancelFunc) {
	t.Helper()
	hub := NewHub(opts...)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel() })
	go hub.Run(ctx)
	return hub, cancel
}

func startTestServer(t *testing.T, hub *Hub, clientOpts ...ClientOption) *httptest.Server {
	t.Helper()
	connCtx, connCancel := context.WithCancel(context.Background())
	t.Cleanup(connCancel)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, err := Accept(connCtx, w, r, hub, nil, clientOpts...)
		if err != nil {
			return
		}
		go client.ReadPump()
		go client.WritePump()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func dialWS(t *testing.T, srvURL string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+srvURL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func readJSON(t *testing.T, conn *websocket.Conn, dst any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func waitForClients(t *testing.T, hub *Hub, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if hub.ClientCount() == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("ClientCount = %d, want %d (timeout)", hub.ClientCount(), want)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestHub_RunExitsOnCancel(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		hub.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancel")
	}
}

func TestHub_ClientCount(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	if hub.ClientCount() != 0 {
		t.Errorf("ClientCount() = %d, want 0", hub.ClientCount())
	}
}

func TestNewEvent(t *testing.T) {
	t.Parallel()
	ev := NewEvent("test", map[string]string{"a": "b"})
	if ev.Type != "test" {
		t.Errorf("Type = %q, want test", ev.Type)
	}
	if ev.Payload == nil {
		t.Error("Payload is nil")
	}
	if ev.Timestamp.IsZero() {
		t.Error("Timestamp is zero")
	}
}

func TestHub_OnConnect(t *testing.T) {
	t.Parallel()
	hub, _ := startTestHub(t, WithOnConnect(func(c *Client) {
		data, _ := json.Marshal(NewEvent("welcome", nil))
		c.Send(data)
	}))
	srv := startTestServer(t, hub)
	conn := dialWS(t, srv.URL)

	var ev Event
	readJSON(t, conn, &ev)
	if ev.Type != "welcome" {
		t.Fatalf("expected welcome, got %q", ev.Type)
	}
}

func TestHub_Broadcast(t *testing.T) {
	t.Parallel()
	hub, _ := startTestHub(t)
	srv := startTestServer(t, hub)
	conn := dialWS(t, srv.URL)
	waitForClients(t, hub, 1)

	hub.Broadcast([]byte(`{"type":"ping"}`))

	var ev Event
	readJSON(t, conn, &ev)
	if ev.Type != "ping" {
		t.Fatalf("expected ping, got %q", ev.Type)
	}
}

func TestHub_BroadcastEvent(t *testing.T) {
	t.Parallel()
	hub, _ := startTestHub(t)
	srv := startTestServer(t, hub)
	conn := dialWS(t, srv.URL)
	waitForClients(t, hub, 1)

	if err := hub.BroadcastEvent(context.Background(), NewEvent("notify", "hello")); err != nil {
		t.Fatalf("BroadcastEvent: %v", err)
	}

	var ev Event
	readJSON(t, conn, &ev)
	if ev.Type != "notify" {
		t.Fatalf("expected notify, got %q", ev.Type)
	}
}

func TestHub_BroadcastJSON(t *testing.T) {
	t.Parallel()
	hub, _ := startTestHub(t)
	srv := startTestServer(t, hub)
	conn := dialWS(t, srv.URL)
	waitForClients(t, hub, 1)

	if err := hub.BroadcastJSON(context.Background(), "update", map[string]int{"v": 42}); err != nil {
		t.Fatalf("BroadcastJSON: %v", err)
	}

	var ev Event
	readJSON(t, conn, &ev)
	if ev.Type != "update" {
		t.Fatalf("expected update, got %q", ev.Type)
	}
}

func TestHub_BroadcastEvent_MarshalError(t *testing.T) {
	t.Parallel()
	hub, _ := startTestHub(t)
	err := hub.BroadcastEvent(context.Background(), make(chan int))
	if err == nil {
		t.Fatal("expected marshal error, got nil")
	}
}

func TestHub_MultipleClients(t *testing.T) {
	t.Parallel()
	hub, _ := startTestHub(t)
	srv := startTestServer(t, hub)

	conns := make([]*websocket.Conn, 3)
	for i := range conns {
		conns[i] = dialWS(t, srv.URL)
	}
	waitForClients(t, hub, 3)

	hub.Broadcast([]byte(`{"type":"all"}`))
	for i, conn := range conns {
		var ev Event
		readJSON(t, conn, &ev)
		if ev.Type != "all" {
			t.Errorf("client %d: expected all, got %q", i, ev.Type)
		}
	}
}

func TestHub_ClientDisconnect(t *testing.T) {
	t.Parallel()
	hub, _ := startTestHub(t)
	srv := startTestServer(t, hub)
	conn := dialWS(t, srv.URL)
	waitForClients(t, hub, 1)

	conn.Close(websocket.StatusNormalClosure, "bye")
	waitForClients(t, hub, 0)
}

func TestHub_ShutdownClosesClients(t *testing.T) {
	t.Parallel()
	hub, cancel := startTestHub(t)
	srv := startTestServer(t, hub)
	conn := dialWS(t, srv.URL)
	waitForClients(t, hub, 1)

	cancel()
	time.Sleep(100 * time.Millisecond)

	ctx, c := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer c()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("expected error after shutdown, got nil")
	}
}

func TestHub_OnTimeout(t *testing.T) {
	t.Parallel()
	var called atomic.Int32
	hub := NewHub(
		WithChannelTimeout(1*time.Nanosecond),
		WithRegisterBuf(0),
		WithOnTimeout(func(op string) {
			called.Add(1)
		}),
	)

	for range 50 {
		hub.Register(&Client{send: make(chan []byte, 1)})
	}
	time.Sleep(50 * time.Millisecond)
	if called.Load() > 0 {
		t.Logf("onTimeout called %d times", called.Load())
	}
}

func TestClient_Send_OK(t *testing.T) {
	t.Parallel()
	c := &Client{send: make(chan []byte, 1)}
	if !c.Send([]byte("hello")) {
		t.Fatal("Send should return true")
	}
}

func TestClient_Send_BufferFull(t *testing.T) {
	t.Parallel()
	c := &Client{send: make(chan []byte)}
	if c.Send([]byte("hello")) {
		t.Fatal("Send should return false on full buffer")
	}
}

func TestClient_Send_AfterClose(t *testing.T) {
	t.Parallel()
	c := &Client{send: make(chan []byte, 1)}
	c.sendClosed.Store(true)
	close(c.send)
	if c.Send([]byte("hello")) {
		t.Fatal("Send should return false after close")
	}
}

func TestClient_SendErr_Closed(t *testing.T) {
	t.Parallel()
	c := &Client{send: make(chan []byte, 1)}
	c.sendClosed.Store(true)
	close(c.send)
	err := c.SendErr([]byte("hello"))
	if err != ErrHubStopped {
		t.Fatalf("SendErr = %v, want ErrHubStopped", err)
	}
}

func TestClient_SendErr_OK(t *testing.T) {
	t.Parallel()
	c := &Client{send: make(chan []byte, 1)}
	if err := c.SendErr([]byte("hello")); err != nil {
		t.Fatalf("SendErr = %v, want nil", err)
	}
}

func TestClient_Options(t *testing.T) {
	t.Parallel()
	cfg := applyClientOptions([]ClientOption{
		WithWriteWait(5 * time.Second),
		WithPingInterval(15 * time.Second),
		WithMaxMessageSize(1024),
		WithSendBufSize(64),
	})
	if cfg.WriteWait != 5*time.Second {
		t.Errorf("WriteWait = %v, want 5s", cfg.WriteWait)
	}
	if cfg.PingInterval != 15*time.Second {
		t.Errorf("PingInterval = %v, want 15s", cfg.PingInterval)
	}
	if cfg.MaxMessageSize != 1024 {
		t.Errorf("MaxMessageSize = %d, want 1024", cfg.MaxMessageSize)
	}
	if cfg.SendBufSize != 64 {
		t.Errorf("SendBufSize = %d, want 64", cfg.SendBufSize)
	}
}

func TestHub_Options(t *testing.T) {
	t.Parallel()
	hub := NewHub(
		WithBroadcastBuf(512),
		WithRegisterBuf(128),
		WithChannelTimeout(10*time.Second),
	)
	if hub.broadcastBuf != 512 {
		t.Errorf("broadcastBuf = %d, want 512", hub.broadcastBuf)
	}
	if hub.registerBuf != 128 {
		t.Errorf("registerBuf = %d, want 128", hub.registerBuf)
	}
	if hub.channelTimeout != 10*time.Second {
		t.Errorf("channelTimeout = %v, want 10s", hub.channelTimeout)
	}
}

func TestHub_ConcurrentBroadcast(t *testing.T) {
	t.Parallel()
	hub, _ := startTestHub(t)
	srv := startTestServer(t, hub)

	conns := make([]*websocket.Conn, 3)
	for i := range conns {
		conns[i] = dialWS(t, srv.URL)
	}
	waitForClients(t, hub, 3)

	var wg sync.WaitGroup
	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			hub.BroadcastJSON(context.Background(), "msg", idx)
		}(i)
	}
	wg.Wait()

	for _, conn := range conns {
		for range 5 {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				t.Fatalf("read: %v", err)
			}
		}
	}
}

func TestAccept(t *testing.T) {
	t.Parallel()
	hub, _ := startTestHub(t)
	srv := startTestServer(t, hub, WithPingInterval(10*time.Second))

	conn := dialWS(t, srv.URL)
	_ = conn
	waitForClients(t, hub, 1)
}

func TestHub_SubscribeToRedis_NilClient(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	hub.SubscribeToRedis(context.Background())
}

func TestHub_BroadcastEvent_NoRedis_Fallback(t *testing.T) {
	t.Parallel()
	hub, _ := startTestHub(t)
	srv := startTestServer(t, hub)
	conn := dialWS(t, srv.URL)
	waitForClients(t, hub, 1)

	if err := hub.BroadcastEvent(context.Background(), NewEvent("local", nil)); err != nil {
		t.Fatalf("BroadcastEvent: %v", err)
	}

	var ev Event
	readJSON(t, conn, &ev)
	if ev.Type != "local" {
		t.Fatalf("expected local, got %q", ev.Type)
	}
}
