package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// httpT is the streamable HTTP transport: every message is a POST; the
// answer is either one JSON body or an SSE stream that carries it.
type httpT struct {
	url     string
	headers map[string]string
	client  *http.Client
	ids     idGen

	mu       sync.Mutex
	session  string
	protocol string
	closed   bool
	gone     chan struct{}
}

func newHTTP(cfg Config) (*httpT, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("bad URL %q", cfg.URL)
	}
	return &httpT{
		url: cfg.URL, headers: cfg.Headers, gone: make(chan struct{}),
		// No overall timeout: calls are bounded by their context, and an SSE
		// answer may stay open for the whole call.
		client: &http.Client{Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 60 * time.Second, IdleConnTimeout: 90 * time.Second,
		}},
	}, nil
}

func (h *httpT) post(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	h.mu.Lock()
	if h.session != "" {
		req.Header.Set("Mcp-Session-Id", h.session)
	}
	if h.protocol != "" {
		req.Header.Set("MCP-Protocol-Version", h.protocol)
	}
	h.mu.Unlock()
	return h.client.Do(req)
}

func (h *httpT) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	id := h.ids.next()
	b, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params})
	resp, err := h.post(ctx, b)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" && method == "initialize" {
		h.mu.Lock()
		h.session = sid
		h.mu.Unlock()
	}
	if resp.StatusCode == http.StatusNotFound && h.sessionSet() {
		// The server forgot our session: this connection is no longer usable.
		h.markGone()
		return nil, fmt.Errorf("%w: the session expired", ErrClosed)
	}
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	ct := resp.Header.Get("Content-Type")
	var m rpcMessage
	switch {
	case strings.HasPrefix(ct, "text/event-stream"):
		m, err = h.readStream(ctx, resp.Body, id)
	default:
		var body []byte
		if body, err = io.ReadAll(io.LimitReader(resp.Body, maxMessage)); err == nil {
			m, err = pick(body, id)
		}
	}
	if err != nil {
		return nil, err
	}
	if m.Error != nil {
		return nil, m.Error
	}
	return m.Result, nil
}

func (h *httpT) sessionSet() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.session != ""
}

// pick finds the response to id in a JSON body (one message or a batch).
func pick(body []byte, id int64) (rpcMessage, error) {
	body = bytes.TrimSpace(body)
	var msgs []rpcMessage
	if len(body) > 0 && body[0] == '[' {
		if err := json.Unmarshal(body, &msgs); err != nil {
			return rpcMessage{}, fmt.Errorf("bad JSON from the server: %w", err)
		}
	} else {
		var m rpcMessage
		if err := json.Unmarshal(body, &m); err != nil {
			return rpcMessage{}, fmt.Errorf("bad JSON from the server: %w", err)
		}
		msgs = []rpcMessage{m}
	}
	for _, m := range msgs {
		if n, ok := m.idOf(); ok && m.isResponse() && n == id {
			return m, nil
		}
	}
	return rpcMessage{}, errors.New("the server did not answer the request")
}

// readStream reads SSE events until the response to id arrives. Requests
// the server sends on the way get a refusal over a separate POST.
func (h *httpT) readStream(ctx context.Context, r io.Reader, id int64) (rpcMessage, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var data []string
	flush := func() (rpcMessage, bool) {
		if len(data) == 0 {
			return rpcMessage{}, false
		}
		payload := strings.Join(data, "\n")
		data = nil
		var m rpcMessage
		if json.Unmarshal([]byte(payload), &m) != nil {
			return rpcMessage{}, false
		}
		if m.isResponse() {
			if n, ok := m.idOf(); ok && n == id {
				return m, true
			}
			return rpcMessage{}, false
		}
		if m.Method != "" && len(m.ID) > 0 {
			go func(b []byte) {
				c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				if resp, err := h.post(c, b); err == nil {
					resp.Body.Close()
				}
			}(refuse(m.ID, m.Method))
		}
		return rpcMessage{}, false
	}
	total := 0
	for {
		line, err := br.ReadString('\n')
		total += len(line)
		if total > maxMessage {
			return rpcMessage{}, errors.New("the answer is too large")
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if m, ok := flush(); ok {
				return m, nil
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		if err != nil {
			if m, ok := flush(); ok {
				return m, nil
			}
			if ctx.Err() != nil {
				return rpcMessage{}, ctx.Err()
			}
			return rpcMessage{}, errors.New("the stream ended without an answer")
		}
	}
}

func (h *httpT) notify(ctx context.Context, method string, params any) error {
	b, _ := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	resp, err := h.post(ctx, b)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (h *httpT) markGone() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.gone)
	}
}

func (h *httpT) close() {
	h.mu.Lock()
	sid, already := h.session, h.closed
	h.mu.Unlock()
	h.markGone()
	if already || sid == "" {
		return
	}
	// End the session politely; best effort.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, h.url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", sid)
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	if resp, err := h.client.Do(req); err == nil {
		resp.Body.Close()
	}
}

func (h *httpT) done() <-chan struct{} { return h.gone }
func (h *httpT) diagnostics() string   { return "" }
func (h *httpT) setProtocol(v string) {
	h.mu.Lock()
	h.protocol = v
	h.mu.Unlock()
}
