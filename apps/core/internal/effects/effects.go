// Package effects runs side effects that were enqueued inside a write
// transaction, after it committed. Failures retry with backoff: 2s, doubling,
// capped at one minute. An effect counts as a system problem only after it
// failed FailThreshold times in a row.
package effects

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const (
	FirstBackoff  = 2 * time.Second
	MaxBackoff    = time.Minute
	FailThreshold = 3
)

// Handler runs one effect. Returning an error schedules a retry.
type Handler func(ctx context.Context, e store.Effect) error

type Runner struct {
	store    *store.Store
	wake     <-chan struct{}
	interval time.Duration
	now      func() time.Time

	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRunner creates a runner. wake may be nil; interval is the polling period.
func NewRunner(s *store.Store, wake <-chan struct{}, interval time.Duration) *Runner {
	return &Runner{store: s, wake: wake, interval: interval, now: time.Now, handlers: map[string]Handler{}}
}

// Handle registers the handler for an effect kind.
func (r *Runner) Handle(kind string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[kind] = h
}

// Backoff returns the delay before the next attempt after `attempts` failures.
func Backoff(attempts int) time.Duration {
	d := FirstBackoff
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= MaxBackoff {
			return MaxBackoff
		}
	}
	return d
}

// Run processes due effects until ctx is done.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		r.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.wake:
		}
	}
}

// RunOnce runs every effect that is due now.
func (r *Runner) RunOnce(ctx context.Context) {
	due, err := r.store.DueEffects(ctx, r.now(), 100)
	if err != nil {
		slog.Error("effects: list due", "err", err)
		return
	}
	for _, e := range due {
		r.mu.RLock()
		h := r.handlers[e.Kind]
		r.mu.RUnlock()
		var runErr error
		if h == nil {
			runErr = fmt.Errorf("no handler for effect kind %q", e.Kind)
		} else {
			runErr = safeRun(ctx, h, e)
		}
		if runErr == nil {
			if err := r.store.CompleteEffect(ctx, e.ID); err != nil {
				slog.Error("effects: complete", "id", e.ID, "err", err)
			}
			continue
		}
		attempts := e.Attempts + 1
		if attempts >= FailThreshold {
			slog.Warn("effects: failing", "kind", e.Kind, "key", e.Key, "attempts", attempts, "err", runErr)
		}
		if err := r.store.RetryEffect(ctx, e.ID, runErr.Error(), r.now().Add(Backoff(attempts))); err != nil {
			slog.Error("effects: retry", "id", e.ID, "err", err)
		}
	}
}

func safeRun(ctx context.Context, h Handler, e store.Effect) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return h(ctx, e)
}
