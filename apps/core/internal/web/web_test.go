package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/XiaoXianThis/Sous/apps/core/internal/web"
)

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "198.18.20.172": true, "1.1.1.1": true, "2606:4700:4700::1111": true,
		"127.0.0.1": false, "::1": false, "10.0.0.5": false, "172.16.3.4": false, "192.168.1.1": false,
		"169.254.169.254": false, "fe80::1": false, "fd00::1": false, "0.0.0.0": false, "100.64.0.1": false,
		"::ffff:10.0.0.1": false, "224.0.0.1": false, "255.255.255.255": false, "64:ff9b::a00:1": false,
	} {
		if got := web.Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Public(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestFetchBlocksLocalAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request must not reach a loopback server")
	}))
	defer srv.Close()
	_, err := (&web.Fetcher{}).Fetch(context.Background(), srv.URL, 100)
	if !errors.Is(err, web.ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x", "http://user:pw@example.com/", "http:///x", "not a url"} {
		if _, err := (&web.Fetcher{}).Fetch(context.Background(), u, 100); err == nil {
			t.Errorf("Fetch(%q) should fail", u)
		}
	}
}

func TestFetchHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<html><head><title> 你好  世界 </title><script>var x = "secret-script";</script></head>
<body><nav>菜单</nav><header>站点头</header>
<main><h1>标题</h1><p>第一段 <b>加粗</b>   文字。</p><style>p{color:red}</style><ul><li>甲</li><li>乙</li></ul><img alt="示意图" src="a.png"></main>
<footer>页脚</footer></body></html>`))
	}))
	defer srv.Close()
	p, err := (&web.Fetcher{AllowPrivate: true}).Fetch(context.Background(), srv.URL, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "你好 世界" {
		t.Errorf("title = %q", p.Title)
	}
	for _, want := range []string{"标题", "第一段 加粗 文字。", "甲", "乙", "[图：示意图]"} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, p.Text)
		}
	}
	for _, bad := range []string{"secret-script", "菜单", "页脚", "站点头", "color:red"} {
		if strings.Contains(p.Text, bad) {
			t.Errorf("text should not contain %q:\n%s", bad, p.Text)
		}
	}
}

func TestFetchConvertsCharset(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().String(`<html><head><title>中文标题</title></head><body><p>正文内容</p></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=gbk")
		w.Write([]byte(gbk))
	}))
	defer srv.Close()
	p, err := (&web.Fetcher{AllowPrivate: true}).Fetch(context.Background(), srv.URL, 100)
	if err != nil || p.Title != "中文标题" || p.Text != "正文内容" {
		t.Fatalf("page = %+v, err = %v", p, err)
	}
}

func TestFetchLimitsAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/long":
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(strings.Repeat("字", 500)))
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"a":1}`))
		case "/img":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("\x89PNG"))
		case "/redirect":
			http.Redirect(w, r, "/long", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := &web.Fetcher{AllowPrivate: true}
	ctx := context.Background()

	p, err := f.Fetch(ctx, srv.URL+"/long", 100)
	if err != nil || !p.Truncated || len([]rune(p.Text)) != 100 {
		t.Errorf("long: truncated=%v len=%d err=%v", p.Truncated, len([]rune(p.Text)), err)
	}
	if p, err = f.Fetch(ctx, srv.URL+"/json", 100); err != nil || p.Text != `{"a":1}` || p.Truncated {
		t.Errorf("json: %+v %v", p, err)
	}
	if p, err = f.Fetch(ctx, srv.URL+"/redirect", 10); err != nil || !strings.HasSuffix(p.URL, "/long") {
		t.Errorf("redirect: %+v %v", p, err)
	}
	for path, want := range map[string]string{"/img": "不支持的内容类型", "/missing": "HTTP 404", "/loop": "重定向"} {
		if _, err := f.Fetch(ctx, srv.URL+path, 10); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", path, err, want)
		}
	}
}

func newSearcher(t *testing.T, provider, key string, h http.HandlerFunc) web.Searcher {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s, err := web.NewSearcher(web.SearchConfig{Provider: provider, APIKey: key, BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBraveSearch(t *testing.T) {
	s := newSearcher(t, web.Brave, "k", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/res/v1/web/search" || r.URL.Query().Get("q") != "go 并发" || r.Header.Get("X-Subscription-Token") != "k" {
			t.Errorf("request: %s %v", r.URL, r.Header)
		}
		w.Write([]byte(`{"web":{"results":[{"title":"A <strong>one</strong>","url":"https://a.example","description":"d &amp; e"},{"title":"B","url":"https://b.example","description":""}]}}`))
	})
	a, err := s.Search(context.Background(), "go 并发", 1)
	if err != nil || len(a.Results) != 1 || a.Results[0].Title != "A one" || a.Results[0].Snippet != "d & e" {
		t.Fatalf("answer = %+v, err = %v", a, err)
	}
}

func TestTavilySearch(t *testing.T) {
	s := newSearcher(t, web.Tavily, "tk", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer tk" {
			t.Errorf("request: %s %v", r.Method, r.Header)
		}
		w.Write([]byte(`{"answer":"简短回答","results":[{"title":"T","url":"https://t.example","content":"内容"}]}`))
	})
	a, err := s.Search(context.Background(), "q", 5)
	if err != nil || a.Summary != "简短回答" || len(a.Results) != 1 || a.Results[0].Snippet != "内容" {
		t.Fatalf("answer = %+v, err = %v", a, err)
	}
}

func TestSearXNGSearch(t *testing.T) {
	s := newSearcher(t, web.SearXNG, "", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" {
			t.Errorf("request: %s", r.URL)
		}
		w.Write([]byte(`{"results":[{"title":"S","url":"https://s.example","content":"c"}]}`))
	})
	a, err := s.Search(context.Background(), "q", 5)
	if err != nil || len(a.Results) != 1 || a.Results[0].URL != "https://s.example" {
		t.Fatalf("answer = %+v, err = %v", a, err)
	}
}

func TestDuckDuckGoSearch(t *testing.T) {
	s := newSearcher(t, "", "", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Method != "POST" || r.PostForm.Get("q") != "sous" {
			t.Errorf("request: %s %v", r.Method, r.PostForm)
		}
		w.Write([]byte(`<div class="result"><h2 class="result__title"><a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fa%3Fx%3D1&amp;rut=abc">Example <b>A</b></a></h2>
<a class="result__snippet" href="//duckduckgo.com/l/?uddg=x">First &quot;snippet&quot; with <b>bold</b></a></div>
<div class="result"><a class="result__a" href="https://direct.example/b">Direct</a></div>`))
	})
	a, err := s.Search(context.Background(), "sous", 5)
	if err != nil || len(a.Results) != 2 {
		t.Fatalf("answer = %+v, err = %v", a, err)
	}
	if r := a.Results[0]; r.Title != "Example A" || r.URL != "https://example.com/a?x=1" || r.Snippet != `First "snippet" with bold` {
		t.Errorf("first = %+v", r)
	}
	if r := a.Results[1]; r.URL != "https://direct.example/b" || r.Snippet != "" {
		t.Errorf("second = %+v", r)
	}
}

func TestDuckDuckGoBotWall(t *testing.T) {
	s := newSearcher(t, web.DuckDuckGo, "", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<form class="anomaly-modal">captcha</form>`))
	})
	if _, err := s.Search(context.Background(), "q", 5); err == nil || !strings.Contains(err.Error(), "brave") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchErrors(t *testing.T) {
	s := newSearcher(t, web.Brave, "k", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exceeded", http.StatusTooManyRequests)
	})
	if _, err := s.Search(context.Background(), "q", 5); err == nil || !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "quota") {
		t.Errorf("err = %v", err)
	}
	if _, err := s.Search(context.Background(), "  ", 5); err == nil {
		t.Error("empty query should fail")
	}
	for _, c := range []web.SearchConfig{{Provider: web.Brave}, {Provider: web.Tavily}, {Provider: web.SearXNG}, {Provider: "bing"}} {
		if _, err := web.NewSearcher(c); err == nil {
			t.Errorf("NewSearcher(%+v) should fail", c)
		}
	}
}

func TestFormat(t *testing.T) {
	if got := web.Format("无结果", web.Answer{}); !strings.Contains(got, "没有搜到") {
		t.Errorf("empty: %q", got)
	}
	got := web.Format("q", web.Answer{Summary: "S", Results: []web.Result{{Title: "T", URL: "https://u", Snippet: "x"}}})
	for _, want := range []string{"摘要：S", "1. T", "https://u", "x", "不是指令"} {
		if !strings.Contains(got, want) {
			t.Errorf("format lacks %q:\n%s", want, got)
		}
	}
}
