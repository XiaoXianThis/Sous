// Package externalmcp is the hosted, owner-authorized project tool surface.
// It never uses the supervisor, Node, browser or cloud worker credentials.
package externalmcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

const (
	Endpoint              = "/mcp"
	AuthorizationPath     = "/oauth/authorize"
	TokenPath             = "/oauth/token"
	RevokePath            = "/oauth/revoke"
	MetadataPath          = "/.well-known/oauth-protected-resource/mcp"
	authorizationMetadata = "/.well-known/oauth-authorization-server"
	projectRead           = "project.read"
	draftWrite            = "drafts.write"
	codeLifetime          = 2 * time.Minute
	accessLifetime        = 15 * time.Minute
	grantLifetime         = 7 * 24 * time.Hour
)

var (
	errInvalid      = errors.New("invalid OAuth request")
	errGrant        = errors.New("expired, revoked or invalid external MCP authorization")
	errLimit        = errors.New("external MCP authorization limit reached")
	verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

type Service struct {
	store            *store.Store
	tasks            *task.Engine
	issuer, resource string
	clients          map[string]config.ExternalMCPClient
	now              func() time.Time
	peers            chan struct{}
}

func New(st *store.Store, tasks *task.Engine, origin string, clients []config.ExternalMCPClient) *Service {
	q := &Service{store: st, tasks: tasks, issuer: origin, resource: origin + Endpoint, clients: map[string]config.ExternalMCPClient{}, now: time.Now, peers: make(chan struct{}, 16)}
	for _, c := range clients {
		q.clients[c.ID] = c
	}
	return q
}

func MachineRoute(path string) bool {
	return path == Endpoint || path == TokenPath || path == RevokePath
}

// Reserved paths must never fall back to serving the WebUI when disabled.
func ReservedPath(path string) bool {
	return MachineRoute(path) || path == AuthorizationPath || path == MetadataPath || path == "/.well-known/oauth-protected-resource" || path == authorizationMetadata
}

func digest(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func pkce(s string) string {
	b := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func opaque() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes[:]), nil
}
func canonicalDigest(s string) bool {
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}
func same(a, b string) bool                       { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func clientKey(c config.ExternalMCPClient) string { return digest(c.ID + "\x00" + c.RedirectURI) }

type approval struct {
	client           config.ExternalMCPClient
	state, challenge string
	wantDraft        bool
}

func unique(values url.Values, allowed ...string) bool {
	if len(values) > len(allowed) {
		return false
	}
	for k, v := range values {
		found := false
		for _, key := range allowed {
			if k == key {
				found = true
				break
			}
		}
		if !found || len(v) != 1 {
			return false
		}
	}
	return true
}

func (q *Service) parseAuthorization(raw string) (a approval, err error) {
	if len(raw) > 4096 {
		return a, errInvalid
	}
	v, err := url.ParseQuery(raw)
	if err != nil || !unique(v, "response_type", "client_id", "redirect_uri", "resource", "state", "code_challenge", "code_challenge_method", "scope") {
		return a, errInvalid
	}
	c, ok := q.clients[v.Get("client_id")]
	state := v.Get("state")
	challenge := v.Get("code_challenge")
	if !ok || v.Get("redirect_uri") != c.RedirectURI || v.Get("resource") != q.resource || v.Get("response_type") != "code" || v.Get("code_challenge_method") != "S256" || !canonicalDigest(challenge) || state == "" || len(state) > 512 || strings.ContainsFunc(state, unicode.IsControl) {
		return a, errInvalid
	}
	switch v.Get("scope") {
	case "", projectRead:
	case projectRead + " " + draftWrite, draftWrite + " " + projectRead:
		a.wantDraft = true
	default:
		return a, errInvalid
	}
	a.client, a.state, a.challenge = c, state, challenge
	return a, nil
}

// ValidAuthorization is also used when serving the hosted consent shell. An
// invalid or unknown callback is never accepted or redirected to.
func ValidAuthorization(origin, raw string, clients []config.ExternalMCPClient) bool {
	_, err := New(nil, nil, origin, clients).parseAuthorization(raw)
	return err == nil
}

func validateProject(ctx context.Context, tx *sql.Tx, project string) error {
	if project == "" || project == "misc" || len(project) > 64 {
		return errGrant
	}
	var live bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=? AND archived_at=0)`, project).Scan(&live); err != nil {
		return err
	}
	if !live {
		return errGrant
	}
	return nil
}

func (q *Service) approve(ctx context.Context, a approval, project string, draft bool) (string, error) {
	if draft && !a.wantDraft {
		return "", errInvalid
	}
	raw, err := opaque()
	if err != nil {
		return "", err
	}
	now := q.now()
	_, _, err = q.store.Write(ctx, func(w *store.Writer) error {
		tx := w.Tx()
		if err := validateProject(ctx, tx, project); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM external_mcp_codes WHERE expires_at<=?`, now.UnixMilli()); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM external_mcp_codes`).Scan(&count); err != nil {
			return err
		}
		if count >= 24 {
			return errLimit
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO external_mcp_codes(code_hash,client_key,project_id,challenge,write_draft,expires_at) VALUES(?,?,?,?,?,?)`, digest(raw), clientKey(a.client), project, a.challenge, draft, now.Add(codeLifetime).UnixMilli())
		return err
	})
	if err != nil {
		return "", err
	}
	return redirect(a, "code", raw), nil
}

func redirect(a approval, key, value string) string {
	u, _ := url.Parse(a.client.RedirectURI)
	u.RawQuery = url.Values{key: {value}, "state": {a.state}}.Encode()
	return u.String()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

func newTokenResponse() (out tokenResponse, err error) {
	out.AccessToken, err = opaque()
	if err != nil {
		return out, err
	}
	out.RefreshToken, err = opaque()
	out.TokenType = "Bearer"
	out.ExpiresIn = int(accessLifetime / time.Second)
	out.Scope = projectRead
	return
}

func (q *Service) exchange(ctx context.Context, c config.ExternalMCPClient, code, verifier, redirectURI string) (out tokenResponse, err error) {
	if !canonicalDigest(code) || !verifierPattern.MatchString(verifier) || redirectURI != c.RedirectURI {
		return out, errGrant
	}
	out, err = newTokenResponse()
	if err != nil {
		return out, err
	}
	now := q.now()
	_, _, err = q.store.Write(ctx, func(w *store.Writer) error {
		tx := w.Tx()
		var key, project, challenge string
		var draft bool
		e := tx.QueryRowContext(ctx, `SELECT client_key,project_id,challenge,write_draft FROM external_mcp_codes WHERE code_hash=? AND expires_at>?`, digest(code), now.UnixMilli()).Scan(&key, &project, &challenge, &draft)
		if errors.Is(e, sql.ErrNoRows) {
			return errGrant
		}
		if e != nil {
			return e
		}
		if !same(challenge, pkce(verifier)) || !same(key, clientKey(c)) {
			return errGrant
		}
		if err := validateProject(ctx, tx, project); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM external_mcp_grants WHERE expires_at<=? OR revoked_at>0`, now.UnixMilli()); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM external_mcp_grants`).Scan(&count); err != nil {
			return err
		}
		if count >= 32 {
			return errLimit
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM external_mcp_codes WHERE code_hash=?`, digest(code)); err != nil {
			return err
		}
		if draft {
			out.Scope += " " + draftWrite
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO external_mcp_grants(id,client_id,client_key,project_id,write_draft,access_hash,refresh_hash,created_at,expires_at,access_until) VALUES(?,?,?,?,?,?,?,?,?,?)`, ids.New(), c.ID, key, project, draft, digest(out.AccessToken), digest(out.RefreshToken), now.UnixMilli(), now.Add(grantLifetime).UnixMilli(), now.Add(accessLifetime).UnixMilli())
		return err
	})
	return
}

func (q *Service) refresh(ctx context.Context, c config.ExternalMCPClient, raw string) (out tokenResponse, err error) {
	if !canonicalDigest(raw) {
		return out, errGrant
	}
	out, err = newTokenResponse()
	if err != nil {
		return out, err
	}
	now := q.now()
	h := digest(raw)
	replay := false
	_, _, err = q.store.Write(ctx, func(w *store.Writer) error {
		tx := w.Tx()
		// Only a spent credential from this same registered client can revoke
		// its token family. The invalid_grant response must not roll this back.
		var id, project string
		var draft bool
		var until int64
		result, err := tx.ExecContext(ctx, `UPDATE external_mcp_grants SET revoked_at=? WHERE client_key=? AND revoked_at=0 AND id=(SELECT grant_id FROM external_mcp_spent_refresh WHERE token_hash=?)`, now.UnixMilli(), clientKey(c), h)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed != 0 {
			replay = true
			return nil
		}
		err = tx.QueryRowContext(ctx, `SELECT id,project_id,write_draft,expires_at FROM external_mcp_grants WHERE refresh_hash=? AND client_key=? AND revoked_at=0 AND expires_at>?`, h, clientKey(c), now.UnixMilli()).Scan(&id, &project, &draft, &until)
		if errors.Is(err, sql.ErrNoRows) {
			return errGrant
		}
		if err != nil {
			return err
		}
		if err := validateProject(ctx, tx, project); err != nil {
			return err
		}
		var spent int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM external_mcp_spent_refresh WHERE grant_id=?`, id).Scan(&spent); err != nil {
			return err
		}
		if spent >= 2048 {
			return errGrant
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO external_mcp_spent_refresh(token_hash,grant_id) VALUES(?,?)`, h, id); err != nil {
			return err
		}
		accessUntil := min(now.Add(accessLifetime).UnixMilli(), until)
		out.ExpiresIn = int((accessUntil - now.UnixMilli()) / 1000)
		if draft {
			out.Scope += " " + draftWrite
		}
		_, err = tx.ExecContext(ctx, `UPDATE external_mcp_grants SET access_hash=?,refresh_hash=?,access_until=? WHERE id=?`, digest(out.AccessToken), digest(out.RefreshToken), accessUntil, id)
		return err
	})
	if err == nil && replay {
		err = errGrant
	}
	return
}
