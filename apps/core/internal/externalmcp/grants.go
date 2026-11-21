package externalmcp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/XiaoXianThis/Sous/apps/core/internal/config"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

type grant struct {
	ID        string `json:"id"`
	Client    string `json:"client"`
	ProjectID string `json:"project_id"`
	Project   string `json:"project"`
	Drafts    bool   `json:"allow_drafts"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	Active    bool   `json:"active"`
}

type queryRow interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (q *Service) access(ctx context.Context, db queryRow, hash string) (g grant, err error) {
	var clientID, key string
	now := q.now().UnixMilli()
	err = db.QueryRowContext(ctx, `SELECT g.id,g.client_id,g.client_key,g.project_id,g.write_draft
 FROM external_mcp_grants g JOIN projects p ON p.id=g.project_id AND p.archived_at=0
 WHERE g.access_hash=? AND g.access_until>? AND g.expires_at>? AND g.revoked_at=0`, hash, now, now).Scan(&g.ID, &clientID, &key, &g.ProjectID, &g.Drafts)
	if errors.Is(err, sql.ErrNoRows) {
		return g, errGrant
	}
	if err != nil {
		return g, err
	}
	c, ok := q.clients[clientID]
	if !ok || !same(key, clientKey(c)) {
		return g, errGrant
	}
	return g, nil
}

func (q *Service) listGrants(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := q.store.DB().QueryContext(ctx, `SELECT g.id,g.client_id,g.client_key,g.project_id,COALESCE(p.name,''),g.write_draft,g.created_at,g.expires_at,p.id IS NOT NULL AND p.archived_at=0
 FROM external_mcp_grants g LEFT JOIN projects p ON p.id=g.project_id WHERE g.revoked_at=0 AND g.expires_at>? ORDER BY g.created_at DESC LIMIT 32`, q.now().UnixMilli())
	if err != nil {
		ownerError(w, err)
		return
	}
	defer rows.Close()
	grants := []grant{}
	for rows.Next() {
		var g grant
		var clientID, key string
		var projectLive bool
		if err := rows.Scan(&g.ID, &clientID, &key, &g.ProjectID, &g.Project, &g.Drafts, &g.CreatedAt, &g.ExpiresAt, &projectLive); err != nil {
			ownerError(w, err)
			return
		}
		c, ok := q.clients[clientID]
		g.Active = ok && same(key, clientKey(c)) && projectLive
		if ok {
			g.Client = c.Name
		} else {
			g.Client = clientID
		}
		if g.Project == "" {
			g.Project = "项目已移除"
		}
		grants = append(grants, g)
	}
	if err := rows.Err(); err != nil {
		ownerError(w, err)
		return
	}
	reply(w, 200, map[string]any{"grants": grants, "endpoint": q.resource})
}

func (q *Service) revokeGrant(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) != 26 {
		ownerError(w, errInvalid)
		return
	}
	ctx := r.Context()
	_, _, err := q.store.Write(ctx, func(w *store.Writer) error {
		_, err := w.Tx().ExecContext(ctx, `UPDATE external_mcp_grants SET revoked_at=? WHERE id=? AND revoked_at=0`, q.now().UnixMilli(), id)
		return err
	})
	if err != nil {
		ownerError(w, err)
		return
	}
	reply(w, 200, map[string]bool{"revoked": true})
}

func (q *Service) revokeByToken(ctx context.Context, c config.ExternalMCPClient, token string) error {
	if !canonicalDigest(token) {
		return nil
	}
	h := digest(token)
	_, _, err := q.store.Write(ctx, func(w *store.Writer) error {
		_, err := w.Tx().ExecContext(ctx, `UPDATE external_mcp_grants SET revoked_at=? WHERE client_key=? AND revoked_at=0 AND (access_hash=? OR refresh_hash=? OR id=(SELECT grant_id FROM external_mcp_spent_refresh WHERE token_hash=?))`, q.now().UnixMilli(), clientKey(c), h, h, h)
		return err
	})
	return err
}
