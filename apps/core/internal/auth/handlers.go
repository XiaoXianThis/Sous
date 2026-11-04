package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Routes registers the auth endpoints under /api/v1/auth and /api/v1/me.
func (a *Auth) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me", a.handleMe)
	mux.HandleFunc("POST /api/v1/auth/passkey/register/begin", a.handleRegisterBegin)
	mux.HandleFunc("POST /api/v1/auth/passkey/register/finish", a.handleRegisterFinish)
	mux.HandleFunc("POST /api/v1/auth/passkey/login/begin", a.handleLoginBegin)
	mux.HandleFunc("POST /api/v1/auth/passkey/login/finish", a.handleLoginFinish)
	mux.HandleFunc("POST /api/v1/auth/logout", a.handleLogout)
	mux.HandleFunc("POST /api/v1/auth/dev-login", a.handleDevLogin)
}

func (a *Auth) handleMe(w http.ResponseWriter, r *http.Request) {
	_, err := a.Session(r)
	has, herr := a.HasPasskey(r.Context())
	if herr != nil {
		writeError(w, http.StatusInternalServerError, "internal", herr.Error())
		return
	}
	writeJSON(w, map[string]any{
		"logged_in":   err == nil,
		"has_passkey": has,
		"dev_login":   a.cfg.Dev && isLoopback(r),
	})
}

// Registering needs a login, or a valid one-time setup token.
func (a *Auth) handleRegisterBegin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SetupToken string `json:"setup_token"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	_, loggedIn := a.Session(r)
	switch {
	case a.validSetupToken(r.Context(), body.SetupToken):
	case loggedIn != nil:
		writeError(w, http.StatusForbidden, "setup_token", "注册链接无效或已过期，请在服务器上执行 sous-hub admin passkey-reset")
		return
	case !a.RequireFresh(w, r):
		// Adding a passkey while logged in needs a recent passkey login,
		// so a stolen cookie cannot plant a passkey of its own.
		return
	}
	o, err := a.owner(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	opts, data, err := a.wa.BeginRegistration(o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "webauthn", err.Error())
		return
	}
	a.putCeremony(w, data)
	writeJSON(w, opts)
}

func (a *Auth) handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	data, err := a.takeCeremony(r)
	if err != nil {
		writeError(w, http.StatusForbidden, "ceremony", "注册流程已过期，请重试")
		return
	}
	o, err := a.owner(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cred, err := a.wa.FinishRegistration(o, *data, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "webauthn", err.Error())
		return
	}
	raw, _ := json.Marshal(cred)
	if err := a.st.PutCredential(r.Context(), string(cred.ID), string(raw)); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	a.consumeSetupToken(r.Context())
	if err := a.login(w, r); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	slog.Info("auth: passkey registered")
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *Auth) handleLoginBegin(w http.ResponseWriter, r *http.Request) {
	o, err := a.owner(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if len(o.creds) == 0 {
		writeError(w, http.StatusConflict, "no_passkey", "还没有注册 Passkey")
		return
	}
	opts, data, err := a.wa.BeginLogin(o, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "webauthn", err.Error())
		return
	}
	a.putCeremony(w, data)
	writeJSON(w, opts)
}

func (a *Auth) handleLoginFinish(w http.ResponseWriter, r *http.Request) {
	data, err := a.takeCeremony(r)
	if err != nil {
		writeError(w, http.StatusForbidden, "ceremony", "登录流程已过期，请重试")
		return
	}
	o, err := a.owner(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cred, err := a.wa.FinishLogin(o, *data, r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "webauthn", err.Error())
		return
	}
	raw, _ := json.Marshal(cred) // keeps the updated sign count
	a.st.PutCredential(r.Context(), string(cred.ID), string(raw))
	// Verifying again while logged in refreshes the session in place, so
	// what belongs to it (open shells) stays reachable.
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		if a.st.RefreshSession(r.Context(), hash(c.Value)) == nil {
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
	}
	if err := a.login(w, r); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (a *Auth) handleLogout(w http.ResponseWriter, r *http.Request) {
	a.logout(w, r)
	writeJSON(w, map[string]bool{"ok": true})
}

// handleDevLogin logs in without a passkey. Only with dev: true, and only for
// requests that come straight from this machine.
func (a *Auth) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.Dev || !isLoopback(r) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" && a.st.RefreshSession(r.Context(), hash(c.Value)) == nil {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if err := a.login(w, r); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
