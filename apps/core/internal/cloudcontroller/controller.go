// Package cloudcontroller coordinates a single hosted workspace's capacity.
// It has no API for work materials or for making guest execution decisions.
package cloudcontroller

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/XiaoXianThis/Sous/apps/core/internal/cloudwork"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
)

const (
	ensure  = "ensure"
	retire  = "retire"
	pending = "pending"
	present = "present"
	absent  = "absent"
)

// Request goes ONLY to the configured operator driver, never to a workspace
// user or its model. A driver must provide a durable fence keyed by Scope and
// Reservation. In particular, it may acknowledge absence only when a late
// ensure of that reservation is unable to create a new instance.
type Request struct {
	Action         string `json:"action"`
	Scope          string `json:"scope"`
	Generation     int64  `json:"generation"`
	Reservation    string `json:"reservation"`
	Hub            string `json:"hub"`
	Bootstrap      string `json:"bootstrap,omitempty"`
	BootstrapUntil int64  `json:"bootstrap_until,omitempty"`
}

type Provider interface {
	Reconcile(context.Context, Request) (string, error)
}

type controlPlane interface {
	Plan(context.Context, cloudwork.ReconcileCapacity) (cloudwork.Capacity, error)
	Retired(context.Context, int64, string) (cloudwork.Capacity, error)
}

type Controller struct {
	hub        controlPlane
	provider   Provider
	origin     string
	seed       []byte
	generation int64
}

func New(h *Client, p Provider, seed string) (*Controller, error) {
	if h == nil || p == nil || !cloudwork.ValidToken(seed) || seed == h.token {
		return nil, errors.New("controller requires a separate private bootstrap seed and a provider driver")
	}
	material, err := hex.DecodeString(seed)
	if err != nil {
		return nil, err
	}
	return &Controller{hub: h, provider: p, origin: h.origin, seed: material}, nil
}

// These short-lived secrets can be re-derived after a crash before or during
// provider creation. The management bearer is deliberately not the seed.
func (c *Controller) bootstrap(gen int64, reservation string) string {
	mac := hmac.New(sha256.New, c.seed)
	_, _ = mac.Write([]byte("sous-cloud-worker-bootstrap-v1\x00" + c.origin + "\x00" + strconv.FormatInt(gen, 10) + "\x00" + reservation))
	return hex.EncodeToString(mac.Sum(nil))
}

// Step is called serially. The provider must be idempotent even across a
// process crash, timeout, and an out-of-order call from an obsolete controller.
func (c *Controller) Step(ctx context.Context) (cloudwork.Capacity, error) {
	state, err := c.hub.Plan(ctx, cloudwork.ReconcileCapacity{Generation: c.generation})
	if err != nil {
		return state, err
	}
	// Revalidate a learned generation before acting. A stale observation is
	// not authority to launch against a newly expired or cancelled grant.
	if state.Generation != c.generation {
		c.generation = state.Generation
		state, err = c.hub.Plan(ctx, cloudwork.ReconcileCapacity{Generation: c.generation})
		if err != nil || state.Generation != c.generation {
			return state, err
		}
	}
	if state.Phase == "off" && state.Queued > 0 && !state.Active {
		if state.Generation == math.MaxInt64 {
			return state, cloudwork.ErrCapacity
		}
		reservation := ids.New()
		next := c.bootstrap(state.Generation+1, reservation)
		state, err = c.hub.Plan(ctx, cloudwork.ReconcileCapacity{Generation: state.Generation, BootstrapHash: cloudwork.Hash([]byte(next)), Reservation: reservation})
		if err != nil {
			return state, err
		}
		c.generation = state.Generation
	}
	if state.Phase == "off" || state.Phase == "online" {
		return state, nil
	}
	if state.Generation < 1 || state.Reservation == "" {
		return state, errors.New("invalid Hub capacity reservation")
	}
	req := Request{Scope: cloudwork.Hash([]byte(c.origin)), Generation: state.Generation, Reservation: state.Reservation, Hub: c.origin}
	switch state.Phase {
	case "starting":
		req.Action = ensure
		req.Bootstrap = c.bootstrap(req.Generation, req.Reservation)
		req.BootstrapUntil = state.BootUntil
	case "retiring":
		req.Action = retire
	default:
		return state, errors.New("unknown Hub capacity phase")
	}
	result, err := c.provider.Reconcile(ctx, req)
	if err != nil {
		return state, fmt.Errorf("provider %s not confirmed for reservation %s", req.Action, req.Reservation)
	}
	switch {
	case result == pending, req.Action == ensure && result == present:
		return state, nil
	case req.Action == retire && result == absent:
		return c.hub.Retired(ctx, req.Generation, req.Reservation)
	default:
		return state, errors.New("provider returned an invalid capacity result")
	}
}
