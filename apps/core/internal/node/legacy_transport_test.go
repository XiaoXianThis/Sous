package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// The tests of this package (and node_test) talk to an in-process Hub
// directly, as 0.2 did. Releases never contain this: they only reach the
// brain through the relay (trustnode.go).

func init() { testTransport = func(d *Daemon) Transport { return &hubTransport{d: d} } }

type hubTransport struct{ d *Daemon }

func (h *hubTransport) Name() string { return "hub (test)" }

type wsConn struct{ ws *websocket.Conn }

func (w wsConn) Read(ctx context.Context) (proto.Frame, error) {
	var f proto.Frame
	_, raw, err := w.ws.Read(ctx)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(raw, &f)
}

func (w wsConn) Write(ctx context.Context, f proto.Frame) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return w.ws.Write(ctx, websocket.MessageText, raw)
}

func (w wsConn) Close() { w.ws.CloseNow() }

func (h *hubTransport) Dial(ctx context.Context) (FrameConn, error) {
	cfg := h.d.conf()
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+cfg.Token)
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(dctx, wsURL(cfg.HubURL), &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return nil, ErrRevoked
		}
		return nil, err
	}
	ws.SetReadLimit(maxFrameBytes)
	return wsConn{ws}, nil
}

var hubHTTP = &http.Client{Timeout: 30 * time.Second}

func (h *hubTransport) Call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, h.d.conf().HubURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+h.d.conf().Token)
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

func (h *hubTransport) Stream(ctx context.Context, path string, fn func([]byte) error) error {
	u := strings.Replace(h.d.conf().HubURL, "http", "ws", 1) + path
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+h.d.conf().Token)
	ws, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(16 << 20)
	for {
		_, raw, err := ws.Read(ctx)
		if err != nil {
			return err
		}
		if err := fn(raw); err != nil {
			return err
		}
	}
}

// Pair trades a 0.2 pairing code for a device token (tests only).
func Pair(ctx context.Context, root, hubURL, code, name string) (Config, error) {
	return PairWith(ctx, root, hubURL, code, name, PairOptions{})
}

// PairWith is Pair with a role and workspaces (tests only).
func PairWith(ctx context.Context, root, hubURL, code, name string, opt PairOptions) (Config, error) {
	hub, err := NormalizeHubURL(hubURL)
	if err != nil {
		return Config{}, err
	}
	host, _ := os.Hostname()
	if name == "" {
		name = host
	}
	body, _ := json.Marshal(proto.PairRequest{Code: code, Name: name, OS: runtime.GOOS, Arch: runtime.GOARCH, Hostname: host})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hub+"/api/v1/node/pair", bytes.NewReader(body))
	if err != nil {
		return Config{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Config{}, fmt.Errorf("连接 Hub 失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return Config{}, fmt.Errorf("配对失败（HTTP %d）：%s", resp.StatusCode, e.Error.Message)
	}
	var pr proto.PairResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return Config{}, err
	}
	c := Config{HubURL: hub, NodeID: pr.NodeID, Name: name, Token: pr.Token, Concurrency: 2}
	if old, err := LoadConfig(root); err == nil {
		c.Agents = old.Agents
		c.Concurrency = old.Concurrency
		c.Role, c.Workspaces = old.Role, old.Workspaces
	}
	if opt.Role == "server" {
		c.Role = "server"
	} else if opt.Role == "dev" {
		c.Role = ""
	}
	if len(opt.Workspaces) > 0 {
		c.Workspaces = opt.Workspaces
	}
	return c, SaveConfig(root, c)
}
