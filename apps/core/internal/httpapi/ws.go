package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

const (
	wsPing       = 30 * time.Second
	wsWrite      = 10 * time.Second
	wsBuffer     = 256
	backlogBatch = 500
)

// ws streams events: first everything stored after ?since=<seq>, then live
// events. Ephemeral events (seq 0) are only sent live. A client that falls
// behind is disconnected and reconnects with the last seq it saw.
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	opts := &websocket.AcceptOptions{}
	if u, err := url.Parse(s.cfg.PublicURL); err == nil {
		opts.OriginPatterns = []string{u.Host}
	}
	if s.cfg.Dev {
		opts.OriginPatterns = append(opts.OriginPatterns, "localhost:*", "127.0.0.1:*")
	}
	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := conn.CloseRead(r.Context())

	// Subscribe before reading the backlog so nothing falls in between; live
	// events already covered by the backlog are skipped by seq.
	live, stop := s.log.Bus.Subscribe(wsBuffer)
	defer stop()

	last := since
	for {
		evs, err := s.log.Store.EventsSince(ctx, last, backlogBatch)
		if err != nil {
			conn.Close(websocket.StatusInternalError, "backlog")
			return
		}
		for _, ev := range evs {
			if err := send(ctx, conn, ev); err != nil {
				return
			}
			last = ev.Seq
		}
		if len(evs) < backlogBatch {
			break
		}
	}

	ping := time.NewTicker(wsPing)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-live:
			if !ok {
				conn.Close(websocket.StatusTryAgainLater, "too slow, reconnect with since")
				return
			}
			if ev.Seq != 0 && ev.Seq <= last {
				continue
			}
			if err := send(ctx, conn, ev); err != nil {
				return
			}
			if ev.Seq != 0 {
				last = ev.Seq
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, wsWrite)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func send(ctx context.Context, conn *websocket.Conn, ev event.Event) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, wsWrite)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, raw)
}
