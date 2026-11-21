package externalmcp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type accessKey struct{}

type projectSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type taskSummary struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}
type taskDetail struct {
	taskSummary
	Detail string `json:"detail"`
}
type pageInput struct {
	After string `json:"after,omitempty" jsonschema:"Use the next_after cursor from the previous page; omit for the first page"`
}
type pageOutput struct {
	Tasks []taskSummary `json:"tasks"`
	Next  string        `json:"next_after,omitempty"`
}
type taskInput struct {
	ID string `json:"id" jsonschema:"Opaque task ID from task_list"`
}
type draftInput struct {
	RequestID string `json:"request_id" jsonschema:"A client-generated UUID; reuse it when retrying the same draft"`
	Title     string `json:"title"`
	Detail    string `json:"detail,omitempty"`
}

var requestID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var refID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var toolFailed = errors.New("Unable to complete the scoped project operation")

func (q *Service) authorizedRead(ctx context.Context, run func(*sql.Tx, grant) error) error {
	hash, ok := ctx.Value(accessKey{}).(string)
	if !ok {
		return errGrant
	}
	tx, err := q.store.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return toolFailed
	}
	defer tx.Rollback()
	g, err := q.access(ctx, tx, hash)
	if errors.Is(err, errGrant) {
		return errGrant
	}
	if err != nil {
		return toolFailed
	}
	return run(tx, g)
}

func (q *Service) server(writable bool) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "sous-project", Title: "Sous Project", Version: "1"}, nil)
	closed := false
	read := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &closed}
	mcp.AddTool(s, &mcp.Tool{Name: "project_get", Description: "Get the name and ID of the one project granted by the workspace owner.", Annotations: read},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, projectSummary, error) {
			var out projectSummary
			err := q.authorizedRead(ctx, func(tx *sql.Tx, g grant) error {
				if err := tx.QueryRowContext(ctx, `SELECT id,substr(name,1,250) FROM projects WHERE id=?`, g.ProjectID).Scan(&out.ID, &out.Name); err != nil {
					return toolFailed
				}
				return nil
			})
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "task_list", Description: "List up to 30 task titles and states in the granted project.", Annotations: read},
		func(ctx context.Context, _ *mcp.CallToolRequest, in pageInput) (*mcp.CallToolResult, pageOutput, error) {
			out := pageOutput{Tasks: []taskSummary{}}
			if in.After != "" && !refID.MatchString(in.After) {
				return nil, out, errors.New("Invalid task cursor")
			}
			err := q.authorizedRead(ctx, func(tx *sql.Tx, g grant) error {
				rows, err := tx.QueryContext(ctx, `SELECT id,substr(title,1,500),status FROM tasks WHERE project_id=? AND id>? ORDER BY id LIMIT 31`, g.ProjectID, in.After)
				if err != nil {
					return toolFailed
				}
				defer rows.Close()
				for rows.Next() {
					var t taskSummary
					if err := rows.Scan(&t.ID, &t.Title, &t.Status); err != nil {
						return toolFailed
					}
					out.Tasks = append(out.Tasks, t)
				}
				if rows.Err() != nil {
					return toolFailed
				}
				if len(out.Tasks) > 30 {
					out.Tasks = out.Tasks[:30]
					out.Next = out.Tasks[29].ID
				}
				return nil
			})
			return nil, out, err
		})
	mcp.AddTool(s, &mcp.Tool{Name: "task_get", Description: "Read a task's title, description and status only within the granted project.", Annotations: read},
		func(ctx context.Context, _ *mcp.CallToolRequest, in taskInput) (*mcp.CallToolResult, taskDetail, error) {
			var out taskDetail
			missing := errors.New("Task not found in the granted project")
			if !refID.MatchString(in.ID) {
				return nil, out, missing
			}
			err := q.authorizedRead(ctx, func(tx *sql.Tx, g grant) error {
				err := tx.QueryRowContext(ctx, `SELECT id,substr(title,1,500),status,substr(detail,1,3000) FROM tasks WHERE id=? AND project_id=?`, in.ID, g.ProjectID).Scan(&out.ID, &out.Title, &out.Status, &out.Detail)
				if errors.Is(err, sql.ErrNoRows) {
					return missing
				}
				if err != nil {
					return toolFailed
				}
				return nil
			})
			return nil, out, err
		})
	if writable {
		nonDestructive := false
		mcp.AddTool(s, &mcp.Tool{Name: "task_create", Description: "File an inbox draft in the granted project. Cannot dispatch execution, assign nodes or approve work.", Annotations: &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &nonDestructive, OpenWorldHint: &closed}},
			func(ctx context.Context, _ *mcp.CallToolRequest, in draftInput) (*mcp.CallToolResult, taskSummary, error) {
				var out taskSummary
				if !requestID.MatchString(in.RequestID) || strings.TrimSpace(in.Title) == "" || len(in.Title) > 500 || len(in.Detail) > 12000 || strings.ContainsRune(in.Title+in.Detail, 0) {
					return nil, out, errors.New("Use a UUID request_id, a title of at most 500 bytes and a description of at most 12000 bytes")
				}
				hash, ok := ctx.Value(accessKey{}).(string)
				if !ok {
					return nil, out, errGrant
				}
				g, err := q.access(ctx, q.store.DB(), hash)
				if errors.Is(err, errGrant) {
					return nil, out, errGrant
				}
				if err != nil {
					return nil, out, toolFailed
				}
				if !g.Drafts {
					return nil, out, errGrant
				}
				t, err := q.tasks.FileExternalDraft(ctx, g.ID, g.ProjectID, in.RequestID, in.Title, in.Detail, func(tx *sql.Tx) error {
					current, err := q.access(ctx, tx, hash)
					if err != nil {
						return err
					}
					if !current.Drafts || current.ID != g.ID || current.ProjectID != g.ProjectID {
						return errGrant
					}
					return nil
				})
				if errors.Is(err, errGrant) {
					return nil, out, errGrant
				}
				if err != nil {
					return nil, out, errors.New("Draft not filed; check the request_id and retry the same draft if needed")
				}
				return nil, taskSummary{t.ID, t.Title, t.Status}, nil
			})
	}
	return s
}

type writeCapability struct{}

func (q *Service) mcpHandler() http.Handler {
	read, write := q.server(false), q.server(true)
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if enabled, _ := r.Context().Value(writeCapability{}).(bool); enabled {
			return write
		}
		return read
	},
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 64 << 10,
			// The hosted server already pins the Host to the canonical HTTPS
			// origin, including when a reverse proxy dials the loopback socket.
			DisableLocalhostProtection: true,
		})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !machineOnly(r) || r.URL.RawQuery != "" {
			http.Error(w, "External MCP requires a non-browser request", http.StatusForbidden)
			return
		}
		challenge := func(invalid bool) {
			value := `Bearer resource_metadata="` + q.issuer + MetadataPath + `"`
			if invalid {
				value += `, error="invalid_token"`
			}
			w.Header().Set("WWW-Authenticate", value)
			reply(w, 401, map[string]string{"error": "invalid_token"})
		}
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || len(r.Header.Values("Authorization")) != 1 {
			challenge(false)
			return
		}
		if len(tok) != 43 || !canonicalDigest(tok) {
			challenge(true)
			return
		}
		h := digest(tok)
		g, err := q.access(r.Context(), q.store.DB(), h)
		if errors.Is(err, errGrant) {
			challenge(true)
			return
		}
		if err != nil {
			reply(w, 503, map[string]string{"error": "temporarily_unavailable"})
			return
		}
		ctx := context.WithValue(r.Context(), accessKey{}, h)
		handler.ServeHTTP(w, r.WithContext(context.WithValue(ctx, writeCapability{}, g.Drafts)))
	})
}
