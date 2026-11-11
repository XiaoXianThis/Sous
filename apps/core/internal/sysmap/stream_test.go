package sysmap

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestSubscribe(t *testing.T) {
	Reset()
	oldTick, oldNow := tickEvery, now
	tickEvery = 10 * time.Millisecond
	var seconds atomic.Int64
	seconds.Store(10000)
	now = func() time.Time { return time.Unix(seconds.Load(), 0) }
	defer func() { tickEvery = oldTick; now = oldNow }()
	a, stopA := Subscribe()
	b, stopB := Subscribe()
	first := <-a
	if len(first.Modules) != len(Modules) {
		t.Fatal(len(first.Modules))
	}
	<-b
	Count("ui>httpapi", Refs{}, "test")
	seconds.Add(1)
	select {
	case tick := <-a:
		if tick.Edges["ui>httpapi"] != 1 {
			t.Fatal(tick.Edges)
		}
	case <-time.After(time.Second):
		t.Fatal("no tick")
	}
	stopA()
	select {
	case <-b:
	case <-time.After(time.Second):
		t.Fatal("second subscriber stopped")
	}
	stopB()
	if running() {
		t.Fatal("ticker still running")
	}
}
