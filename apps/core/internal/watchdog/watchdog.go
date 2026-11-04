// Package watchdog is the in-process half of the process watchdog (design
// §9). Long-running loops beat; probes check that shared locks and the
// database still answer. When a loop stops beating or a probe hangs past
// its limit, the process is stuck, and the watchdog exits so the service
// manager (systemd, launchd, Windows service recovery) starts it again. The
// event log, the effects queue and the node's outbox let the new process
// pick up where the old one stopped.
package watchdog

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"
)

type Watchdog struct {
	// Exit is called with the names of the stuck parts (default: log and
	// os.Exit(3), a failure every service manager restarts).
	Exit func(stuck []string)
	// Every is how often the watchdog checks (default 15s).
	Every time.Duration

	mu     sync.Mutex
	loops  map[string]*loop
	probes map[string]probe
}

type loop struct {
	limit time.Duration
	last  time.Time
}

type probe struct {
	limit time.Duration
	fn    func(ctx context.Context) error
}

func New() *Watchdog {
	return &Watchdog{loops: map[string]*loop{}, probes: map[string]probe{}}
}

// Loop registers a loop that must beat at least once every limit, and
// returns its beat function.
func (w *Watchdog) Loop(name string, limit time.Duration) func() {
	w.mu.Lock()
	w.loops[name] = &loop{limit: limit, last: time.Now()}
	w.mu.Unlock()
	return func() {
		w.mu.Lock()
		if l := w.loops[name]; l != nil {
			l.last = time.Now()
		}
		w.mu.Unlock()
	}
}

// Probe registers a check that must return within limit. Its error is
// logged but does not count as stuck: only hanging does.
func (w *Watchdog) Probe(name string, limit time.Duration, fn func(ctx context.Context) error) {
	w.mu.Lock()
	w.probes[name] = probe{limit: limit, fn: fn}
	w.mu.Unlock()
}

// Run checks until ctx ends.
func (w *Watchdog) Run(ctx context.Context) {
	every := w.Every
	if every <= 0 {
		every = 15 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if stuck := w.Check(ctx); len(stuck) > 0 && ctx.Err() == nil {
				exit := w.Exit
				if exit == nil {
					exit = defaultExit
				}
				exit(stuck)
				return
			}
		}
	}
}

// Check returns the names of stuck loops and probes.
func (w *Watchdog) Check(ctx context.Context) []string {
	w.mu.Lock()
	var stuck []string
	now := time.Now()
	for name, l := range w.loops {
		if now.Sub(l.last) > l.limit {
			stuck = append(stuck, name)
		}
	}
	probes := make(map[string]probe, len(w.probes))
	for k, v := range w.probes {
		probes[k] = v
	}
	w.mu.Unlock()

	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, p := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := make(chan error, 1)
			pctx, cancel := context.WithTimeout(ctx, p.limit)
			defer cancel()
			go func() { done <- p.fn(pctx) }()
			select {
			case err := <-done:
				if err != nil && ctx.Err() == nil {
					slog.Warn("watchdog: probe failed", "probe", name, "err", err)
				}
			case <-time.After(p.limit):
				mu.Lock()
				stuck = append(stuck, name)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Strings(stuck)
	return stuck
}

func defaultExit(stuck []string) {
	slog.Error("watchdog: the process is stuck; exiting so the service manager restarts it", "stuck", stuck)
	os.Exit(3)
}
