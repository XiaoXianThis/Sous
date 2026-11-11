package sysmap

import (
	"context"
	"testing"
	"time"
)

func TestSnapshotInstancesAndTimeout(t *testing.T) {
	Reset()
	RegisterInstances("machine", func(context.Context) []Instance {
		return []Instance{{Module: Module{ID: "machine:test"}, Status: Status{State: OK}}}
	})
	RegisterProbe("ui", ProbeFunc(func(context.Context) Status { time.Sleep(300 * time.Millisecond); return Status{State: OK} }))
	g := Snapshot(context.Background())
	if g.Status["ui"].Summary != "状态读取超时" {
		t.Fatal(g.Status["ui"])
	}
	found := 0
	for _, e := range g.Edges {
		if e.From == "machine:test" || e.To == "machine:test" {
			found++
		}
	}
	if found != 2 {
		t.Fatal(found)
	}
}
