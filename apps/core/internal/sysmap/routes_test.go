package sysmap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutes(t *testing.T) {
	Reset()
	mux := http.NewServeMux()
	Routes(mux, func(pattern string, h http.HandlerFunc) { mux.HandleFunc(pattern, h) }, nil, TraceDeps{})
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	w := get("/api/v1/system/graph")
	var graph Graph
	if err := json.Unmarshal(w.Body.Bytes(), &graph); err != nil || len(graph.Modules) != 16 {
		t.Fatalf("%v %d", err, len(graph.Modules))
	}
	if w = get("/api/v1/system/trace?task_id=t&run_id=r"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	Count("ui>httpapi", Refs{TaskID: "t"}, "one")
	Count("httpapi>eventlog", Refs{TaskID: "t"}, "two")
	w = get("/api/v1/system/trace?task_id=t")
	var trace struct {
		Steps []traceStep `json:"steps"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &trace); err != nil || len(trace.Steps) != 2 || trace.Steps[0].Summary != "one" || trace.Steps[1].Summary != "two" {
		t.Fatalf("%v %+v", err, trace)
	}
}
