package auth

import "context"

// Tunnel marks a request the brain received through the relay (decision
// 0030). The brain checked the session certificate (SID) or the machine
// (Device) and, for critical requests, the user's assertion (Fresh). Such a
// request carries no cookie; the brain has no HTTP listener of its own.
type Tunnel struct {
	SID    string // a page's session
	Device string // a machine's device call (sous chat, sous mcp)
	Fresh  bool   // the user verified this very request
}

type tunnelKey struct{}

// WithTunnel attaches t to ctx.
func WithTunnel(ctx context.Context, t Tunnel) context.Context {
	return context.WithValue(ctx, tunnelKey{}, t)
}

// TunnelOf returns the tunnel identity of a request, if it has one.
func TunnelOf(ctx context.Context) (Tunnel, bool) {
	t, ok := ctx.Value(tunnelKey{}).(Tunnel)
	return t, ok
}
