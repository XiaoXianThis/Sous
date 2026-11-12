package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/ipc"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sousmcp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/update"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

// ServeIPC answers local clients (`sous chat`, `sous status`, desktop) on
// the daemon's socket until ctx ends. Conversation calls are relayed to the
// Hub with this machine's device token (docs/api/core-daemon-ipc.md).
func (d *Daemon) ServeIPC(ctx context.Context, logs *LogRing) error {
	l, err := ipc.Listen(ipc.Path(d.root))
	if err != nil {
		return err
	}
	go ipc.Serve(ctx, l, func(ctx context.Context, method string, params json.RawMessage, notify func(string, any) error) (any, error) {
		return d.ipcCall(ctx, method, params, notify, logs)
	})
	return nil
}

// hub calls a device endpoint of the brain (sous chat, sous note, sous mcp).
func (d *Daemon) hub(ctx context.Context, method, path string, body, out any) error {
	t, err := d.transport()
	if err != nil {
		return err
	}
	return t.Call(ctx, method, "/api/v1/device"+path, body, out)
}

func (d *Daemon) ipcCall(ctx context.Context, method string, params json.RawMessage, notify func(string, any) error, logs *LogRing) (any, error) {
	switch method {
	case sousmcp.MethodProgress, sousmcp.MethodAsk, sousmcp.MethodResult:
		return d.sousCall(ctx, method, params)
	}
	var p struct {
		Text     string `json:"text"`
		ThreadID string `json:"thread_id"`
		Title    string `json:"title"`
		Content  string `json:"content"`
		Before   int64  `json:"before"`
		Limit    int    `json:"limit"`
		Since    int64  `json:"since"`
		ID       string `json:"id"`
		OptionID string `json:"option_id"`
		Note     string `json:"note"`
		Lines    int    `json:"lines"`
		Version  string `json:"version"`
	}
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}
	switch method {
	case "status":
		return d.Status(), nil
	case "threads.list":
		var out json.RawMessage
		return out, d.hub(ctx, "GET", "/threads", nil, &out)
	case "threads.create":
		var out json.RawMessage
		return out, d.hub(ctx, "POST", "/threads", map[string]string{"title": p.Title}, &out)
	case "messages.list":
		if p.ThreadID == "" {
			return nil, errors.New("thread_id is required")
		}
		q := url.Values{}
		if p.Limit > 0 {
			q.Set("limit", fmt.Sprint(p.Limit))
		}
		if p.Before > 0 {
			q.Set("before", fmt.Sprint(p.Before))
		}
		var out json.RawMessage
		return out, d.hub(ctx, "GET", "/threads/"+url.PathEscape(p.ThreadID)+"/messages?"+q.Encode(), nil, &out)
	case "note.add":
		var out struct {
			NoteID string `json:"note_id"`
		}
		if err := d.hub(ctx, "POST", "/notes", map[string]string{"text": p.Text}, &out); err != nil {
			return nil, err
		}
		return out, nil
	case "chat.send":
		thread := p.ThreadID
		if thread == "" {
			var ts struct {
				Threads []struct{ ID, Kind string }
			}
			if err := d.hub(ctx, "GET", "/threads", nil, &ts); err != nil {
				return nil, err
			}
			for _, t := range ts.Threads {
				if t.Kind == "main" {
					thread = t.ID
				}
			}
		}
		var out struct {
			MessageID string `json:"message_id"`
			Seq       int64  `json:"seq"`
		}
		if err := d.hub(ctx, "POST", "/threads/"+url.PathEscape(thread)+"/messages", map[string]string{"content": p.Content}, &out); err != nil {
			return nil, err
		}
		return map[string]any{"thread_id": thread, "message_id": out.MessageID, "seq": out.Seq}, nil
	case "approvals.list":
		var out json.RawMessage
		return out, d.hub(ctx, "GET", "/approvals?status=pending", nil, &out)
	case "approvals.decide":
		return map[string]bool{"ok": true}, d.hub(ctx, "POST", "/approvals/"+url.PathEscape(p.ID)+"/decide",
			map[string]string{"option_id": p.OptionID, "note": p.Note}, nil)
	case "events.subscribe":
		go d.relayEvents(ctx, p.Since, notify)
		return map[string]bool{"ok": true}, nil
	case "logs.tail":
		return map[string]any{"lines": logs.Tail(p.Lines)}, nil
	case "update.check":
		rel, err := update.Client{}.Release(ctx, "")
		if err != nil {
			return nil, err
		}
		return map[string]any{"current": version.Version, "latest": rel.Tag, "notes_url": rel.HTMLURL, "newer": update.Newer(rel.Tag, version.Version)}, nil
	case "update.apply":
		tag, err := d.Update(ctx, "", p.Version)
		if err != nil {
			return nil, err
		}
		if d.OnUpdated != nil {
			go func() {
				time.Sleep(500 * time.Millisecond) // let the answer reach the caller
				d.OnUpdated(tag)
			}()
		}
		return map[string]any{"ok": true, "version": tag}, nil
	}
	return nil, fmt.Errorf("unknown method %q", method)
}

// relayEvents streams the brain's event stream to a local client as "event"
// notifications, reconnecting until the client goes away.
func (d *Daemon) relayEvents(ctx context.Context, since int64, notify func(string, any) error) {
	backoff := time.Second
	for ctx.Err() == nil {
		t, err := d.transport()
		if err != nil {
			return
		}
		connected := false
		err = t.Stream(ctx, fmt.Sprintf("/api/v1/device/ws?since=%d", since), func(raw []byte) error {
			if !connected {
				connected = true
				backoff = time.Second
				notify("hub", map[string]bool{"connected": true})
			}
			var ev struct {
				Seq int64 `json:"seq"`
			}
			json.Unmarshal(raw, &ev)
			if ev.Seq > 0 {
				since = ev.Seq
			}
			if notify("event", json.RawMessage(raw)) != nil {
				return errClientGone
			}
			return nil
		})
		if errors.Is(err, errClientGone) {
			return
		}
		if notify("hub", map[string]bool{"connected": false}) != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// LogRing keeps the daemon's last log lines for `logs.tail`. It is a
// slog.Handler wrapper.
type LogRing struct {
	slog.Handler
	ring *ring
}

type ring struct {
	mu    sync.Mutex
	lines []string
	buf   *bytes.Buffer
	text  slog.Handler
}

const ringSize = 1000

// NewLogRing wraps h, so every record also lands in the ring.
func NewLogRing(h slog.Handler) *LogRing {
	r := &ring{buf: &bytes.Buffer{}}
	r.text = slog.NewTextHandler(r.buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return &LogRing{Handler: h, ring: r}
}

func (l *LogRing) Handle(ctx context.Context, rec slog.Record) error {
	l.ring.mu.Lock()
	l.ring.buf.Reset()
	l.ring.text.Handle(ctx, rec)
	l.ring.lines = append(l.ring.lines, strings.TrimRight(l.ring.buf.String(), "\n"))
	if len(l.ring.lines) > ringSize {
		l.ring.lines = l.ring.lines[len(l.ring.lines)-ringSize:]
	}
	l.ring.mu.Unlock()
	return l.Handler.Handle(ctx, rec)
}

func (l *LogRing) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogRing{Handler: l.Handler.WithAttrs(attrs), ring: l.ring}
}

func (l *LogRing) WithGroup(name string) slog.Handler {
	return &LogRing{Handler: l.Handler.WithGroup(name), ring: l.ring}
}

// Tail returns the last n lines (all when n <= 0).
func (l *LogRing) Tail(n int) []string {
	if l == nil {
		return []string{}
	}
	l.ring.mu.Lock()
	defer l.ring.mu.Unlock()
	lines := l.ring.lines
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return append([]string{}, lines...)
}
