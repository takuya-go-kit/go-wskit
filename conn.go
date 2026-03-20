package wskit

import (
	"context"
	"io"
	"net/http"

	"github.com/coder/websocket"
)

// Conn abstracts a WebSocket connection for testing and alternate implementations.
type Conn interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Writer(ctx context.Context, typ websocket.MessageType) (io.WriteCloser, error)
	Close(code websocket.StatusCode, reason string) error
	Ping(ctx context.Context) error
	SetReadLimit(limit int64)
}

// AcceptFunc is the type of websocket.Accept for dependency injection.
type AcceptFunc func(w http.ResponseWriter, r *http.Request, opts *websocket.AcceptOptions) (*websocket.Conn, error)
