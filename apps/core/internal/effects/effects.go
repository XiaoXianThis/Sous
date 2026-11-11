// Package effects runs side effects that were enqueued inside a write
// transaction, after it committed. Failures retry with backoff: 2s, doubling,
// capped at one minute. An effect counts as a system problem only after it
// failed FailThreshold times in a row.
package effects

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
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
	// Beat, when set, is called on every loop iteration (for the watchdog).
	Beat func()

	mu       sync.RWMutex
	handlers map[string]Handler
	inflight map[int64]bool
	wg       sync.WaitGroup
}

// NewRunner creates a runner. wake may be nil; interval is the polling period.
func NewRunner(s *store.Store, wake <-chan struct{}, interval time.Duration) *Runner {
	r := &Runner{store: s, wake: wake, interval: interval, now: time.Now, handlers: map[string]Handler{}, inflight: map[int64]bool{}}
	var mu sync.Mutex
	var at time.Time
	var cached sysmap.Status
	sysmap.RegisterProbe("effects", sysmap.ProbeFunc(func(ctx context.Context) sysmap.Status {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(at) < 2*time.Second {
			return cached
		}
		n, err := s.PendingEffects(ctx)
		if err != nil {
			return sysmap.Status{State: sysmap.Degraded, Summary: "状态读取超时"}
		}
		failed, err := s.FailingEffects(ctx, 1)
		if err != nil {
			return sysmap.Status{State: sysmap.Degraded, Summary: "状态读取超时"}
		}
		cached = sysmap.Status{State: sysmap.Idle, Summary: "队列是空的", Gauges: []sysmap.Gauge{{Label: "积压", Value: float64(n)}}}
		if len(failed) > 0 {
			cached.State = sysmap.Degraded
			cached.Summary = fmt.Sprintf("积压 %d 个，有失败多次的：%s", n, failed[0].Kind)
		} else if n > 0 {
			cached.State = sysmap.Busy
			cached.Summary = fmt.Sprintf("积压 %d 个", n)
		}
		at = time.Now()
		return cached
	}))
	return r
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

// Run processes due effects until ctx is done. Effects run concurrently, so
// a slow one (a model call) does not hold up the others; one effect never
// runs twice at once.
func (r *Runner) Run(ctx context.Context) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	for {
		if r.Beat != nil {
			r.Beat()
		}
		r.start(ctx)
		select {
		case <-ctx.Done():
			r.wg.Wait()
			return
		case <-t.C:
		case <-r.wake:
		}
	}
}

// RunOnce runs every effect that is due now and waits for them.
func (r *Runner) RunOnce(ctx context.Context) {
	r.start(ctx)
	r.wg.Wait()
}

func (r *Runner) start(ctx context.Context) {
	due, err := r.store.DueEffects(ctx, r.now(), 100)
	if err != nil {
		slog.Error("effects: list due", "err", err)
		return
	}
	for _, e := range due {
		r.mu.Lock()
		if r.inflight[e.ID] {
			r.mu.Unlock()
			continue
		}
		r.inflight[e.ID] = true
		h := r.handlers[e.Kind]
		r.mu.Unlock()
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.runOne(ctx, h, e)
			r.mu.Lock()
			delete(r.inflight, e.ID)
			r.mu.Unlock()
		}()
	}
}

func (r *Runner) runOne(ctx context.Context, h Handler, e store.Effect) {
	edge := "effects>task"
	if e.Kind == store.EffectPush {
		edge = "effects>notify"
	}
	sysmap.Count(edge, effectRefs(e), e.Kind)
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
		return
	}
	if ctx.Err() != nil {
		return // shutting down; it runs again after the restart
	}
	attempts := e.Attempts + 1
	if attempts >= FailThreshold {
		slog.Warn("effects: failing", "kind", e.Kind, "key", e.Key, "attempts", attempts, "err", runErr)
	}
	if err := r.store.RetryEffect(ctx, e.ID, runErr.Error(), r.now().Add(Backoff(attempts))); err != nil {
		slog.Error("effects: retry", "id", e.ID, "err", err)
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

// effectRefs is what an effect is about, for the system page: the run a task
// is advanced for, or the event a push notification tells of.
func effectRefs(e store.Effect) sysmap.Refs {
	switch e.Kind {
	case store.EffectTaskAdvance, store.EffectSetup:
		return sysmap.Refs{RunID: string(e.Payload)}
	case store.EffectPush:
		var ev struct {
			ThreadID string `json:"thread_id"`
			TaskID   string `json:"task_id"`
			RunID    string `json:"run_id"`
		}
		if json.Unmarshal(e.Payload, &ev) == nil {
			return sysmap.Refs{ThreadID: ev.ThreadID, TaskID: ev.TaskID, RunID: ev.RunID}
		}
	}
	return sysmap.Refs{}
}
