// Package auth handles the single user's login: passkeys (WebAuthn), cookie
// sessions with a CSRF token, the one-time setup link, and a loopback-only
// dev login.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const (
	SessionCookie  = "sous_session"
	CSRFCookie     = "sous_csrf"
	CSRFHeader     = "X-CSRF-Token"
	ceremonyCookie = "sous_ceremony"
	sessionTTL     = 30 * 24 * time.Hour
	ceremonyTTL    = 5 * time.Minute
	setupTokenTTL  = 24 * time.Hour
	hostedSetupTTL = 15 * time.Minute
	maxCeremonies  = 512
)

var ErrForbidden = errors.New("forbidden")

type Auth struct {
	st  *store.Store
	cfg *config.Hub
	wa  *webauthn.WebAuthn

	desktop    desktopAuth
	mu         sync.Mutex
	ceremonies map[string]ceremony
	streamMu   sync.Mutex
	streams    map[string]map[context.Context]context.CancelFunc
}

type ceremony struct {
	data      *webauthn.SessionData
	expires   time.Time
	register  bool
	setupHash string
	owner     string
}

func New(st *store.Store, cfg *config.Hub) (*Auth, error) {
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID(),
		RPDisplayName: "sous",
		RPOrigins:     []string{cfg.PublicURL},
		// A passkey is the only factor, so it must verify the user
		// (fingerprint, face, PIN), not just that someone touched it.
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, err
	}
	return &Auth{st: st, cfg: cfg, wa: wa, ceremonies: map[string]ceremony{}, streams: make(map[string]map[context.Context]context.CancelFunc)}, nil
}

// owner is the only WebAuthn user.
type owner struct{ creds []webauthn.Credential }

func (owner) WebAuthnID() []byte                           { return []byte("sous-owner") }
func (owner) WebAuthnName() string                         { return "owner" }
func (owner) WebAuthnDisplayName() string                  { return "sous 主人" }
func (o owner) WebAuthnCredentials() []webauthn.Credential { return o.creds }

func (a *Auth) owner(ctx context.Context) (owner, error) {
	raw, err := a.st.Credentials(ctx)
	if err != nil {
		return owner{}, err
	}
	var o owner
	for _, r := range raw {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(r), &c); err != nil {
			return owner{}, err
		}
		o.creds = append(o.creds, c)
	}
	return o, nil
}

// HasPasskey reports whether any passkey is registered.
func (a *Auth) HasPasskey(ctx context.Context) (bool, error) {
	return a.st.HasCredentials(ctx)
}

// NewSetupToken creates a one-time token for registering a passkey without
// being logged in, valid for 24 hours. It replaces any earlier token.
func (a *Auth) NewSetupToken(ctx context.Context) (string, error) {
	tok := randomToken()
	ttl := setupTokenTTL
	if a.cfg.HostedWorkspace {
		ttl = hostedSetupTTL
	}
	if err := a.st.ReplacePasskeySetup(ctx, hash(tok), time.Now().Add(ttl)); err != nil {
		return "", err
	}
	return tok, nil
}

// SetupURL keeps direct workspace enrollment credentials in a fragment, which
// is not part of the URL seen by the HTTP server or reverse proxy.
func (a *Auth) SetupURL(tok string) string {
	if a.cfg.DirectWorkspace() {
		return a.cfg.PublicURL + "/setup#token=" + tok
	}
	return a.cfg.PublicURL + "/setup?token=" + tok
}

func (a *Auth) setupHash(ctx context.Context, tok string) string {
	if len(tok) != 43 {
		return ""
	}
	want, expires, err := a.st.PasskeySetup(ctx)
	if err != nil || !time.Now().Before(expires) {
		return ""
	}
	sum := hash(tok)
	if subtle.ConstantTimeCompare([]byte(sum), []byte(want)) != 1 {
		return ""
	}
	return sum
}

// ResetPasskeys is the recovery path for `sous-hub admin passkey-reset`.
// With revokeAll it also removes every passkey and logs out every session.
func (a *Auth) ResetPasskeys(ctx context.Context, revokeAll bool) (string, error) {
	if revokeAll {
		if err := a.st.DeleteCredentials(ctx); err != nil {
			return "", err
		}
		if err := a.st.DeleteSessions(ctx); err != nil {
			return "", err
		}
	}
	return a.NewSetupToken(ctx)
}

// Session returns the CSRF token of the request's session, or ErrForbidden.
func (a *Auth) Session(r *http.Request) (string, error) {
	if t, ok := TunnelOf(r.Context()); ok && t.SID != "" {
		return "", nil
	}
	c, err := r.Cookie(a.cookieName(SessionCookie))
	if err != nil || c.Value == "" {
		return "", ErrForbidden
	}
	id := hash(c.Value)
	if session, ok := a.desktop.session(id); ok {
		return session.csrf, nil
	}
	csrf, err := a.st.Session(r.Context(), id)
	if err != nil {
		return "", ErrForbidden
	}
	return csrf, nil
}

// Require wraps handlers that need a logged-in user. Requests other than GET
// and HEAD must also carry the session's CSRF token in X-CSRF-Token.
func (a *Auth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if t, ok := TunnelOf(r.Context()); ok && t.SID != "" {
			next.ServeHTTP(w, r) // no cookie, so no CSRF to check
			return
		}
		csrf, err := a.Session(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			got := r.Header.Get(CSRFHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(csrf)) != 1 {
				writeError(w, http.StatusForbidden, "csrf", "CSRF 校验失败")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// FreshLogin is how recent a passkey login must be for RequireFresh.
const FreshLogin = 15 * time.Minute

// RequireFresh reports whether the request's session logged in with a
// passkey within FreshLogin, and otherwise answers 401 "reauth": the page
// then asks for the passkey again and retries. Guards what a stolen cookie
// must not be enough for, such as opening a shell on a machine.
func (a *Auth) RequireFresh(w http.ResponseWriter, r *http.Request) bool {
	if t, ok := TunnelOf(r.Context()); ok {
		if t.Fresh {
			return true
		}
		writeError(w, http.StatusUnauthorized, "reauth", "这个操作需要你刷一次 Face ID")
		return false
	}
	if c, err := r.Cookie(a.cookieName(SessionCookie)); err == nil && c.Value != "" {
		id := hash(c.Value)
		if session, ok := a.desktop.session(id); ok && session.native && time.Since(session.created) < FreshLogin {
			return true
		}
		if at, err := a.st.SessionCreated(r.Context(), id); err == nil && time.Since(at) < FreshLogin {
			return true
		}
	}
	if a.cfg.LocalDesktopAuth {
		writeError(w, http.StatusUnauthorized, "reauth", "这个操作需要重新验证系统身份")
	} else {
		writeError(w, http.StatusUnauthorized, "reauth", "这个操作需要重新验证 Passkey")
	}
	return false
}

// Owner is an opaque id of the request's login session, for things that
// belong to one session (shells). It is not the session token, nor what the
// database keeps, so it is safe to hand to a machine.
func (a *Auth) Owner(r *http.Request) string {
	if t, ok := TunnelOf(r.Context()); ok && t.SID != "" {
		return hash("sous-session-owner:tunnel:" + t.SID)
	}
	c, err := r.Cookie(a.cookieName(SessionCookie))
	if err != nil || c.Value == "" {
		return ""
	}
	return hash("sous-session-owner:" + hash(c.Value))
}

func (a *Auth) cookieName(name string) string {
	if a.cfg.HostedWorkspace {
		return "__Host-" + name
	}
	return name
}

// login creates a session and sets its cookies.
func (a *Auth) login(w http.ResponseWriter, r *http.Request) error {
	tok, csrf := randomToken(), randomToken()
	exp := time.Now().Add(sessionTTL)
	if err := a.st.CreateSession(r.Context(), hash(tok), csrf, exp); err != nil {
		return err
	}
	a.setSessionCookies(w, tok, csrf, exp)
	return nil
}

func (a *Auth) setSessionCookies(w http.ResponseWriter, tok, csrf string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(SessionCookie), Value: tok, Path: "/", Expires: exp,
		HttpOnly: true, Secure: a.cfg.Secure(), SameSite: http.SameSiteLaxMode})
	// The CSRF cookie is readable by the page, which echoes it in a header.
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(CSRFCookie), Value: csrf, Path: "/", Expires: exp,
		Secure: a.cfg.Secure(), SameSite: http.SameSiteStrictMode})
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(a.cookieName(SessionCookie)); err == nil {
		id := hash(c.Value)
		if !a.desktop.revoke(id) {
			if err := a.st.DeleteSession(r.Context(), id); err != nil {
				return err
			}
		}
		a.endStreams(id)
	}
	for _, name := range []string{SessionCookie, CSRFCookie} {
		http.SetCookie(w, &http.Cookie{Name: a.cookieName(name), Value: "", Path: "/", MaxAge: -1, Secure: a.cfg.Secure()})
	}
	return nil
}

func (a *Auth) putCeremony(w http.ResponseWriter, r *http.Request, c ceremony) bool {
	id := randomToken()
	a.mu.Lock()
	now := time.Now()
	if old, err := r.Cookie(a.cookieName(ceremonyCookie)); err == nil {
		delete(a.ceremonies, old.Value)
	}
	for key, pending := range a.ceremonies {
		if !now.Before(pending.expires) {
			delete(a.ceremonies, key)
		}
	}
	if len(a.ceremonies) >= maxCeremonies {
		a.mu.Unlock()
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusTooManyRequests, "ceremonies_busy", "身份验证请求过多，请稍后重试")
		return false
	}
	c.expires = now.Add(ceremonyTTL)
	a.ceremonies[id] = c
	a.mu.Unlock()
	cookiePath := "/api/v1/auth/"
	if a.cfg.HostedWorkspace {
		cookiePath = "/"
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(ceremonyCookie), Value: id, Path: cookiePath, MaxAge: int(ceremonyTTL.Seconds()),
		HttpOnly: true, Secure: a.cfg.Secure(), SameSite: http.SameSiteStrictMode})
	return true
}

func (a *Auth) takeCeremony(r *http.Request, register bool) (ceremony, error) {
	c, err := r.Cookie(a.cookieName(ceremonyCookie))
	if err != nil {
		return ceremony{}, ErrForbidden
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cer, ok := a.ceremonies[c.Value]
	delete(a.ceremonies, c.Value)
	if !ok || cer.register != register || !time.Now().Before(cer.expires) {
		return ceremony{}, ErrForbidden
	}
	return cer, nil
}

// isLoopback reports whether r came directly from this machine.
func isLoopback(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("auth: random: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
