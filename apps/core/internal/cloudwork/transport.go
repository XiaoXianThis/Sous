package cloudwork

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// MachineHTTP is the shared HTTPS transport for non-browser workspace roles.
// It deliberately does not follow redirects, load browser cookies, or use an
// inherited proxy. A loopback override is for reverse proxies and test TLS.
func MachineHTTP(origin, caPath, socket string) (*http.Client, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(origin, "#") {
		return nil, errors.New("需要专用 HTTPS 云工作区地址")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if caPath != "" {
		f, err := os.Open(caPath)
		if err != nil {
			return nil, err
		}
		raw, err := io.ReadAll(io.LimitReader(f, 128<<10))
		_ = f.Close()
		if err != nil || !roots.AppendCertsFromPEM(raw) {
			return nil, errors.New("无法读取测试或私有 CA")
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true, MaxIdleConnsPerHost: 4, IdleConnTimeout: time.Minute}
	if socket != "" {
		host, port, err := net.SplitHostPort(socket)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() || port == "" {
			return nil, errors.New("private dial override requires a numeric loopback address")
		}
		dialer := net.Dialer{Timeout: 5 * time.Second}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", socket)
		}
	}
	return &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
