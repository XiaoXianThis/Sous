package sysmap

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCountAndSum(t *testing.T) {
	Reset()
	old := now
	defer func() { now = old }()
	tm := time.Unix(10000, 0)
	now = func() time.Time { return tm }
	for i := 0; i < 3; i++ {
		Count("a>b", Refs{}, "")
	}
	if got := Sum("a>b", 900); got != 3 {
		t.Fatal(got)
	}
	tm = tm.Add(901 * time.Second)
	Count("a>b", Refs{}, "")
	if got := Sum("a>b", 900); got != 1 {
		t.Fatal(got)
	}
}
func TestRecordsRing(t *testing.T) {
	Reset()
	for i := 0; i < 5001; i++ {
		Count("a>b", Refs{}, fmt.Sprint(i))
	}
	records := Records(0, 10000)
	if len(records) != 5000 || records[0].Summary != "1" {
		t.Fatal(len(records), records[0])
	}
}
func TestCallStats(t *testing.T) {
	Reset()
	for _, ms := range []int{10, 20, 30} {
		Call("a>b", Refs{}, "", time.Duration(ms)*time.Millisecond, nil)
	}
	Call("a>b", Refs{}, "", 40*time.Millisecond, errors.New("boom"))
	if got := Stats("a>b"); got != (CallStat{Calls: 4, Errors: 1, AvgMS: 25, LastError: "boom"}) {
		t.Fatal(got)
	}
}
func TestSummaryRedacted(t *testing.T) {
	Reset()
	secret := "sk-abcdefghijklmnopqrstuvwxyz"
	Count("a>b", Refs{}, secret)
	if strings.Contains(Records(0, 1)[0].Summary, secret) {
		t.Fatal("secret retained")
	}
}
func TestWriter(t *testing.T) {
	if got := Writer(); got != "" {
		t.Fatal(got)
	}
}

func TestCallersOf(t *testing.T) {
	const m = "github.com/XiaoXianThis/Sous/apps/core/internal/"
	cases := []struct {
		funcs          []string
		writer, caller string
	}{
		// The task engine's endpoint, served by the API.
		{[]string{m + "eventlog.(*Log).Write", m + "task.(*Engine).create", m + "httpapi.(*Server).ServeHTTP"}, "task", "httpapi"},
		// The task engine asks the machine connection to start a run.
		{[]string{m + "store.(*Store).Write", m + "eventlog.(*Log).Write", m + "nodehub.(*Hub).Create", m + "task.(*Engine).startRun", m + "effects.(*Runner).runOne"}, "nodehub", "task"},
		// A listener: past the event log the caller is not a direct call.
		{[]string{m + "eventlog.(*Log).Write", m + "task.(*Engine).transition", m + "eventlog.(*Log).Write", m + "nodehub.(*nodeConn).handle"}, "task", ""},
		// A tool call: the supervisor stops the search.
		{[]string{m + "eventlog.(*Log).Write", m + "task.(*Engine).Create", m + "tool.(*Registry).Call", m + "supervisor.(*Supervisor).answerTopic"}, "task", ""},
		// Same module further up is not a caller.
		{[]string{m + "eventlog.(*Log).Write", m + "task.a", m + "router.b", m + "hub.(*Hub).Run"}, "task", ""},
		// A machine's websocket goroutine starts in the API's handler.
		{[]string{m + "eventlog.(*Log).Write", m + "nodehub.(*nodeConn).handle", m + "nodehub.(*Hub).ServeNode", m + "httpapi.(*Server).ServeHTTP"}, "nodehub", ""},
		// An unknown writer.
		{[]string{m + "eventlog.(*Log).Write", m + "hub.(*Hub).Run"}, "", ""},
	}
	for i, c := range cases {
		w, caller := callersOf(c.funcs)
		if w != c.writer || caller != c.caller {
			t.Errorf("case %d: got (%q, %q), want (%q, %q)", i, w, caller, c.writer, c.caller)
		}
	}
}
