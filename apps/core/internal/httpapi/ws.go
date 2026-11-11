package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
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
func (s *Server) ws(w http.ResponseWriter, r *http.Request) { s.stream(w, r, nil) }

// deviceWS is the event stream for paired machines (`sous chat`): only the
// conversation, runs starting and ending, and approvals. Terminal output of
// installs, logins and shells, file and config changes never reach a
// device token.
func (s *Server) deviceWS(w http.ResponseWriter, r *http.Request) { s.stream(w, r, deviceEvent) }

func deviceEvent(t event.Type) bool {
	switch t {
	case event.RunQueued, event.RunStarted, event.RunEnded:
		return true
	}
	for _, p := range []string{"thread.", "message.", "approval.", "trigger."} {
		if strings.HasPrefix(string(t), p) {
			return true
		}
	}
	return false
}

var pageConns, deviceConns atomic.Int64

// stream sends the events allow accepts (all of them when it is nil).
func (s *Server) stream(w http.ResponseWriter, r *http.Request, allow func(event.Type) bool) {
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
	counter := &pageConns
	edge := "httpapi>ui"
	if allow != nil {
		counter = &deviceConns
		edge = "httpapi>cli"
	}
	counter.Add(1)
	defer counter.Add(-1)
	record := func(ev event.Event) {
		sysmap.Count(edge, sysmap.Refs{ThreadID: ev.ThreadID, TaskID: ev.TaskID, RunID: ev.RunID}, string(ev.Type))
	}
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
			last = ev.Seq
			if allow != nil && !allow(ev.Type) {
				continue
			}
			if err := send(ctx, conn, ev); err != nil {
				return
			}
			record(ev)
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
			if allow != nil && !allow(ev.Type) {
				if ev.Seq != 0 {
					last = ev.Seq
				}
				continue
			}
			if err := send(ctx, conn, ev); err != nil {
				return
			}
			record(ev)
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
