package watchdog

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestStuckLoopsAndProbes(t *testing.T) {
	w := New()
	beat := w.Loop("effects", 50*time.Millisecond)
	w.Loop("idle", time.Hour)
	block := make(chan struct{})
	defer close(block)
	w.Probe("store", 50*time.Millisecond, func(ctx context.Context) error { <-block; return nil })
	w.Probe("ok", time.Second, func(context.Context) error { return nil })

	time.Sleep(80 * time.Millisecond)
	if got := w.Check(context.Background()); !reflect.DeepEqual(got, []string{"effects", "store"}) {
		t.Fatalf("stuck: %v", got)
	}
	beat()
	w2 := New()
	w2.Loop("x", time.Hour)()
	if got := w2.Check(context.Background()); len(got) != 0 {
		t.Fatalf("healthy: %v", got)
	}
}

func TestRunExits(t *testing.T) {
	w := New()
	w.Every = 10 * time.Millisecond
	w.Loop("loop", 20*time.Millisecond)
	exited := make(chan []string, 1)
	w.Exit = func(s []string) { exited <- s }
	go w.Run(context.Background())
	select {
	case s := <-exited:
		if len(s) != 1 || s[0] != "loop" {
			t.Fatalf("exit: %v", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the watchdog did not fire")
	}
}
