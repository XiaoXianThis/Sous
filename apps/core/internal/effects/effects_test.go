package effects

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestBackoff(t *testing.T) {
	cases := map[int]time.Duration{1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 5: 32 * time.Second, 6: time.Minute, 20: time.Minute}
	for attempts, want := range cases {
		if got := Backoff(attempts); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}

func TestRunnerRetriesThenCompletes(t *testing.T) {
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, _, err := s.Write(ctx, func(w *store.Writer) error { return w.Enqueue("k", "1", []byte("x")) }); err != nil {
		t.Fatal(err)
	}

	clock := time.Now()
	r := NewRunner(s, nil, time.Hour)
	r.now = func() time.Time { return clock }
	calls := 0
	r.Handle("k", func(ctx context.Context, e store.Effect) error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})

	r.RunOnce(ctx)
	r.RunOnce(ctx) // not due yet: backoff applies
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
	clock = clock.Add(Backoff(1))
	r.RunOnce(ctx)
	clock = clock.Add(Backoff(2))
	r.RunOnce(ctx)
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
	if due, _ := s.DueEffects(ctx, clock.Add(time.Hour), 10); len(due) != 0 {
		t.Fatalf("effect not completed: %+v", due)
	}
}

func TestRunnerRecoversPanicsAndUnknownKinds(t *testing.T) {
	s, _ := store.Open(":memory:")
	defer s.Close()
	ctx := context.Background()
	s.Write(ctx, func(w *store.Writer) error {
		w.Enqueue("panics", "1", nil)
		return w.Enqueue("unknown", "1", nil)
	})
	r := NewRunner(s, nil, time.Hour)
	r.Handle("panics", func(context.Context, store.Effect) error { panic("boom") })
	r.RunOnce(ctx)
	failing, _ := s.FailingEffects(ctx, 1)
	if len(failing) != 2 {
		t.Fatalf("failing = %+v", failing)
	}
}
