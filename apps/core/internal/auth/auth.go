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
	SessionCookie   = "sous_session"
	CSRFCookie      = "sous_csrf"
	CSRFHeader      = "X-CSRF-Token"
	ceremonyCookie  = "sous_ceremony"
	sessionTTL      = 30 * 24 * time.Hour
	ceremonyTTL     = 5 * time.Minute
	setupTokenKey   = "auth.setup_token_hash"
	setupExpiresKey = "auth.setup_token_expires"
	setupTokenTTL   = 24 * time.Hour
)

var ErrForbidden = errors.New("forbidden")

type Auth struct {
	st  *store.Store
	cfg *config.Hub
	wa  *webauthn.WebAuthn

	mu         sync.Mutex
	ceremonies map[string]ceremony
}

type ceremony struct {
	data    *webauthn.SessionData
	expires time.Time
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
	return &Auth{st: st, cfg: cfg, wa: wa, ceremonies: map[string]ceremony{}}, nil
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
	raw, err := a.st.Credentials(ctx)
	return len(raw) > 0, err
}

// NewSetupToken creates a one-time token for registering a passkey without
// being logged in, valid for 24 hours. It replaces any earlier token.
func (a *Auth) NewSetupToken(ctx context.Context) (string, error) {
	tok := randomToken()
	if err := a.st.SetSetting(ctx, setupTokenKey, hash(tok)); err != nil {
		return "", err
	}
	if err := a.st.SetSetting(ctx, setupExpiresKey, time.Now().Add(setupTokenTTL).Format(time.RFC3339)); err != nil {
		return "", err
	}
	return tok, nil
}

// SetupURL returns the link that opens the passkey setup page.
func (a *Auth) SetupURL(tok string) string {
	return a.cfg.PublicURL + "/setup?token=" + tok
}

func (a *Auth) validSetupToken(ctx context.Context, tok string) bool {
	if tok == "" {
		return false
	}
	want, err := a.st.Setting(ctx, setupTokenKey)
	if err != nil {
		return false
	}
	exp, err := a.st.Setting(ctx, setupExpiresKey)
	if err != nil {
		return false
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil || time.Now().After(t) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hash(tok)), []byte(want)) == 1
}

func (a *Auth) consumeSetupToken(ctx context.Context) {
	a.st.DeleteSetting(ctx, setupTokenKey)
	a.st.DeleteSetting(ctx, setupExpiresKey)
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
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return "", ErrForbidden
	}
	csrf, err := a.st.Session(r.Context(), hash(c.Value))
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
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		if at, err := a.st.SessionCreated(r.Context(), hash(c.Value)); err == nil && time.Since(at) < FreshLogin {
			return true
		}
	}
	writeError(w, http.StatusUnauthorized, "reauth", "这个操作需要重新验证 Passkey")
	return false
}

// Owner is an opaque id of the request's login session, for things that
// belong to one session (shells). It is not the session token, nor what the
// database keeps, so it is safe to hand to a machine.
func (a *Auth) Owner(r *http.Request) string {
	if t, ok := TunnelOf(r.Context()); ok && t.SID != "" {
		return hash("sous-session-owner:tunnel:" + t.SID)
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	return hash("sous-session-owner:" + hash(c.Value))
}

// login creates a session and sets its cookies.
func (a *Auth) login(w http.ResponseWriter, r *http.Request) error {
	tok, csrf := randomToken(), randomToken()
	exp := time.Now().Add(sessionTTL)
	if err := a.st.CreateSession(r.Context(), hash(tok), csrf, exp); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: tok, Path: "/", Expires: exp,
		HttpOnly: true, Secure: a.cfg.Secure(), SameSite: http.SameSiteLaxMode})
	// The CSRF cookie is readable by the page, which echoes it in a header.
	http.SetCookie(w, &http.Cookie{Name: CSRFCookie, Value: csrf, Path: "/", Expires: exp,
		Secure: a.cfg.Secure(), SameSite: http.SameSiteStrictMode})
	return nil
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		a.st.DeleteSession(r.Context(), hash(c.Value))
	}
	for _, name := range []string{SessionCookie, CSRFCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1})
	}
}

func (a *Auth) putCeremony(w http.ResponseWriter, data *webauthn.SessionData) {
	id := randomToken()
	a.mu.Lock()
	now := time.Now()
	for k, c := range a.ceremonies {
		if now.After(c.expires) {
			delete(a.ceremonies, k)
		}
	}
	a.ceremonies[id] = ceremony{data: data, expires: now.Add(ceremonyTTL)}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: ceremonyCookie, Value: id, Path: "/api/v1/auth/", MaxAge: int(ceremonyTTL.Seconds()),
		HttpOnly: true, Secure: a.cfg.Secure(), SameSite: http.SameSiteStrictMode})
}

func (a *Auth) takeCeremony(r *http.Request) (*webauthn.SessionData, error) {
	c, err := r.Cookie(ceremonyCookie)
	if err != nil {
		return nil, ErrForbidden
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cer, ok := a.ceremonies[c.Value]
	delete(a.ceremonies, c.Value)
	if !ok || time.Now().After(cer.expires) {
		return nil, ErrForbidden
	}
	return cer.data, nil
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
