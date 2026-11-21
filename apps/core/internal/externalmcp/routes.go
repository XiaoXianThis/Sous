package externalmcp

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
)

const ownerPath = "/api/v1/external-mcp/"

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func ownerError(w http.ResponseWriter, err error) {
	code, status, msg := "external_mcp_unavailable", http.StatusServiceUnavailable, "外部连接暂不可用，请稍后重试。"
	switch {
	case errors.Is(err, errInvalid):
		code, status, msg = "invalid_external_request", http.StatusBadRequest, "授权请求无效，或此客户端未注册。请从客户端重新发起。"
	case errors.Is(err, errGrant):
		code, status, msg = "invalid_external_project", http.StatusConflict, "此项目不可授权。请选择未归档的项目。"
	case errors.Is(err, errLimit):
		code, status, msg = "external_grant_limit", http.StatusTooManyRequests, "外部授权数量已达上限。请稍后重试或撤销旧连接。"
	default:
		slog.Warn("external MCP owner operation failed")
	}
	reply(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func machineOnly(r *http.Request) bool {
	return len(r.Header.Values("Origin")) == 0 && len(r.Header.Values("Cookie")) == 0
}

// A flood of external requests must not open unlimited sessions or SQLite
// transactions alongside the owner workbench. Deployment edge limits are
// still useful; this local cap also works behind a reverse proxy.
func (q *Service) boundedPeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case q.peers <- struct{}{}:
			defer func() { <-q.peers }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			reply(w, http.StatusTooManyRequests, map[string]string{"error": "temporarily_unavailable"})
		}
	})
}

func (q *Service) Routes(fresh func(http.ResponseWriter, *http.Request) bool) func(*http.ServeMux, func(string, http.HandlerFunc)) {
	return func(mux *http.ServeMux, protect func(string, http.HandlerFunc)) {
		mux.Handle(Endpoint, q.boundedPeer(q.mcpHandler()))
		mux.HandleFunc("GET "+MetadataPath, q.protectedResource)
		mux.HandleFunc("GET /.well-known/oauth-protected-resource", q.protectedResource)
		mux.HandleFunc("GET "+authorizationMetadata, q.serverMetadata)
		mux.Handle("POST "+TokenPath, q.boundedPeer(http.HandlerFunc(q.token)))
		mux.Handle("POST "+RevokePath, q.boundedPeer(http.HandlerFunc(q.revokeToken)))
		protect("GET "+ownerPath+"preview", q.preview)
		protect("POST "+ownerPath+"decision", func(w http.ResponseWriter, r *http.Request) { q.decision(w, r, fresh) })
		protect("GET "+ownerPath+"grants", q.listGrants)
		protect("DELETE "+ownerPath+"grants/{id}", q.revokeGrant)
	}
}

func (q *Service) protectedResource(w http.ResponseWriter, _ *http.Request) {
	reply(w, http.StatusOK, map[string]any{"resource": q.resource, "authorization_servers": []string{q.issuer}, "scopes_supported": []string{projectRead, draftWrite}, "bearer_methods_supported": []string{"header"}})
}
func (q *Service) serverMetadata(w http.ResponseWriter, _ *http.Request) {
	reply(w, http.StatusOK, map[string]any{
		"issuer": q.issuer, "authorization_endpoint": q.issuer + AuthorizationPath, "token_endpoint": q.issuer + TokenPath, "revocation_endpoint": q.issuer + RevokePath,
		"scopes_supported": []string{projectRead, draftWrite}, "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"}, "revocation_endpoint_auth_methods_supported": []string{"none"}, "code_challenge_methods_supported": []string{"S256"},
	})
}

func form(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if !machineOnly(r) || len(r.Header.Values("Authorization")) != 0 || r.URL.RawQuery != "" || err != nil || kind != "application/x-www-form-urlencoded" {
		return nil, false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		return nil, false
	}
	values, err := url.ParseQuery(string(raw))
	return values, err == nil
}

func (q *Service) token(w http.ResponseWriter, r *http.Request) {
	v, ok := form(w, r)
	if !ok {
		reply(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	if v.Get("resource") != q.resource {
		reply(w, 400, map[string]string{"error": "invalid_target"})
		return
	}
	c, ok := q.clients[v.Get("client_id")]
	if !ok {
		reply(w, 400, map[string]string{"error": "invalid_client"})
		return
	}
	var out tokenResponse
	var err error
	switch v.Get("grant_type") {
	case "authorization_code":
		if !unique(v, "grant_type", "client_id", "resource", "code", "code_verifier", "redirect_uri") {
			err = errInvalid
			break
		}
		out, err = q.exchange(r.Context(), c, v.Get("code"), v.Get("code_verifier"), v.Get("redirect_uri"))
	case "refresh_token":
		if !unique(v, "grant_type", "client_id", "resource", "refresh_token") {
			err = errInvalid
			break
		}
		out, err = q.refresh(r.Context(), c, v.Get("refresh_token"))
	default:
		reply(w, 400, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	if err != nil {
		reason := "server_error"
		status := http.StatusServiceUnavailable
		switch {
		case errors.Is(err, errGrant):
			reason, status = "invalid_grant", 400
		case errors.Is(err, errInvalid):
			reason, status = "invalid_request", 400
		case errors.Is(err, errLimit):
			reason = "temporarily_unavailable"
		default:
			slog.Warn("external MCP token operation failed")
		}
		reply(w, status, map[string]string{"error": reason})
		return
	}
	reply(w, 200, out)
}

func userJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	return d.Decode(out) == nil && d.Decode(new(json.RawMessage)) == io.EOF
}

type option struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (q *Service) preview(w http.ResponseWriter, r *http.Request) {
	v, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || !unique(v, "request") {
		ownerError(w, errInvalid)
		return
	}
	a, err := q.parseAuthorization(v.Get("request"))
	if err != nil {
		ownerError(w, err)
		return
	}
	ps, err := q.store.Projects(r.Context())
	if err != nil {
		ownerError(w, err)
		return
	}
	options := []option{}
	for _, p := range ps {
		if p.ID != "misc" && !p.Archived() {
			options = append(options, option{p.ID, p.Name})
		}
	}
	reply(w, 200, map[string]any{"client": a.client.Name, "callback": a.client.RedirectURI, "requests_drafts": a.wantDraft, "projects": options})
}

func (q *Service) decision(w http.ResponseWriter, r *http.Request, fresh func(http.ResponseWriter, *http.Request) bool) {
	var input struct {
		Request  string `json:"request"`
		Project  string `json:"project_id"`
		Drafts   bool   `json:"allow_drafts"`
		Decision string `json:"decision"`
	}
	if !userJSON(w, r, &input) {
		ownerError(w, errInvalid)
		return
	}
	a, err := q.parseAuthorization(input.Request)
	if err != nil {
		ownerError(w, err)
		return
	}
	var to string
	switch input.Decision {
	case "deny":
		to = redirect(a, "error", "access_denied")
	case "allow":
		if !fresh(w, r) {
			return
		}
		to, err = q.approve(r.Context(), a, input.Project, input.Drafts)
	default:
		err = errInvalid
	}
	if err != nil {
		ownerError(w, err)
		return
	}
	reply(w, 200, map[string]string{"redirect_to": to})
}

func (q *Service) revokeToken(w http.ResponseWriter, r *http.Request) {
	v, ok := form(w, r)
	if !ok || !unique(v, "client_id", "token", "token_type_hint") {
		reply(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	var c config.ExternalMCPClient
	if c, ok = q.clients[v.Get("client_id")]; !ok {
		reply(w, 400, map[string]string{"error": "invalid_client"})
		return
	}
	if err := q.revokeByToken(r.Context(), c, v.Get("token")); err != nil {
		reply(w, 503, map[string]string{"error": "temporarily_unavailable"})
		return
	}
	reply(w, 200, map[string]bool{})
}
