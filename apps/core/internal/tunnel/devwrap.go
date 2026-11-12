package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/coder/websocket"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// anon sends one frame on an unauthenticated connection and returns the answer.
func anon(ctx context.Context, relayURL string, f rframe) (rframe, error) {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dctx, WSURL(relayURL), nil)
	if err != nil {
		return rframe{}, err
	}
	defer ws.CloseNow()
	r := &Room{ws: ws}
	if _, err := r.read(dctx); err != nil {
		return rframe{}, err
	}
	if err := writeJSON(dctx, ws, rframe{T: "anon"}); err != nil {
		return rframe{}, err
	}
	if _, err := r.read(dctx); err != nil {
		return rframe{}, err
	}
	f.RID = "1"
	if err := writeJSON(dctx, ws, f); err != nil {
		return rframe{}, err
	}
	res, err := r.read(dctx)
	if err != nil {
		return rframe{}, err
	}
	if res.T == "err" {
		return rframe{}, errors.New(res.Error)
	}
	return res, nil
}

// DevwrapChallenge asks the relay for a one-time challenge (decision 0033).
func DevwrapChallenge(ctx context.Context, relayURL string) (string, error) {
	f, err := anon(ctx, relayURL, rframe{T: "devwrap.challenge"})
	return f.Nonce, err
}

// DevwrapGet trades an assertion over the challenge for the device's wrap.
func DevwrapGet(ctx context.Context, relayURL, credID, nonce string, a trust.Assertion) (json.RawMessage, error) {
	data, _ := json.Marshal(a)
	f, err := anon(ctx, relayURL, rframe{T: "devwrap.get", Name: credID, Nonce: nonce, Data: data})
	return f.Body, err
}

// DevwrapPut stores a device wrap (sessions only).
func (c *Client) DevwrapPut(ctx context.Context, credID string, body json.RawMessage) error {
	_, err := c.request(ctx, rframe{T: "devwrap.put", Name: credID, Body: body})
	return err
}
