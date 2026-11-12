package brain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/auth"
	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tunnel"
)

// acceptLink bridges a machine's link to the Hub's /node endpoint.
func (b *Brain) acceptLink(l *tunnel.Link) {
	id, ok := cutNode(l.Peer)
	if !ok || !b.Leading() {
		l.Close()
		return
	}
	r := b.C.Roster()
	if r == nil {
		l.Close()
		return
	}
	if n, in := r.Node(id); !in || n.Frozen {
		l.Close()
		return
	}
	hc, err := b.client(auth.Tunnel{Device: id})
	if err != nil {
		l.Close()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://brain/node", &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		slog.Warn("brain: cannot connect a machine to the Hub", "node", id, "err", err)
		l.Close()
		return
	}
	ws.SetReadLimit(8 << 20)
	defer ws.CloseNow()
	go func() {
		defer cancel()
		for {
			_, raw, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if err := l.Write(ctx, raw); err != nil {
				return
			}
		}
	}()
	go func() {
		defer cancel()
		<-l.Done()
	}()
	for {
		f, err := l.Read(ctx)
		if err != nil {
			break
		}
		if err := ws.Write(ctx, websocket.MessageText, f); err != nil {
			break
		}
	}
	l.Close()
}

// The headers a page or machine may send; everything else is dropped.
var passHeaders = []string{"Content-Type", "Accept", "If-Match", "If-None-Match", "Last-Event-ID"}

// Routes that no longer exist behind the relay: machines join and leave
// through the user's roster, never through the brain.
var gone = []*regexp.Regexp{
	regexp.MustCompile(`^POST /api/v1/nodes/pairing-code$`),
	regexp.MustCompile(`^DELETE /api/v1/nodes/[^/]+$`),
	regexp.MustCompile(`^POST /api/v1/node/pair$`),
	regexp.MustCompile(`^(GET|POST) /api/v1/auth/`),
}

// Critical routes whose real check is on the machine (a permit with the
// user's assertion goes there directly): the brain does not ask again.
var machineChecked = []*regexp.Regexp{
	regexp.MustCompile(`^POST /api/v1/projects/[^/]+/shells$`),
}

// SessionMessage handles what a page sends to the brain.
func (b *Brain) SessionMessage(from string, payload []byte) {
	var m tunnel.RPCMsg
	if json.Unmarshal(payload, &m) != nil {
		return
	}
	sid := strings.TrimPrefix(from, "sess:")
	b.serve(from, m, auth.Tunnel{SID: sid})
}

// MachineMessage handles a machine's device calls (sous chat, sous mcp).
func (b *Brain) MachineMessage(from string, payload []byte) {
	var m tunnel.RPCMsg
	if json.Unmarshal(payload, &m) != nil {
		return
	}
	id, _ := cutNode(from)
	if m.K == "rpc" || m.K == "ws.open" {
		path := m.Path
		if !strings.HasPrefix(path, "/api/v1/device/") {
			b.answer(from, tunnel.RPCMsg{K: "rpc.res", ID: m.ID, Status: 403, Body: errBody("forbidden", "机器只能调用设备接口")})
			return
		}
	}
	b.serve(from, m, auth.Tunnel{Device: id})
}

func (b *Brain) serve(from string, m tunnel.RPCMsg, t auth.Tunnel) {
	switch m.K {
	case "rpc":
		go b.rpc(from, m, t)
	case "ws.open":
		go b.stream(from, m, t)
	case "ws.close":
		b.mu.Lock()
		if c := b.streams[from+"|"+m.ID]; c != nil {
			c()
			delete(b.streams, from+"|"+m.ID)
		}
		b.mu.Unlock()
	}
}

func (b *Brain) answer(to string, m tunnel.RPCMsg) {
	raw, _ := json.Marshal(m)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := b.C.Send(ctx, to, raw); err != nil {
		slog.Debug("brain: cannot answer", "to", to, "err", err)
	}
}

func errBody(code, msg string) string {
	raw, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": msg}})
	return trust.B64.EncodeToString(raw)
}

func routeKey(method, path string) string {
	p, _, _ := strings.Cut(path, "?")
	return method + " " + p
}

func matches(res []*regexp.Regexp, key string) bool {
	for _, re := range res {
		if re.MatchString(key) {
			return true
		}
	}
	return false
}

func (b *Brain) rpc(from string, m tunnel.RPCMsg, t auth.Tunnel) {
	res := tunnel.RPCMsg{K: "rpc.res", ID: m.ID}
	defer func() { b.answer(from, res) }()
	if !b.Leading() {
		res.Error = errNotLeading.Error()
		return
	}
	if !strings.HasPrefix(m.Path, "/") || strings.Contains(m.Path, "://") {
		res.Status, res.Body = 400, errBody("bad_path", "bad path")
		return
	}
	key := routeKey(m.Method, m.Path)
	if matches(gone, key) {
		res.Status, res.Body = 410, errBody("gone", "机器的加入和移除改在“机器”里用名册完成")
		return
	}
	body, err := trust.B64.DecodeString(m.Body)
	if err != nil {
		res.Status, res.Body = 400, errBody("bad_body", "bad body")
		return
	}
	if t.SID != "" {
		if matches(machineChecked, key) {
			t.Fresh = true
		} else if m.Header["X-Sous-Intent"] != "" {
			if err := b.checkIntent(t.SID, m, body); err != nil {
				res.Status, res.Body = 401, errBody("reauth", "Face ID 验证没有通过："+err.Error())
				return
			}
			t.Fresh = true
		}
	}
	hc, err := b.client(t)
	if err != nil {
		res.Error = err.Error()
		return
	}
	method := m.Method
	if method == "" {
		method = http.MethodGet
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://brain"+m.Path, bytes.NewReader(body))
	if err != nil {
		res.Status, res.Body = 400, errBody("bad_request", err.Error())
		return
	}
	for _, h := range passHeaders {
		if v := m.Header[h]; v != "" {
			req.Header.Set(h, v)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		res.Error = err.Error()
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 6<<20))
	res.Status, res.Body = resp.StatusCode, trust.B64.EncodeToString(data)
	res.Header = map[string]string{"Content-Type": resp.Header.Get("Content-Type")}
}

// checkIntent verifies that the user approved this exact request just now.
func (b *Brain) checkIntent(sid string, m tunnel.RPCMsg, body []byte) error {
	raw, err := trust.B64.DecodeString(m.Header["X-Sous-Intent"])
	if err != nil {
		return err
	}
	var in trust.Intent
	if err := json.Unmarshal(raw, &in); err != nil || in.T != trust.TIntent {
		return errors.New("不是请求意图")
	}
	sum := sha256.Sum256(body)
	if in.SID != sid || in.Method != m.Method || in.Path != m.Path || in.BodySHA256 != trust.B64.EncodeToString(sum[:]) {
		return errors.New("意图和请求不符")
	}
	if err := trust.CheckFresh(in.IAT, trust.AssertionTTL, b.C.Now()); err != nil {
		return err
	}
	var a trust.Assertion
	ar, err := trust.B64.DecodeString(m.Header["X-Sous-Assertion"])
	if err != nil || json.Unmarshal(ar, &a) != nil {
		return errors.New("缺少断言")
	}
	r := b.C.Roster()
	if r == nil {
		return errors.New("没有名册")
	}
	return trust.VerifyAssertion(r, a, trust.CriticalChallenge(raw), b.C.Now(), b.used)
}

var usedMu sync.Mutex
var usedSet = map[string]time.Time{}

func (b *Brain) used(c string) bool {
	usedMu.Lock()
	defer usedMu.Unlock()
	now := time.Now()
	for k, t := range usedSet {
		if now.Sub(t) > 2*trust.AssertionTTL {
			delete(usedSet, k)
		}
	}
	_, seen := usedSet[c]
	usedSet[c] = now
	return seen
}

func (b *Brain) stream(from string, m tunnel.RPCMsg, t auth.Tunnel) {
	closeWith := func(msg string) {
		b.answer(from, tunnel.RPCMsg{K: "ws.close", ID: m.ID, Error: msg})
	}
	if !b.Leading() {
		closeWith(errNotLeading.Error())
		return
	}
	hc, err := b.client(t)
	if err != nil {
		closeWith(err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	key := from + "|" + m.ID
	b.mu.Lock()
	b.streams[key] = cancel
	b.mu.Unlock()
	defer func() {
		cancel()
		b.mu.Lock()
		delete(b.streams, key)
		b.mu.Unlock()
	}()
	ws, resp, err := websocket.Dial(ctx, "ws://brain"+m.Path, &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		msg := err.Error()
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			msg = "unauthorized"
		}
		closeWith(msg)
		return
	}
	ws.SetReadLimit(16 << 20)
	defer ws.CloseNow()
	b.answer(from, tunnel.RPCMsg{K: "ws.ok", ID: m.ID})
	for {
		_, raw, err := ws.Read(ctx)
		if err != nil {
			if ctx.Err() == nil {
				closeWith("")
			}
			return
		}
		data, _ := json.Marshal(tunnel.RPCMsg{K: "ws.msg", ID: m.ID, Body: trust.B64.EncodeToString(raw)})
		sctx, scancel := context.WithTimeout(ctx, 30*time.Second)
		err = b.C.Send(sctx, from, data)
		scancel()
		if err != nil {
			return
		}
	}
}

var _ net.Conn = (*taggedConn)(nil)
