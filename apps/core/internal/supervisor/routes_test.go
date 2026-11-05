package supervisor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestContextRoutes(t *testing.T) {
	comp := &answering{fn: reply("## 待办和承诺\n- 周五提醒续费 [#2]")}
	log, main, sup := setupSup(t, Options{Provider: &answering{fn: reply("嗯")}, Compactor: comp, CompactorModel: "c"})
	for i := 0; i < 14; i++ {
		converse(t, log, main, "第"+string(rune('A'+i))+"句")
	}
	mux := http.NewServeMux()
	sup.Routes(mux, func(p string, h http.HandlerFunc) { mux.HandleFunc(p, h) })
	do := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	var v ContextView
	w := do("GET", "/api/v1/threads/"+main+"/context", "")
	json.Unmarshal(w.Body.Bytes(), &v)
	if w.Code != 200 || v.Segment != 1 || v.Used == 0 || v.Window != DefaultContextWindow || v.Cache.Calls != 14 {
		t.Fatalf("context: %d %+v", w.Code, v)
	}
	if w := do("POST", "/api/v1/threads/"+main+"/compact", ""); w.Code != 202 {
		t.Fatalf("compact: %d", w.Code)
	}
	waitFor(t, func() bool {
		g, _ := log.Store.LatestSegment(context.Background(), main)
		return g.Segment == 2 && g.Reason == "manual" && g.CoversUpTo > 0
	})
	if w := do("PUT", "/api/v1/threads/"+main+"/summary", `{"summary":"## 待办和承诺\n- 用户改过的摘要"}`); w.Code != 202 {
		t.Fatalf("summary: %d", w.Code)
	}
	waitFor(t, func() bool {
		g, _ := log.Store.LatestSegment(context.Background(), main)
		return g.Segment == 3 && g.Reason == "edit" && strings.Contains(g.Summary, "用户改过")
	})
	if w := do("GET", "/api/v1/threads/nope/context", ""); w.Code != 404 {
		t.Fatalf("missing thread: %d", w.Code)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}
