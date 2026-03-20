package wskit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func benchHub(b *testing.B) (*Hub, context.CancelFunc) {
	b.Helper()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	go hub.Run(ctx)
	b.Cleanup(func() { cancel() })
	return hub, cancel
}

func benchServer(b *testing.B, hub *Hub) *httptest.Server {
	b.Helper()
	connCtx, connCancel := context.WithCancel(context.Background())
	b.Cleanup(connCancel)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, err := Accept(connCtx, w, r, hub, nil)
		if err != nil {
			return
		}
		go client.ReadPump()
		go client.WritePump()
	}))
	b.Cleanup(srv.Close)
	return srv
}

func benchDial(b *testing.B, srvURL string) *websocket.Conn {
	b.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+srvURL[4:], nil)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	b.Cleanup(func() { conn.CloseNow() })
	return conn
}

func benchWaitClients(b *testing.B, hub *Hub, want int) {
	b.Helper()
	deadline := time.After(5 * time.Second)
	for hub.ClientCount() != want {
		select {
		case <-deadline:
			b.Fatalf("ClientCount = %d, want %d", hub.ClientCount(), want)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func BenchmarkHub_Broadcast_1Client(b *testing.B) {
	hub, _ := benchHub(b)
	srv := benchServer(b, hub)
	conn := benchDial(b, srv.URL)
	benchWaitClients(b, hub, 1)

	msg := []byte(`{"type":"bench"}`)

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)

	b.ResetTimer()
	for range b.N {
		hub.Broadcast(msg)
	}
	b.StopTimer()
}

func BenchmarkHub_Broadcast_10Clients(b *testing.B) {
	hub, _ := benchHub(b)
	srv := benchServer(b, hub)

	for range 10 {
		conn := benchDial(b, srv.URL)
		go func() {
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _, err := conn.Read(ctx)
				cancel()
				if err != nil {
					return
				}
			}
		}()
	}
	benchWaitClients(b, hub, 10)

	msg := []byte(`{"type":"bench"}`)
	b.ResetTimer()
	for range b.N {
		hub.Broadcast(msg)
	}
	b.StopTimer()
}

func BenchmarkHub_Broadcast_100Clients(b *testing.B) {
	hub, _ := benchHub(b)
	srv := benchServer(b, hub)

	for range 100 {
		conn := benchDial(b, srv.URL)
		go func() {
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, _, err := conn.Read(ctx)
				cancel()
				if err != nil {
					return
				}
			}
		}()
	}
	benchWaitClients(b, hub, 100)

	msg := []byte(`{"type":"bench"}`)
	b.ResetTimer()
	for range b.N {
		hub.Broadcast(msg)
	}
	b.StopTimer()
}

func BenchmarkHub_BroadcastEvent(b *testing.B) {
	hub, _ := benchHub(b)
	srv := benchServer(b, hub)
	conn := benchDial(b, srv.URL)
	benchWaitClients(b, hub, 1)

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)

	ev := NewEvent("bench", nil)
	b.ResetTimer()
	for range b.N {
		hub.BroadcastEvent(context.Background(), ev)
	}
	b.StopTimer()
}

func BenchmarkHub_BroadcastJSON(b *testing.B) {
	hub, _ := benchHub(b)
	srv := benchServer(b, hub)
	conn := benchDial(b, srv.URL)
	benchWaitClients(b, hub, 1)

	go func() {
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(20 * time.Millisecond)

	b.ResetTimer()
	for range b.N {
		hub.BroadcastJSON(context.Background(), "bench", nil)
	}
	b.StopTimer()
}

func BenchmarkNewEvent(b *testing.B) {
	for range b.N {
		NewEvent("bench", map[string]string{"key": "value"})
	}
}

func BenchmarkEvent_Marshal(b *testing.B) {
	ev := NewEvent("bench", map[string]string{"key": "value"})
	b.ResetTimer()
	for range b.N {
		json.Marshal(ev)
	}
}

func BenchmarkClient_Send(b *testing.B) {
	c := &Client{send: make(chan []byte, 4096)}
	data := []byte(`{"type":"bench"}`)
	go func() {
		for range c.send {
		}
	}()
	b.ResetTimer()
	for range b.N {
		c.Send(data)
	}
}

func BenchmarkHub_RegisterUnregister(b *testing.B) {
	hub, _ := benchHub(b)
	b.ResetTimer()
	for range b.N {
		c := &Client{send: make(chan []byte, 1)}
		hub.Register(c)
		hub.Unregister(c)
	}
	b.StopTimer()
}
