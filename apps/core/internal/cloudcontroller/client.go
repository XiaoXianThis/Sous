package cloudcontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudwork"
)

type Client struct {
	origin, token string
	http          *http.Client
}

func NewClient(origin, token, ca, socket string) (*Client, error) {
	if !cloudwork.ValidToken(token) {
		return nil, errors.New("invalid controller credential")
	}
	h, err := cloudwork.MachineHTTP(origin, ca, socket)
	if err != nil {
		return nil, err
	}
	return &Client{origin, token, h}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) Plan(ctx context.Context, in cloudwork.ReconcileCapacity) (cloudwork.Capacity, error) {
	return c.send(ctx, "reconcile", in)
}

func (c *Client) Retired(ctx context.Context, gen int64, reservation string) (cloudwork.Capacity, error) {
	return c.send(ctx, "retired", struct {
		Generation  int64  `json:"generation"`
		Reservation string `json:"reservation"`
	}{gen, reservation})
}

func (c *Client) send(ctx context.Context, action string, in any) (out cloudwork.Capacity, err error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return out, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+cloudwork.ControllerPath+action, bytes.NewReader(raw))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	r, err := c.http.Do(req)
	if err != nil {
		return out, errors.New("unable to reach hosted cloud capacity control plane")
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return out, fmt.Errorf("Hub did not confirm capacity %s (HTTP %d)", action, r.StatusCode)
	}
	d := json.NewDecoder(io.LimitReader(r.Body, 4096))
	d.DisallowUnknownFields()
	if err = d.Decode(&out); err != nil || d.Decode(new(json.RawMessage)) != io.EOF || out.Phase == "" {
		return out, errors.New("invalid Hub capacity response")
	}
	return out, nil
}
