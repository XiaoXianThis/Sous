package brain

import (
	"context"

	"github.com/XiaoXianThis/Sous/apps/core/internal/node"
)

// Attach makes a daemon take part in the brain election. Every machine
// runs it: whether this one may think is the roster's decision, which can
// change at any time.
func Attach(ctx context.Context, d *node.Daemon, root, nodeID string, opts ...func(*Brain)) (*Brain, error) {
	c, r, err := d.Tunnel()
	if err != nil {
		return nil, err
	}
	b := &Brain{Root: root, NodeID: nodeID, C: c, R: r}
	d.SetBrainHooks(b.SessionMessage, b.MachineMessage)
	d.OnTrustDoc(func(name string) {
		switch name {
		case "roster":
			b.RosterChanged()
		case "lease", "snapshot", "domain":
			b.Kick()
		}
	})
	for _, o := range opts {
		o(b)
	}
	go b.Run(ctx)
	return b, nil
}
