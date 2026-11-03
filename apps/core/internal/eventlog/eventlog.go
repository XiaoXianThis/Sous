// Package eventlog is the single writer of the Hub: it stores events through
// the store, then publishes them on the bus and notifies listeners.
package eventlog

import (
	"context"
	"sync"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
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

func New(s *store.Store, b *event.Bus) *Log {
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
