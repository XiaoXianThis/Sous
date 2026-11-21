package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"

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
	if a.cfg.DirectWorkspace() {
		mux.Handle("POST /api/v1/auth/logout", a.Require(http.HandlerFunc(a.handleLogout)))
	} else {
		mux.HandleFunc("POST /api/v1/auth/logout", a.handleLogout)
	}
	mux.HandleFunc("POST /api/v1/auth/dev-login", a.handleDevLogin)
	if a.cfg.LocalDesktopAuth {
		mux.HandleFunc("POST /api/v1/auth/desktop", a.handleDesktopLogin)
	}
}

func (a *Auth) handleMe(w http.ResponseWriter, r *http.Request) {
	_, err := a.Session(r)
	has, herr := a.HasPasskey(r.Context())
	if herr != nil {
		writeError(w, http.StatusInternalServerError, "internal", "暂时无法读取工作区登录状态")
		return
	}
	writeJSON(w, map[string]any{
		"logged_in":        err == nil,
		"has_passkey":      has,
		"dev_login":        a.cfg.Dev && isLoopback(r),
		"local_workspace":  a.cfg.LocalWorkspace,
		"hosted_workspace": a.cfg.HostedWorkspace,
		"desktop_auth":     a.cfg.LocalDesktopAuth,
	})
}

// Registering needs a login, or a valid one-time setup token.
func (a *Auth) handleRegisterBegin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SetupToken string `json:"setup_token"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	_, loggedIn := a.Session(r)
	setup := a.setupHash(r.Context(), body.SetupToken)
	switch {
	case setup != "":
	case loggedIn != nil:
		writeError(w, http.StatusForbidden, "setup_token", "注册链接无效或已过期，请使用管理员或桌面应用提供的新链接")
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
	if a.cfg.HostedWorkspace && setup != "" && len(o.creds) != 0 {
		writeError(w, http.StatusForbidden, "setup_token", "工作区已经完成首次注册")
		return
	}
	opts, data, err := a.wa.BeginRegistration(o)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "webauthn", err.Error())
		return
	}
	cer := ceremony{data: data, register: true, setupHash: setup}
	if setup == "" {
		cer.owner = a.Owner(r)
	}
	if a.putCeremony(w, r, cer) {
		writeJSON(w, opts)
	}
}

func (a *Auth) handleRegisterFinish(w http.ResponseWriter, r *http.Request) {
	cer, err := a.takeCeremony(r, true)
	if err != nil {
		writeError(w, http.StatusForbidden, "ceremony", "注册流程已过期，请重试")
		return
	}
	o, err := a.owner(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	if cer.setupHash == "" {
		if cer.owner == "" || cer.owner != a.Owner(r) {
			writeError(w, http.StatusForbidden, "ceremony", "注册必须由发起它的登录会话完成")
			return
		}
		if !a.RequireFresh(w, r) {
			return
		}
	}
	cred, err := a.wa.FinishRegistration(o, *cer.data, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "webauthn", err.Error())
		return
	}
	raw, _ := json.Marshal(cred)
	if cer.setupHash != "" {
		err = a.st.RegisterWithSetup(r.Context(), cer.setupHash, string(cred.ID), string(raw), a.cfg.HostedWorkspace)
	} else {
		err = a.st.PutCredential(r.Context(), string(cred.ID), string(raw))
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusForbidden, "setup_token", "注册链接已过期、已使用或已更换")
		return
	}
	if err != nil {
		slog.Error("auth: register credential", "error", err)
		writeError(w, http.StatusInternalServerError, "internal", "无法保存 Passkey，请重试")
		return
	}
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
	if a.putCeremony(w, r, ceremony{data: data}) {
		writeJSON(w, opts)
	}
}

func (a *Auth) handleLoginFinish(w http.ResponseWriter, r *http.Request) {
	cer, err := a.takeCeremony(r, false)
	if err != nil {
		writeError(w, http.StatusForbidden, "ceremony", "登录流程已过期，请重试")
		return
	}
	o, err := a.owner(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	cred, err := a.wa.FinishLogin(o, *cer.data, r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "webauthn", err.Error())
		return
	}
	raw, _ := json.Marshal(cred) // keeps the updated sign count
	a.st.PutCredential(r.Context(), string(cred.ID), string(raw))
	// Verifying again while logged in refreshes the session in place, so
	// what belongs to it (open shells) stays reachable.
	if c, err := r.Cookie(a.cookieName(SessionCookie)); err == nil && c.Value != "" {
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
	if err := a.logout(w, r); err != nil {
		slog.Error("auth: logout", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "无法撤销会话，请重试")
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleDevLogin logs in without a passkey. Only with dev: true, and only for
// requests that come straight from this machine.
func (a *Auth) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.Dev || !isLoopback(r) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if c, err := r.Cookie(a.cookieName(SessionCookie)); err == nil && c.Value != "" && a.st.RefreshSession(r.Context(), hash(c.Value)) == nil {
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
