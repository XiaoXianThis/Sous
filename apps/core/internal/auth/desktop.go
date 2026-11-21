package auth

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"
)

const desktopGrantTTL = 20 * time.Second
const desktopSessionTTL = 12 * time.Hour
const desktopDebugSessionTTL = 2 * time.Hour

type desktopSession struct {
	id, csrf         string
	created, expires time.Time
	native           bool
}

type desktopAuth struct {
	mu          sync.Mutex
	grantHash   string
	grantExpiry time.Time
	grantNative bool
	active      desktopSession
}

// DesktopGrant is called only by the app-managed inherited-pipe handler, not
// a web endpoint. It does not read or create any persistent credentials.
func (a *Auth) DesktopGrant(native bool) (string, error) {
	if !a.cfg.LocalDesktopAuth {
		return "", ErrForbidden
	}
	token := randomToken()
	a.desktop.mu.Lock()
	defer a.desktop.mu.Unlock()
	a.desktop.grantHash = hash(token)
	a.desktop.grantExpiry = time.Now().Add(desktopGrantTTL)
	a.desktop.grantNative = native
	return token, nil
}

func (d *desktopAuth) session(id string) (desktopSession, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active, d.active.id != "" && id == d.active.id && time.Now().Before(d.active.expires)
}

func (d *desktopAuth) revoke(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id == "" || id != d.active.id {
		return false
	}
	d.active = desktopSession{}
	return true
}

func (a *Auth) LockDesktop() {
	a.desktop.mu.Lock()
	a.desktop.grantHash = ""
	old := a.desktop.active.id
	a.desktop.active = desktopSession{}
	a.desktop.mu.Unlock()
	a.endStreams(old)
}

func (a *Auth) handleDesktopLogin(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) || r.Header.Get("Origin") != a.cfg.PublicURL {
		writeError(w, http.StatusForbidden, "origin", "请在本机 Sous 桌面程序中登录")
		return
	}
	kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var body struct {
		Grant string `json:"grant"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256))
	d.DisallowUnknownFields()
	var extra json.RawMessage
	if kind != "application/json" || d.Decode(&body) != nil || len(body.Grant) != 43 || !errors.Is(d.Decode(&extra), io.EOF) {
		writeError(w, http.StatusBadRequest, "grant", "无效的桌面登录请求")
		return
	}
	token, csrf := randomToken(), randomToken()
	now := time.Now()
	a.desktop.mu.Lock()
	if now.After(a.desktop.grantExpiry) || subtle.ConstantTimeCompare([]byte(hash(body.Grant)), []byte(a.desktop.grantHash)) != 1 {
		a.desktop.mu.Unlock()
		writeError(w, http.StatusForbidden, "grant", "桌面登录授权已失效，请重新验证")
		return
	}
	a.desktop.grantHash = ""
	if c, err := r.Cookie(a.cookieName(SessionCookie)); err == nil && a.desktop.grantNative &&
		a.desktop.active.native && hash(c.Value) == a.desktop.active.id && now.Before(a.desktop.active.expires) {
		a.desktop.active.created = now
		a.desktop.mu.Unlock()
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	old := a.desktop.active.id
	age := desktopDebugSessionTTL
	if a.desktop.grantNative {
		age = desktopSessionTTL
	}
	expires := now.Add(age)
	a.desktop.active = desktopSession{id: hash(token), csrf: csrf, created: now, expires: expires, native: a.desktop.grantNative}
	a.desktop.mu.Unlock()
	a.endStreams(old)
	a.setSessionCookies(w, token, csrf, expires)
	writeJSON(w, map[string]bool{"ok": true})
}
