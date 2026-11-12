package tunnel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestCancellationDuringWritePreservesConnection(t *testing.T) {
	peer := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		peer <- ws
	}))
	defer srv.Close()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	ws, _, err := websocket.Dial(ctx, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	remote := <-peer
	defer remote.CloseNow()
	remote.SetReadLimit(16 << 20)
	c := &Client{ws: ws}
	request, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.write(request, rframe{T: "blob.put", Blob: strings.Repeat("x", 8<<20)}) }()
	// Keep the peer from reading until the write has filled the socket.
	select {
	case err := <-done:
		t.Fatalf("write finished before peer read: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	if _, _, err := remote.Read(ctx); err != nil {
		t.Fatalf("canceling request interrupted its frame: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	go func() { done <- c.write(ctx, rframe{T: "get", Name: "roster"}) }()
	if _, _, err := remote.Read(ctx); err != nil {
		t.Fatalf("next frame failed: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
