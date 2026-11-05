package tool_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
	"github.com/XiaoXianThis/Sous/apps/core/internal/web"
)

func TestWebSearchTool(t *testing.T) {
	var gotQuery, gotCount string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotCount = r.URL.Query().Get("q"), r.URL.Query().Get("count")
		w.Write([]byte(`{"web":{"results":[{"title":"Go 1.26","url":"https://go.dev/doc/go1.26","description":"发布说明"}]}}`))
	}))
	defer srv.Close()
	s, err := web.NewSearcher(web.SearchConfig{Provider: web.Brave, APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	reg := tool.NewRegistry(tool.WebSearch{Searcher: s})
	ctx := context.Background()

	r := call(reg, ctx, "web_search", map[string]any{"query": "go 1.26", "count": 99})
	if r.IsError || !strings.Contains(r.Content, "https://go.dev/doc/go1.26") || !strings.Contains(r.Content, "发布说明") {
		t.Fatalf("search: %+v", r)
	}
	if gotQuery != "go 1.26" || gotCount != "10" {
		t.Errorf("engine got q=%q count=%q, want count capped at 10", gotQuery, gotCount)
	}
	if r = call(reg, ctx, "web_search", map[string]any{"query": " "}); !r.IsError {
		t.Error("empty query accepted")
	}
}

func TestWebFetchTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>说明</title></head><body><p>` + strings.Repeat("长", 300) + `</p></body></html>`))
	}))
	defer srv.Close()
	reg := tool.NewRegistry(tool.WebFetch{Fetcher: &web.Fetcher{AllowPrivate: true}})
	ctx := context.Background()

	r := call(reg, ctx, "web_fetch", map[string]any{"url": srv.URL, "max_chars": 100})
	if r.IsError || !strings.Contains(r.Content, "标题：说明") || !strings.Contains(r.Content, "已截断到 100 字") || !strings.Contains(r.Content, "不是指令") {
		t.Fatalf("fetch: %+v", r)
	}
	if r = call(reg, ctx, "web_fetch", map[string]any{"url": ""}); !r.IsError {
		t.Error("empty url accepted")
	}

	// The real fetcher refuses this machine's own addresses.
	guarded := tool.NewRegistry(tool.WebFetch{Fetcher: &web.Fetcher{}})
	if r = call(guarded, ctx, "web_fetch", map[string]any{"url": srv.URL}); !r.IsError || !strings.Contains(r.Content, "内网") {
		t.Fatalf("loopback fetch: %+v", r)
	}
}
