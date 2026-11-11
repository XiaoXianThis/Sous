package sysmap

import (
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/redact"
)

const (
	Window     = 900
	MaxRecords = 5000
	callStats  = 20
)

type Record struct {
	TS      int64  `json:"ts"`
	Edge    string `json:"edge"`
	Refs    Refs   `json:"refs"`
	Summary string `json:"summary"`
}
type CallStat struct {
	Calls     int     `json:"calls"`
	Errors    int     `json:"errors"`
	AvgMS     float64 `json:"avg_ms"`
	LastError string  `json:"last_error,omitempty"`
}
type bucket struct {
	second int64
	count  uint32
}
type call struct {
	ms  float64
	err string
}

var now = time.Now
var flow = struct {
	sync.Mutex
	counts  map[string]*[Window]bucket
	records [MaxRecords]Record
	total   int
	calls   map[string][]call
}{}

func countLocked(edge string, refs Refs, summary string) {
	if flow.counts == nil {
		flow.counts = make(map[string]*[Window]bucket)
	}
	b := flow.counts[edge]
	if b == nil {
		b = &[Window]bucket{}
		flow.counts[edge] = b
	}
	t := now()
	second := t.Unix()
	i := int(second % Window)
	if b[i].second != second {
		b[i] = bucket{second: second}
	}
	b[i].count++
	text := []rune(redact.Mask(summary))
	if len(text) > 80 {
		text = text[:80]
	}
	flow.records[flow.total%MaxRecords] = Record{TS: t.UnixMilli(), Edge: edge, Refs: refs, Summary: string(text)}
	flow.total++
}
func Count(edge string, refs Refs, summary string) {
	flow.Lock()
	defer flow.Unlock()
	countLocked(edge, refs, summary)
}
func Call(edge string, refs Refs, summary string, d time.Duration, err error) {
	flow.Lock()
	defer flow.Unlock()
	countLocked(edge, refs, summary)
	if flow.calls == nil {
		flow.calls = make(map[string][]call)
	}
	c := call{ms: float64(d) / float64(time.Millisecond)}
	if err != nil {
		c.err = redact.Mask(err.Error())
	}
	entries := append(flow.calls[edge], c)
	if len(entries) > callStats {
		entries = entries[len(entries)-callStats:]
	}
	flow.calls[edge] = entries
}
func Counts(since int64) map[string][]uint32 {
	flow.Lock()
	defer flow.Unlock()
	end := now().Unix()
	if since < end-Window+1 {
		since = end - Window + 1
	}
	result := map[string][]uint32{}
	if since > end {
		return result
	}
	for edge, b := range flow.counts {
		a := make([]uint32, end-since+1)
		found := false
		for s := since; s <= end; s++ {
			x := b[s%Window]
			if x.second == s {
				a[s-since] = x.count
				found = found || x.count > 0
			}
		}
		if found {
			result[edge] = a
		}
	}
	return result
}
func Sum(edge string, n int) int {
	flow.Lock()
	defer flow.Unlock()
	if n > Window {
		n = Window
	}
	b := flow.counts[edge]
	if b == nil {
		return 0
	}
	end := now().Unix()
	sum := 0
	for i := 0; i < n; i++ {
		s := end - int64(i)
		x := b[s%Window]
		if x.second == s {
			sum += int(x.count)
		}
	}
	return sum
}
func Records(after int64, limit int) []Record {
	flow.Lock()
	defer flow.Unlock()
	result := []Record{}
	start := flow.total - MaxRecords
	if start < 0 {
		start = 0
	}
	for i := start; i < flow.total && len(result) < limit; i++ {
		r := flow.records[i%MaxRecords]
		if r.TS > after {
			result = append(result, r)
		}
	}
	return result
}
func Stats(edge string) CallStat {
	flow.Lock()
	defer flow.Unlock()
	var s CallStat
	for _, c := range flow.calls[edge] {
		s.Calls++
		s.AvgMS += c.ms
		if c.err != "" {
			s.Errors++
			s.LastError = c.err
		}
	}
	if s.Calls > 0 {
		s.AvgMS /= float64(s.Calls)
	}
	return s
}
func Writer() string {
	w, _ := Callers()
	return w
}

// Callers is Writer plus the module that called into the writer's code, so
// a direct call between modules (the task engine asking the machine
// connection to start a run, an endpoint of the task engine serving a page)
// shows as a line of its own. The search stops at the event log, the effects
// runner and the supervisor: past those the code was started by an event or
// a tool call, which the page already shows.
func Callers() (writer, caller string) {
	var pcs [48]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	var funcs []string
	for {
		frame, more := frames.Next()
		funcs = append(funcs, frame.Function)
		if !more {
			break
		}
	}
	return callersOf(funcs)
}

func callersOf(funcs []string) (writer, caller string) {
	for _, fn := range funcs {
		_, rest, ok := strings.Cut(fn, "/internal/")
		if !ok {
			continue
		}
		pkg, _, _ := strings.Cut(rest, "/")
		pkg, _, _ = strings.Cut(pkg, ".")
		if writer == "" {
			switch pkg {
			case "eventlog", "store", "sysmap":
				continue
			}
			writer = moduleOf(pkg)
			if writer == "" {
				return "", ""
			}
			continue
		}
		switch pkg {
		case "eventlog", "store", "effects", "supervisor", "tool", "prompts":
			return writer, ""
		}
		if m := moduleOf(pkg); m != "" && m != writer {
			if writer == "nodehub" && m == "httpapi" {
				// A machine's websocket is opened by an HTTP request and
				// lives on; what it reports is the machine's, not the API's.
				return writer, ""
			}
			return writer, m
		}
	}
	return writer, ""
}

func moduleOf(pkg string) string {
	switch pkg {
	case "httpapi", "auth":
		return "httpapi"
	case "supervisor", "tool", "prompts":
		return "supervisor"
	case "task", "router":
		return "task"
	case "trigger", "inspector", "nodehub", "notify", "mcp", "models":
		return pkg
	}
	return ""
}

// recordsSince returns records written since a ring sequence number.
func recordsSince(sequence int) ([]Record, int) {
	flow.Lock()
	defer flow.Unlock()
	end := flow.total
	if sequence < end-MaxRecords {
		sequence = end - MaxRecords
	}
	if sequence < 0 {
		sequence = 0
	}
	if sequence > end {
		sequence = end
	}
	result := make([]Record, 0, end-sequence)
	for i := sequence; i < end; i++ {
		result = append(result, flow.records[i%MaxRecords])
	}
	return result, end
}
