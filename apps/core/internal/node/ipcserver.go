package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

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

var hubHTTP = &http.Client{Timeout: 30 * time.Second}

// hub calls a device endpoint of the Hub.
func (d *Daemon) hub(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.conf().HubURL+"/api/v1/device"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.conf().Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hubHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 Hub：%w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
			return errors.New(e.Error.Message)
		}
		return fmt.Errorf("Hub 返回 %s", resp.Status)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
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

// relayEvents streams the Hub's event stream to a local client as "event"
// notifications, reconnecting until the client goes away.
func (d *Daemon) relayEvents(ctx context.Context, since int64, notify func(string, any) error) {
	u := strings.Replace(d.conf().HubURL, "http", "ws", 1) + "/api/v1/device/ws"
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+d.conf().Token)
	backoff := time.Second
	for ctx.Err() == nil {
		ws, _, err := websocket.Dial(ctx, fmt.Sprintf("%s?since=%d", u, since), &websocket.DialOptions{HTTPHeader: hdr})
		if err == nil {
			ws.SetReadLimit(16 << 20)
			backoff = time.Second
			notify("hub", map[string]bool{"connected": true})
			for {
				_, raw, err := ws.Read(ctx)
				if err != nil {
					break
				}
				var ev struct {
					Seq int64 `json:"seq"`
				}
				json.Unmarshal(raw, &ev)
				if ev.Seq > 0 {
					since = ev.Seq
				}
				if notify("event", json.RawMessage(raw)) != nil {
					ws.CloseNow()
					return
				}
			}
			ws.CloseNow()
			if notify("hub", map[string]bool{"connected": false}) != nil {
				return
			}
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
