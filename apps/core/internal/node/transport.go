package node

import (
	"context"
	"errors"

	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// FrameConn is one connection to the brain.
type FrameConn interface {
	Read(ctx context.Context) (proto.Frame, error)
	Write(ctx context.Context, f proto.Frame) error
	Close()
}

// Transport reaches the brain. In a release it is always the end-to-end
// encrypted tunnel through the relay (trustnode.go); tests in this package
// may plug in a direct connection to an in-process Hub.
type Transport interface {
	Name() string
	// Dial connects to the current brain.
	Dial(ctx context.Context) (FrameConn, error)
	// Call is a device request: sous chat, sous note, sous mcp.
	Call(ctx context.Context, method, path string, body, out any) error
	// Stream reads a device event stream until fn fails or the stream ends.
	Stream(ctx context.Context, path string, fn func(raw []byte) error) error
}

// errClientGone ends Stream when the local client went away.
var errClientGone = errors.New("client gone")

// testTransport lets this package's tests connect straight to a Hub.
var testTransport func(d *Daemon) Transport

func (d *Daemon) transport() (Transport, error) {
	d.mu.Lock()
	t := d.tr
	d.mu.Unlock()
	if t != nil {
		return t, nil
	}
	if testTransport != nil && d.conf().RelayURL == "" {
		t = testTransport(d)
	} else {
		var err error
		if t, err = d.openTrust(); err != nil {
			return nil, err
		}
	}
	d.mu.Lock()
	if d.tr == nil {
		d.tr = t
	}
	t = d.tr
	d.mu.Unlock()
	return t, nil
}
