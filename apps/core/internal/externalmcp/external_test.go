package externalmcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var clientFixture = config.ExternalMCPClient{ID: "fixture-client", Name: "Fixture Client", RedirectURI: "https://client.example.test/callback"}

const verifier = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func fixture(t *testing.T, origin string) *Service {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, _, err = st.Write(context.Background(), func(w *store.Writer) error {
		_, err := w.Tx().Exec(`INSERT INTO projects(id,name,repo_url,created_at,updated_at) VALUES('p','Approved','private-project-url',1,1),('q','Other','other-private-url',1,1);
 INSERT INTO tasks(id,title,detail,project_id,status,kind,created_at,updated_at) VALUES('visible','Project task','review content','p','inbox','general',1,1),('hidden','Other project task','never disclose','q','inbox','general',1,1);`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return New(st, task.New(eventlog.New(st, event.NewBus()), nil), origin, []config.ExternalMCPClient{clientFixture})
}

func authorization(q *Service, draft bool) url.Values {
	scope := projectRead
	if draft {
		scope += " " + draftWrite
	}
	return url.Values{"client_id": {clientFixture.ID}, "redirect_uri": {clientFixture.RedirectURI}, "resource": {q.resource}, "state": {"fixture-state"}, "code_challenge": {pkce(verifier)}, "code_challenge_method": {"S256"}, "response_type": {"code"}, "scope": {scope}}
}

func approvedCode(t *testing.T, q *Service, draft bool) string {
	t.Helper()
	a, err := q.parseAuthorization(authorization(q, true).Encode())
	if err != nil {
		t.Fatal(err)
	}
	to, err := q.approve(context.Background(), a, "p", draft)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(to)
	if err != nil || u.Scheme != "https" || u.Host != "client.example.test" || u.Query().Get("state") != "fixture-state" || u.Query().Get("code") == "" {
		t.Fatal("authorization code did not use the registered callback")
	}
	return u.Query().Get("code")
}

func TestOAuthProtectsResourceCallbackCodeAndRotatingTokens(t *testing.T) {
	q := fixture(t, "https://hub.example.test")
	ctx := context.Background()
	now := time.Now()
	q.now = func() time.Time { return now }
	for key, value := range map[string]string{"redirect_uri": "https://foreign.example.test/callback", "resource": "https://foreign.example.test/mcp", "code_challenge_method": "plain", "response_type": "token", "client_id": "unknown-client", "state": "", "scope": "workspace.admin"} {
		changed := authorization(q, true)
		changed.Set(key, value)
		if _, err := q.parseAuthorization(changed.Encode()); !errors.Is(err, errInvalid) {
			t.Fatalf("accepted invalid %s", key)
		}
	}
	duplicate := authorization(q, true)
	duplicate.Add("redirect_uri", clientFixture.RedirectURI)
	if _, err := q.parseAuthorization(duplicate.Encode()); err == nil {
		t.Fatal("ambiguous URI accepted")
	}
	code := approvedCode(t, q, true)
	if _, err := q.exchange(ctx, clientFixture, code, strings.Repeat("a", 43), clientFixture.RedirectURI); !errors.Is(err, errGrant) {
		t.Fatal("accepted wrong PKCE verifier", err)
	}
	if _, err := q.exchange(ctx, clientFixture, code, verifier, "https://bad.example.test/return"); !errors.Is(err, errGrant) {
		t.Fatal("accepted wrong redirect", err)
	}
	token, err := q.exchange(ctx, clientFixture, code, verifier, clientFixture.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.exchange(ctx, clientFixture, code, verifier, clientFixture.RedirectURI); !errors.Is(err, errGrant) {
		t.Fatal("authorization code was reusable", err)
	}
	g, err := q.access(ctx, q.store.DB(), digest(token.AccessToken))
	if err != nil || g.ProjectID != "p" || !g.Drafts {
		t.Fatal(g, err)
	}
	var raw string
	if err := q.store.DB().QueryRow(`SELECT access_hash FROM external_mcp_grants`).Scan(&raw); err != nil || raw != digest(token.AccessToken) {
		t.Fatal("token was not stored solely as a digest", err)
	}
	now = now.Add(accessLifetime + time.Second)
	if _, err := q.access(ctx, q.store.DB(), digest(token.AccessToken)); !errors.Is(err, errGrant) {
		t.Fatal("expired access accepted", err)
	}
	renewed, err := q.refresh(ctx, clientFixture, token.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.access(ctx, q.store.DB(), digest(renewed.AccessToken)); err != nil {
		t.Fatal(err)
	}
	if _, err := q.refresh(ctx, clientFixture, token.RefreshToken); !errors.Is(err, errGrant) {
		t.Fatal("replayed refresh accepted", err)
	}
	if _, err := q.access(ctx, q.store.DB(), digest(renewed.AccessToken)); !errors.Is(err, errGrant) {
		t.Fatal("refresh replay did not revoke the family", err)
	}
	code = approvedCode(t, q, false)
	now = now.Add(codeLifetime + time.Second)
	if _, err := q.exchange(ctx, clientFixture, code, verifier, clientFixture.RedirectURI); !errors.Is(err, errGrant) {
		t.Fatal("expired code accepted", err)
	}
}

type authorizing struct {
	base   http.RoundTripper
	bearer string
}

func (a authorizing) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+a.bearer)
	return a.base.RoundTrip(r)
}

func TestOfficialMCPClientCanOnlySeeAndFileDraftsForItsGrant(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	defer server.Close()
	q := fixture(t, server.URL)
	q.Routes(func(http.ResponseWriter, *http.Request) bool { return false })(mux, func(string, http.HandlerFunc) {})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpDefault := server.Client()
	res, err := httpDefault.Get(q.resource)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 401 || !strings.Contains(res.Header.Get("WWW-Authenticate"), q.issuer+MetadataPath) {
		t.Fatal("missing OAuth discovery on unauthorized streamable HTTP", res.StatusCode)
	}
	res.Body.Close()
	// The native token endpoint must see the exact resource indicator.
	v := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientFixture.ID}, "resource": {"https://wrong.example.test/mcp"}, "code": {approvedCode(t, q, true)}, "redirect_uri": {clientFixture.RedirectURI}, "code_verifier": {verifier}}
	res, err = httpDefault.PostForm(q.issuer+TokenPath, v)
	if err != nil {
		t.Fatal(err)
	}
	bad, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 400 || !strings.Contains(string(bad), "invalid_target") {
		t.Fatal("accepted token for another workspace")
	}
	v.Set("resource", q.resource)
	res, err = httpDefault.PostForm(q.issuer+TokenPath, v)
	if err != nil {
		t.Fatal(err)
	}
	var write tokenResponse
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&write) != nil || write.AccessToken == "" {
		t.Fatal("token was not issued", res.StatusCode)
	}
	res.Body.Close()
	read, err := q.exchange(ctx, clientFixture, approvedCode(t, q, false), verifier, clientFixture.RedirectURI)
	if err != nil {
		t.Fatal(err)
	}
	connect := func(token string) *mcp.ClientSession {
		t.Helper()
		client := mcp.NewClient(&mcp.Implementation{Name: "separate-external-client", Version: "test"}, nil)
		hc := &http.Client{Transport: authorizing{httpDefault.Transport, token}}
		s, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: q.resource, HTTPClient: hc, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	list := func(s *mcp.ClientSession) []string {
		t.Helper()
		tools, err := s.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, tool := range tools.Tools {
			names = append(names, tool.Name)
		}
		sort.Strings(names)
		return names
	}
	ro := connect(read.AccessToken)
	if got := list(ro); !reflect.DeepEqual(got, []string{"project_get", "task_get", "task_list"}) {
		t.Fatal("wrong read grant tools", got)
	}
	rw := connect(write.AccessToken)
	if got := list(rw); !reflect.DeepEqual(got, []string{"project_get", "task_create", "task_get", "task_list"}) {
		t.Fatal("wrong draft grant tools", got)
	}
	invoke := func(s *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	contains := func(out *mcp.CallToolResult, part string) bool {
		b, _ := json.Marshal(out.StructuredContent)
		return strings.Contains(string(b), part)
	}
	if project := invoke(rw, "project_get", nil); project.IsError || !contains(project, "Approved") || contains(project, "private-project-url") {
		t.Fatal("wrong project disclosure", project)
	}
	if tasks := invoke(rw, "task_list", nil); tasks.IsError || !contains(tasks, "Project task") || contains(tasks, "Other project task") {
		t.Fatal("wrong task disclosure", tasks)
	}
	if tsk := invoke(rw, "task_get", map[string]any{"id": "visible"}); tsk.IsError || !contains(tsk, "review content") {
		t.Fatal("expected scoped details", tsk)
	}
	if tsk := invoke(rw, "task_get", map[string]any{"id": "hidden"}); !tsk.IsError || contains(tsk, "never disclose") {
		t.Fatal("cross-project read succeeded", tsk)
	}
	args := map[string]any{"request_id": "00000000-0000-4000-8000-000000000001", "title": "External draft", "detail": "Synthetic"}
	if result, err := ro.CallTool(ctx, &mcp.CallToolParams{Name: "task_create", Arguments: args}); err == nil && !result.IsError {
		t.Fatal("read grant can write")
	}
	draft := invoke(rw, "task_create", args)
	again := invoke(rw, "task_create", args)
	if draft.IsError || again.IsError || !reflect.DeepEqual(draft.StructuredContent, again.StructuredContent) || !contains(draft, "inbox") {
		t.Fatal("draft write/bounded replay failed", draft, again)
	}
	var count int
	if err := q.store.DB().QueryRow(`SELECT count(*) FROM tasks WHERE project_id='p' AND title='External draft' AND node_pref=''`).Scan(&count); err != nil || count != 1 {
		t.Fatal("external task was not a single undispatched draft", count, err)
	}
	if err := q.store.DB().QueryRow(`SELECT count(*) FROM runs`).Scan(&count); err != nil || count != 0 {
		t.Fatal("external tool created an execution", count, err)
	}
	if err := q.revokeByToken(ctx, clientFixture, write.RefreshToken); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, q.resource, nil)
	req.Header.Set("Authorization", "Bearer "+write.AccessToken)
	res, err = httpDefault.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("revoked MCP session still authorized", res.StatusCode)
	}
}
