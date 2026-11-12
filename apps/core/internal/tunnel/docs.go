package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/trust"
)

// acceptDoc verifies a document and keeps it when it is newer.
func (c *Client) acceptDoc(ctx context.Context, name string, body json.RawMessage) {
	if len(body) == 0 {
		return
	}
	var err error
	changed := false
	switch name {
	case "roster":
		changed, err = c.acceptRoster(ctx, body)
	case "scopes":
		var u trust.ScopesUpdate
		if err = json.Unmarshal(body, &u); err == nil {
			c.mu.Lock()
			r, prev := c.roster, c.scopes
			c.mu.Unlock()
			var sc trust.Scopes
			if sc, err = trust.AcceptScopes(r, prev, u, c.cfg.Now(), nil); err == nil {
				c.mu.Lock()
				c.scopes, c.st.Scopes = &sc, &u
				c.mu.Unlock()
				changed = true
			} else if prev != nil && sc.V == prev.V {
				err = nil // the same version again
			}
		}
	case "pause":
		var sd trust.Signed
		if err = json.Unmarshal(body, &sd); err == nil {
			var p trust.Pause
			if _, err = sd.Open(c.cfg.Identity, trust.TPause, &p); err == nil {
				c.mu.Lock()
				if c.pause == nil || p.V > c.pause.V {
					c.pause, c.st.Pause, changed = &p, &sd, true
				}
				c.mu.Unlock()
			}
		}
	case "domain":
		var sd trust.Signed
		if err = json.Unmarshal(body, &sd); err == nil {
			var d trust.DomainKeys
			if _, err = sd.Open(c.cfg.Identity, trust.TDomain, &d); err == nil {
				c.mu.Lock()
				if c.domain == nil || d.V > c.domain.V {
					c.domain, c.st.Domain, changed = &d, &sd, true
				}
				c.mu.Unlock()
			}
		}
	case "lease":
		var sd trust.Signed
		if err = json.Unmarshal(body, &sd); err == nil {
			c.mu.Lock()
			r := c.roster
			c.mu.Unlock()
			if r == nil {
				return
			}
			var l trust.Lease
			if l, err = trust.OpenLease(r, sd); err == nil {
				c.mu.Lock()
				if c.lease == nil || l.Newer(*c.lease) {
					c.lease, c.leaseDoc, changed = &l, sd, true
				}
				c.mu.Unlock()
			}
		}
	case "snapshot":
		var sd trust.Signed
		if err = json.Unmarshal(body, &sd); err == nil {
			var m trust.SnapshotManifest
			if m, err = c.openSnapshot(sd); err == nil {
				c.mu.Lock()
				if c.snapshot == nil || m.Seq > c.snapshot.Seq {
					c.snapshot, c.snapDoc, changed = &m, sd, true
					if m.Seq > c.st.Snap {
						c.st.Snap = m.Seq
					}
				}
				c.mu.Unlock()
			}
		}
	default:
		changed = true // alerts and the like: the receiver decides
	}
	if err != nil {
		slog.Warn("tunnel: rejected a document", "name", name, "err", err)
		return
	}
	if changed {
		c.save()
		if c.cfg.OnDoc != nil {
			c.cfg.OnDoc(name)
		}
	}
}

func (c *Client) acceptRoster(ctx context.Context, body json.RawMessage) (bool, error) {
	var u trust.RosterUpdate
	if err := json.Unmarshal(body, &u); err != nil {
		return false, err
	}
	next, err := trust.OpenRoster(c.cfg.Identity, u.Doc)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	cur := c.roster
	c.mu.Unlock()
	if cur != nil && next.V <= cur.V {
		return false, nil
	}
	used := func(ch string) bool { return false } // versions only grow
	// Walk every version we missed, each checked against the one before.
	steps := []trust.RosterUpdate{u}
	if cur != nil && next.V > cur.V+1 {
		steps = nil
		for v := cur.V + 1; v <= next.V; v++ {
			raw, err := c.Get(ctx, fmt.Sprintf("roster@%d", v))
			if err != nil || len(raw) == 0 {
				return false, fmt.Errorf("missing roster v%d: %v", v, err)
			}
			var s trust.RosterUpdate
			if err := json.Unmarshal(raw, &s); err != nil {
				return false, err
			}
			steps = append(steps, s)
		}
	}
	r := cur
	var last trust.RosterUpdate
	for _, s := range steps {
		got, err := trust.AcceptRoster(c.cfg.Identity, r, s, c.cfg.Now(), used)
		if err != nil {
			return false, err
		}
		r, last = &got, s
	}
	c.mu.Lock()
	c.roster, c.st.Roster = r, &last
	c.mu.Unlock()
	return true, nil
}

func (c *Client) openSnapshot(sd trust.Signed) (trust.SnapshotManifest, error) {
	var m trust.SnapshotManifest
	raw, err := sd.Payload()
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	c.mu.Lock()
	r := c.roster
	c.mu.Unlock()
	if r == nil {
		return m, fmt.Errorf("no roster")
	}
	n, ok := r.Node(m.Holder)
	if !ok || n.Brain == nil {
		return m, fmt.Errorf("%s is not a brain", m.Holder)
	}
	pub, _ := trust.B64.DecodeString(n.SignPub)
	_, err = sd.Open(pub, trust.TSnapshot, &m)
	return m, err
}

// Roster is the current verified roster (nil before pairing finished).
func (c *Client) Roster() *trust.Roster {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.roster == nil {
		return nil
	}
	r := *c.roster
	return &r
}

// Scopes is the user's autonomy scope, or the defaults.
func (c *Client) Scopes() trust.Scopes {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.scopes == nil {
		return trust.DefaultScopes()
	}
	return *c.scopes
}

// Paused reports whether the user paused all work.
func (c *Client) Paused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pause != nil && c.pause.Paused
}

// Lease is the newest verified lease and its document.
func (c *Client) Lease() (trust.Lease, trust.Signed, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lease == nil {
		return trust.Lease{}, trust.Signed{}, false
	}
	return *c.lease, c.leaseDoc, true
}

// Brain is the machine that holds a live lease now, if any.
func (c *Client) Brain() (string, int64, bool) {
	l, _, ok := c.Lease()
	if !ok || !l.Live(c.cfg.Now()) {
		return "", 0, false
	}
	c.mu.Lock()
	r := c.roster
	c.mu.Unlock()
	if r == nil {
		return "", 0, false
	}
	if n, ok := r.Node(l.Holder); !ok || n.Brain == nil || n.Frozen {
		return "", 0, false
	}
	return l.Holder, l.Term, true
}

// Domain is the newest verified domain-key document.
func (c *Client) Domain() *trust.DomainKeys {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.domain
}

// Snapshot is the newest verified snapshot manifest.
func (c *Client) Snapshot() (trust.SnapshotManifest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot == nil {
		return trust.SnapshotManifest{}, false
	}
	return *c.snapshot, true
}

// SetLease records a lease this client just put (it also comes back as a doc).
func (c *Client) SetLease(l trust.Lease, sd trust.Signed) {
	c.mu.Lock()
	if c.lease == nil || l.Newer(*c.lease) {
		c.lease, c.leaseDoc = &l, sd
	}
	c.mu.Unlock()
}

// Now is the client's clock.
func (c *Client) Now() time.Time { return c.cfg.Now() }

// Addr is this client's address.
func (c *Client) Addr() string { return c.cfg.Addr }

// SignKey is this client's signing key.
func (c *Client) SignKey() *trust.SignKey { return c.cfg.Sign }

// KEMKey is this client's KEM key.
func (c *Client) KEMKey() *trust.KEMKey { return c.cfg.KEM }

// Identity is the pinned identity key.
func (c *Client) Identity() []byte { return c.cfg.Identity }
