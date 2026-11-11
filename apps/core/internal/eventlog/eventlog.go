// Package eventlog is the single writer of the Hub: it stores events through
// the store, then publishes them on the bus and notifies listeners.
package eventlog

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sysmap"
)

// Listener is called after commit for every stored event. It must not block.
type Listener func(event.Event)

type Log struct {
	Store *store.Store
	Bus   *event.Bus

	mu        sync.RWMutex
	listeners []Listener
	effectsCh chan struct{}
}

var lastSeq atomic.Int64

func New(s *store.Store, b *event.Bus) *Log {
	lastSeq.Store(0)
	sysmap.RegisterProbe("eventlog", sysmap.ProbeFunc(func(context.Context) sysmap.Status {
		n := 0
		for _, edge := range sysmap.StaticEdges {
			if edge.To == "eventlog" {
				n += sysmap.Sum(edge.ID, 60)
			}
		}
		state := sysmap.OK
		if n == 0 {
			state = sysmap.Idle
		}
		return sysmap.Status{State: state, Summary: fmt.Sprintf("最新 #%d · 每分钟 %d 条", lastSeq.Load(), n), Gauges: []sysmap.Gauge{{Label: "事件/分钟", Value: float64(n)}}}
	}))
	return &Log{Store: s, Bus: b, effectsCh: make(chan struct{}, 1)}
}

// Listen registers a listener for stored events.
func (l *Log) Listen(fn Listener) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.listeners = append(l.listeners, fn)
}

// EffectsEnqueued signals when a write enqueued effects, so the runner wakes early.
func (l *Log) EffectsEnqueued() <-chan struct{} { return l.effectsCh }

// Write runs fn in one transaction, then publishes the appended events.
func (l *Log) Write(ctx context.Context, fn func(w *store.Writer) error) ([]event.Event, error) {
	evs, effects, err := l.Store.Write(ctx, fn)
	if err != nil {
		return nil, err
	}
	w, caller := sysmap.Callers()
	for _, ev := range evs {
		if caller != "" {
			// "call <type>": the caller asked the writer for this event.
			sysmap.Count(sysmap.EdgeID(caller, w), refsOf(ev), "call "+string(ev.Type))
		}
		if w != "" {
			sysmap.Count(sysmap.EdgeID(w, "eventlog"), refsOf(ev), string(ev.Type))
		}
		for _, edge := range sysmap.ReadEdges[string(ev.Type)] {
			sysmap.Count(edge, refsOf(ev), string(ev.Type))
		}
		for old := lastSeq.Load(); ev.Seq > old && !lastSeq.CompareAndSwap(old, ev.Seq); old = lastSeq.Load() {
		}
	}
	if effects {
		sysmap.Count("eventlog>effects", sysmap.Refs{}, "入队")
	}
	l.mu.RLock()
	listeners := l.listeners
	l.mu.RUnlock()
	for _, ev := range evs {
		l.Bus.Publish(ev)
		for _, fn := range listeners {
			fn(ev)
		}
	}
	if effects {
		select {
		case l.effectsCh <- struct{}{}:
		default:
		}
	}
	return evs, nil
}

// Append stores events in one transaction.
func (l *Log) Append(ctx context.Context, evs ...event.Event) ([]event.Event, error) {
	return l.Write(ctx, func(w *store.Writer) error {
		for _, ev := range evs {
			if _, err := w.Append(ev); err != nil {
				return err
			}
		}
		return nil
	})
}

// Emit publishes an ephemeral event without storing it.
func (l *Log) Emit(ev event.Event) {
	ev.Seq = 0
	if w := sysmap.Writer(); w != "" {
		sysmap.Count(sysmap.EdgeID(w, "eventlog"), refsOf(ev), string(ev.Type))
	}
	l.Bus.Publish(ev)
}

// EnsureMainThread creates the main thread if it does not exist and returns its id.
func (l *Log) EnsureMainThread(ctx context.Context) (string, error) {
	id, err := l.Store.MainThreadID(ctx)
	if err == nil {
		return id, nil
	}
	if err != store.ErrNotFound {
		return "", err
	}
	ev := event.New(event.ThreadCreated, event.ActorSystem, "", event.ThreadCreatedPayload{Title: "主线", Kind: "main"})
	ev.ThreadID = ev.ID
	if _, err := l.Append(ctx, ev); err != nil {
		return "", err
	}
	return ev.ThreadID, nil
}

func refsOf(ev event.Event) sysmap.Refs {
	return sysmap.Refs{ThreadID: ev.ThreadID, TaskID: ev.TaskID, RunID: ev.RunID}
}
