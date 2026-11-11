package sysmap

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type TraceDeps struct {
	RunsOf    func(context.Context, string) []string
	PendingOf func(context.Context, string, string) bool
}
type traceStep struct {
	TS      int64  `json:"ts"`
	Edge    string `json:"edge"`
	Summary string `json:"summary"`
}

func Routes(_ *http.ServeMux, protect func(string, http.HandlerFunc), origins []string, deps TraceDeps) {
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	protect("GET /api/v1/system/graph", func(w http.ResponseWriter, r *http.Request) { write(w, Snapshot(r.Context())) })
	protect("GET /api/v1/system/history", func(w http.ResponseWriter, r *http.Request) {
		end := now().Unix()
		since, err := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		if err != nil || since < end-Window {
			since = end - Window
		}
		write(w, map[string]any{"counts": Counts(since), "records": Records(since*1000, MaxRecords), "now": end})
	})
	protect("GET /api/v1/system/trace", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		kind, id := "", ""
		for _, k := range []string{"task", "run", "thread"} {
			if value := q.Get(k + "_id"); value != "" {
				if kind != "" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "bad_request", "message": "只能指定一个追踪对象"}})
					return
				}
				kind, id = k, value
			}
		}
		if kind == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "bad_request", "message": "只能指定一个追踪对象"}})
			return
		}
		runs := map[string]bool{}
		if kind == "task" && deps.RunsOf != nil {
			for _, run := range deps.RunsOf(r.Context(), id) {
				runs[run] = true
			}
		}
		steps := []traceStep{}
		for _, rec := range Records(0, MaxRecords) {
			match := kind == "task" && (rec.Refs.TaskID == id || rec.Refs.RunID != "" && runs[rec.Refs.RunID]) || kind == "run" && rec.Refs.RunID == id || kind == "thread" && rec.Refs.ThreadID == id
			if match {
				steps = append(steps, traceStep{rec.TS, rec.Edge, rec.Summary})
			}
		}
		sort.SliceStable(steps, func(i, j int) bool { return steps[i].TS < steps[j].TS })
		if len(steps) > 300 {
			steps = steps[len(steps)-300:]
		}
		pending := ""
		if len(steps) > 0 && deps.PendingOf != nil && deps.PendingOf(r.Context(), kind, id) {
			edge := steps[len(steps)-1].Edge
			if i := strings.LastIndex(edge, ">"); i >= 0 {
				pending = edge[i+1:]
			}
		}
		write(w, map[string]any{"steps": steps, "pending": pending})
	})
	protect("GET /api/v1/system/stream", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: origins})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := conn.CloseRead(r.Context())
		ticks, stop := Subscribe()
		defer stop()
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case tick := <-ticks:
				raw, err := json.Marshal(map[string]any{"type": "sys.tick", "payload": tick})
				if err != nil {
					return
				}
				wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err = conn.Write(wctx, websocket.MessageText, raw)
				cancel()
				if err != nil {
					return
				}
			case <-ping.C:
				pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pctx)
				cancel()
				if err != nil {
					return
				}
			}
		}
	})
}
