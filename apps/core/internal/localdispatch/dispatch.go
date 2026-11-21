// Package localdispatch carries actor-approved task intent to a local desktop.
// This is not the legacy Node protocol. Nothing here can name a source path,
// command, executable, report path, or a writeback authorization.
package localdispatch

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const maxChanges = 2048
const leaseMillis = (30 * time.Second) / time.Millisecond

var (
	errNotFound   = errors.New("找不到此沙盒请求或任务")
	errConflict   = errors.New("任务或请求已变化，请刷新状态后重试")
	errBusy       = errors.New("已有一项执行尚未结束")
	errIneligible = errors.New("只有待分配或待办、且无未完成依赖的任务可以请求本机沙盒")
	errLimit      = errors.New("等待授权的沙盒请求已达上限")
	errInvalid    = errors.New("无效的本机执行请求")
)

type Queue struct {
	store *store.Store
	now   func() time.Time
}

func New(s *store.Store) *Queue { return &Queue{s, time.Now} }

// View is safe for browser clients; never include a local execution detail,
// credential, host path, or the contents of any report.
type View struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	Changes   int    `json:"changes"`
	UpdatedAt int64  `json:"updated_at"`
	ExpiresAt int64  `json:"expires_at"`
}

type Intent struct {
	View
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

const cols = `id,task_id,status,changes,updated_at,expires_at`
const eligible = `t.status IN ('inbox','planned')
 AND NOT EXISTS(SELECT 1 FROM projects p WHERE p.id=t.project_id AND p.archived_at>0)
 AND NOT EXISTS(SELECT 1 FROM task_deps d LEFT JOIN tasks dep ON dep.id=d.depends_on
                      WHERE d.task_id=t.id AND (dep.status IS NULL OR dep.status<>'done'))`

const sameTask = `SELECT 1 FROM tasks t WHERE t.id=sandbox_dispatches.task_id
 AND t.updated_at=sandbox_dispatches.task_rev AND t.title=sandbox_dispatches.title
 AND t.detail=sandbox_dispatches.detail AND t.project_id=sandbox_dispatches.project_id AND ` + eligible

func sqlView(row interface{ Scan(...any) error }) (View, error) {
	var v View
	err := row.Scan(&v.ID, &v.TaskID, &v.Status, &v.Changes, &v.UpdatedAt, &v.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = errNotFound
	}
	return v, err
}

func (q *Queue) write(ctx context.Context, fn func(*sql.Tx, int64) error) error {
	_, _, err := q.store.Write(ctx, func(w *store.Writer) error {
		now := q.now().UnixMilli()
		tx := w.Tx()
		// Enqueue snapshots a task. An edit, cancellation or new dependency
		// cannot smuggle different work past an already opened native dialog.
		if _, err := tx.ExecContext(ctx, `UPDATE sandbox_dispatches SET status=CASE
 WHEN expires_at<=? THEN 'expired' ELSE 'stale' END, updated_at=?
 WHERE status='pending' AND (expires_at<=? OR NOT EXISTS(`+sameTask+`))`, now, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sandbox_dispatches SET status='cancel_requested',updated_at=?
 WHERE status='running' AND NOT EXISTS(`+sameTask+`)`, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sandbox_dispatches SET status='interrupted',updated_at=?,lease='',lease_until=0
 WHERE status IN ('running','cancel_requested') AND lease_until<=?`, now, now); err != nil {
			return err
		}
		return fn(tx, now)
	})
	return err
}

// Recover is called only after the authoritative workspace is locked. It
// never re-runs work whose previous outcome couldn't be observed.
func (q *Queue) Recover(ctx context.Context) error {
	return q.write(ctx, func(tx *sql.Tx, now int64) error {
		_, err := tx.ExecContext(ctx, `UPDATE sandbox_dispatches SET status='interrupted',updated_at=?,lease='',lease_until=0 WHERE status IN ('running','cancel_requested')`, now)
		return err
	})
}

func (q *Queue) enqueue(ctx context.Context, task string) (out View, err error) {
	err = q.write(ctx, func(tx *sql.Tx, now int64) error {
		current, err := sqlView(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM sandbox_dispatches WHERE task_id=? AND status IN ('pending','running','cancel_requested')`, task))
		if err == nil {
			out = current
			return nil
		}
		if !errors.Is(err, errNotFound) {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sandbox_dispatches WHERE status='pending'`).Scan(&count); err != nil {
			return err
		}
		if count >= 48 {
			return errLimit
		}
		id := ids.New()
		res, err := tx.ExecContext(ctx, `INSERT INTO sandbox_dispatches(id,task_id,task_rev,title,detail,project_id,status,created_at,updated_at,expires_at)
 SELECT ?,t.id,t.updated_at,t.title,t.detail,t.project_id,'pending',?,?,? FROM tasks t
 WHERE t.id=? AND length(CAST(t.title AS BLOB))<=512 AND length(CAST(t.detail AS BLOB))<=12000 AND `+eligible,
			id, now, now, now+int64((24*time.Hour)/time.Millisecond), task)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE id=?`, task).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return errNotFound
			}
			return errIneligible
		}
		out, err = sqlView(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM sandbox_dispatches WHERE id=?`, id))
		return err
	})
	return
}

// latest is read-only: polling an open task must not acquire the store's
// writer mutex or sweep unrelated dispatches. The next successful mutation
// persists the same lazy transitions atomically before changing anything.
func (q *Queue) latest(ctx context.Context, task string) (*View, error) {
	now := q.now().UnixMilli()
	v, err := sqlView(q.store.DB().QueryRowContext(ctx, `SELECT id,task_id,
 CASE WHEN status IN ('running','cancel_requested') AND lease_until<=?1 THEN 'interrupted'
      WHEN status='pending' AND expires_at<=?1 THEN 'expired'
      WHEN status='pending' AND NOT EXISTS(`+sameTask+`) THEN 'stale'
      WHEN status='running' AND NOT EXISTS(`+sameTask+`) THEN 'cancel_requested'
      ELSE status END,
 changes,updated_at,expires_at FROM sandbox_dispatches WHERE task_id=?2 ORDER BY created_at DESC,rowid DESC LIMIT 1`, now, task))
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (q *Queue) cancel(ctx context.Context, id string) (out View, err error) {
	err = q.write(ctx, func(tx *sql.Tx, now int64) error {
		v, err := sqlView(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM sandbox_dispatches WHERE id=?`, id))
		if err != nil {
			return err
		}
		var to string
		switch v.Status {
		case "pending":
			to = "cancelled"
		case "running":
			to = "cancel_requested"
		}
		if to != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE sandbox_dispatches SET status=?,updated_at=? WHERE id=?`, to, now, id); err != nil {
				return err
			}
			v.Status, v.UpdatedAt = to, now
		}
		out = v
		return nil
	})
	return
}

func (q *Queue) pending(ctx context.Context) ([]Intent, error) {
	rows, err := q.store.DB().QueryContext(ctx, `SELECT `+cols+`,title,detail FROM sandbox_dispatches
 WHERE status='pending' AND expires_at>? AND EXISTS(`+sameTask+`) ORDER BY created_at,rowid LIMIT 48`, q.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Intent{}
	for rows.Next() {
		var x Intent
		if err := rows.Scan(&x.ID, &x.TaskID, &x.Status, &x.Changes, &x.UpdatedAt, &x.ExpiresAt, &x.Title, &x.Detail); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (q *Queue) claim(ctx context.Context, id string) (lease string, err error) {
	err = q.write(ctx, func(tx *sql.Tx, now int64) error {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sandbox_dispatches WHERE status IN ('running','cancel_requested')`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errBusy
		}
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return err
		}
		token := hex.EncodeToString(secret[:])
		res, err := tx.ExecContext(ctx, `UPDATE sandbox_dispatches SET lease=?,lease_until=?,status='running',updated_at=? WHERE id=? AND status='pending'`, token, now+int64(leaseMillis), now, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return errConflict
		}
		lease = token
		return nil
	})
	return
}

func (q *Queue) heartbeat(ctx context.Context, id, lease string) (shouldCancel bool, err error) {
	err = q.write(ctx, func(tx *sql.Tx, now int64) error {
		var status string
		err := tx.QueryRowContext(ctx, `SELECT status FROM sandbox_dispatches WHERE id=? AND lease=? AND status IN ('running','cancel_requested')`, id, lease).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return errConflict
		}
		if err != nil {
			return err
		}
		shouldCancel = status == "cancel_requested"
		_, err = tx.ExecContext(ctx, `UPDATE sandbox_dispatches SET lease_until=? WHERE id=? AND lease=?`, now+int64(leaseMillis), id, lease)
		return err
	})
	return
}

func (q *Queue) complete(ctx context.Context, id, lease, outcome string, changes int) (out View, err error) {
	if len(lease) != 64 || changes < 0 || changes > maxChanges {
		return out, errInvalid
	}
	if outcome != "ready" && outcome != "failed" && outcome != "cancelled" && outcome != "interrupted" || outcome != "ready" && changes != 0 {
		return out, errInvalid
	}
	err = q.write(ctx, func(tx *sql.Tx, now int64) error {
		v, err := sqlView(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM sandbox_dispatches WHERE id=? AND lease=?`, id, lease))
		if errors.Is(err, errNotFound) {
			return errConflict
		}
		if err != nil {
			return err
		}
		if v.Status == outcome && v.Changes == changes {
			out = v
			return nil
		} // same completion can be retried
		if v.Status != "running" && v.Status != "cancel_requested" {
			return errConflict
		}
		// Do not claim cancellation before host cleanup. If a prepared report
		// won the race with a cancel request, only explicit ready reports win.
		if _, err := tx.ExecContext(ctx, `UPDATE sandbox_dispatches SET status=?,changes=?,updated_at=?,lease_until=0 WHERE id=?`, outcome, changes, now, id); err != nil {
			return err
		}
		v.Status, v.Changes, v.UpdatedAt = outcome, changes, now
		out = v
		return nil
	})
	return
}
