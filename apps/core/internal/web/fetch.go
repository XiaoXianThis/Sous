// Package web gives the supervisor access to the internet: searching it and
// reading pages. The Hub often runs on a server next to private services, so
// everything a page can make the Hub connect to is checked (see Fetcher).
package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html/charset"
)

const (
	defaultTimeout  = 20 * time.Second
	defaultMaxBytes = 2 << 20 // bytes read from a page
	maxRedirects    = 5
	userAgent       = "Mozilla/5.0 (compatible; sous/1.0; personal assistant)"
)

// ErrBlocked means the address is not public: loopback, private, link-local
// (cloud metadata lives there) and other special ranges.
var ErrBlocked = errors.New("不允许访问内网或本机地址")

// Fetcher downloads pages for the model to read.
//
// A page is untrusted: it can link, redirect or resolve to an address inside
// the Hub's network. The address check therefore happens on the connection
// itself, after DNS, for every redirect hop, so DNS rebinding cannot get
// around it. Proxies from the environment are ignored for the same reason.
type Fetcher struct {
	// AllowPrivate turns the address check off. Tests only.
	AllowPrivate bool
	Timeout      time.Duration
	MaxBytes     int64

	client *http.Client
}

// Page is a fetched page, already reduced to text.
type Page struct {
	URL         string // after redirects
	Title       string
	Text        string
	ContentType string
	Truncated   bool // the text was cut at the limit asked for
}

// 198.18.0.0/15 is deliberately not here: fake-IP proxies (Clash, Surge) answer
// every DNS lookup with an address in it, and no real service lives there.
var reserved = mustPrefixes(
	"100.64.0.0/10",   // carrier-grade NAT
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // documentation
	"198.51.100.0/24", // documentation
	"203.0.113.0/24",  // documentation
	"240.0.0.0/4",     // reserved
	"64:ff9b::/96",    // NAT64: embeds an IPv4 address
	"2001:db8::/32",   // documentation
	"2002::/16",       // 6to4: embeds an IPv4 address
)

func mustPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, p := range s {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// Public reports whether a is an address on the public internet.
func Public(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() {
		return false
	}
	for _, p := range reserved {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

func (f *Fetcher) http() *http.Client {
	if f.client != nil {
		return f.client
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		// Control runs once the host name is resolved, right before connecting.
		Control: func(network, address string, _ syscall.RawConn) error {
			if f.AllowPrivate {
				return nil
			}
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !Public(ap.Addr()) {
				return ErrBlocked
			}
			return nil
		},
	}
	f.client = &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			DisableKeepAlives:     true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("重定向超过 %d 次", maxRedirects)
			}
			return checkURL(req.URL)
		},
	}
	return f.client
}

func checkURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("只支持 http 和 https 链接")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("链接里没有主机名")
	}
	if u.User != nil {
		return fmt.Errorf("链接里不能带用户名和密码")
	}
	return nil
}

func textual(mt string) bool {
	switch {
	case strings.HasPrefix(mt, "text/"), mt == "application/json", mt == "application/xml",
		mt == "application/xhtml+xml", mt == "application/rss+xml", mt == "application/atom+xml",
		strings.HasSuffix(mt, "+json"), strings.HasSuffix(mt, "+xml"):
		return true
	}
	return false
}

// Fetch downloads rawURL and returns its text, cut to maxChars characters.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string, maxChars int) (Page, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Page{}, fmt.Errorf("链接无效：%w", err)
	}
	if err := checkURL(u); err != nil {
		return Page{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Page{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.1")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := f.http().Do(req)
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return Page{}, ErrBlocked
		}
		return Page{}, fmt.Errorf("请求失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Page{}, fmt.Errorf("网站返回 HTTP %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	mt, _, _ := mime.ParseMediaType(ct)
	if mt == "" {
		mt = "text/html" // many servers leave it out; the parser copes with either
	}
	if !textual(mt) {
		return Page{}, fmt.Errorf("不支持的内容类型 %s（只能读网页和文本）", mt)
	}
	limit := f.MaxBytes
	if limit <= 0 {
		limit = defaultMaxBytes
	}
	// The charset reader turns GBK, Big5 and the like into UTF-8.
	body, err := charset.NewReader(io.LimitReader(resp.Body, limit), ct)
	if err != nil {
		return Page{}, err
	}
	page := Page{URL: resp.Request.URL.String(), ContentType: mt}
	if mt == "text/html" || mt == "application/xhtml+xml" {
		if page.Title, page.Text, err = HTMLText(body); err != nil {
			return Page{}, fmt.Errorf("解析网页失败：%w", err)
		}
	} else {
		raw, err := io.ReadAll(body)
		if err != nil {
			return Page{}, fmt.Errorf("读取失败：%w", err)
		}
		page.Text = strings.ToValidUTF8(strings.TrimSpace(string(raw)), "")
	}
	page.Text, page.Truncated = Truncate(page.Text, maxChars)
	return page, nil
}
