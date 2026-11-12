package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// RPC carries HTTP-shaped requests and WebSocket-shaped streams to the
// brain inside channels (docs/api/core-brain-rpc.md). The page and the
// machines' device calls both use it.

// RPCMsg is one RPC message. Bodies are base64url.
type RPCMsg struct {
	K      string            `json:"k"` // rpc, rpc.res, ws.open, ws.ok, ws.msg, ws.close
	ID     string            `json:"id"`
	Method string            `json:"method,omitempty"`
	Path   string            `json:"path,omitempty"`
	Header map[string]string `json:"header,omitempty"`
	Body   string            `json:"body,omitempty"`
	Status int               `json:"status,omitempty"`
	Error  string            `json:"error,omitempty"`
}

// RPCClient sends requests to one peer at a time (the current brain).
type RPCClient struct {
	C *Client

	mu      sync.Mutex
	n       int64
	pending map[string]chan RPCMsg
	streams map[string]chan RPCMsg
}

// NewRPCClient makes a client.
func NewRPCClient(c *Client) *RPCClient {
	return &RPCClient{C: c, pending: map[string]chan RPCMsg{}, streams: map[string]chan RPCMsg{}}
}

// Handle takes RPC answers; it reports whether payload was one.
func (r *RPCClient) Handle(from string, payload []byte) bool {
	var m RPCMsg
	if json.Unmarshal(payload, &m) != nil {
		return false
	}
	// Only answers to our own requests are ours: the brain sharing this
	// address gets everything else (a page closing its stream, say).
	switch m.K {
	case "rpc.res":
		r.mu.Lock()
		ch := r.pending[m.ID]
		r.mu.Unlock()
		if ch == nil {
			return false
		}
		select {
		case ch <- m:
		default:
		}
		return true
	case "ws.ok", "ws.msg", "ws.close":
		r.mu.Lock()
		ch := r.streams[m.ID]
		r.mu.Unlock()
		if ch == nil {
			return false
		}
		select {
		case ch <- m:
		case <-time.After(10 * time.Second):
		}
		return true
	}
	return false
}

func (r *RPCClient) id() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	return fmt.Sprintf("%s.%d", trust.B64.EncodeToString(trust.RandomBytes(6)), r.n)
}

// HTTPError is a non-2xx answer.
type HTTPError struct {
	Status  int
	Code    string
	Message string
}

func (e *HTTPError) Error() string { return e.Message }

// Do sends a request to peer and decodes the JSON answer into out.
func (r *RPCClient) Do(ctx context.Context, peer, method, path string, header map[string]string, body []byte) (RPCMsg, error) {
	m := RPCMsg{K: "rpc", ID: r.id(), Method: method, Path: path, Header: header}
	if body != nil {
		m.Body = trust.B64.EncodeToString(body)
	}
	ch := make(chan RPCMsg, 1)
	r.mu.Lock()
	r.pending[m.ID] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.pending, m.ID)
		r.mu.Unlock()
	}()
	raw, _ := json.Marshal(m)
	if err := r.C.Send(ctx, peer, raw); err != nil {
		return RPCMsg{}, err
	}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	select {
	case res := <-ch:
		if res.Error != "" {
			return res, errors.New(res.Error)
		}
		return res, nil
	case <-rctx.Done():
		return RPCMsg{}, fmt.Errorf("大脑没有应答：%w", rctx.Err())
	}
}

// JSON is Do with JSON bodies and HTTP error decoding.
func (r *RPCClient) JSON(ctx context.Context, peer, method, path string, body, out any) error {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	res, err := r.Do(ctx, peer, method, path, map[string]string{"Content-Type": "application/json"}, raw)
	if err != nil {
		return err
	}
	data, _ := trust.B64.DecodeString(res.Body)
	if res.Status >= 300 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		json.Unmarshal(data, &e)
		if e.Error.Message == "" {
			e.Error.Message = fmt.Sprintf("HTTP %d", res.Status)
		}
		return &HTTPError{Status: res.Status, Code: e.Error.Code, Message: e.Error.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Stream opens a WebSocket-shaped stream on peer and calls fn for every
// message until the stream ends or fn fails.
func (r *RPCClient) Stream(ctx context.Context, peer, path string, fn func([]byte) error) error {
	m := RPCMsg{K: "ws.open", ID: r.id(), Path: path}
	ch := make(chan RPCMsg, 64)
	r.mu.Lock()
	r.streams[m.ID] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.streams, m.ID)
		r.mu.Unlock()
		raw, _ := json.Marshal(RPCMsg{K: "ws.close", ID: m.ID})
		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r.C.Send(cctx, peer, raw)
		cancel()
	}()
	raw, _ := json.Marshal(m)
	if err := r.C.Send(ctx, peer, raw); err != nil {
		return err
	}
	opened := false
	for {
		var wait <-chan time.Time
		if !opened {
			wait = time.After(30 * time.Second)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait:
			return errors.New("大脑没有打开事件流")
		case res := <-ch:
			switch res.K {
			case "ws.ok":
				opened = true
			case "ws.close":
				if res.Error != "" {
					return errors.New(res.Error)
				}
				return ErrLinkClosed
			case "ws.msg":
				data, _ := trust.B64.DecodeString(res.Body)
				if err := fn(data); err != nil {
					return err
				}
			}
		}
	}
}
