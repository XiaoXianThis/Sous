package cloudwork

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

const fields = `id,task_id,status,base_job_id,files,exit_code,created_at,expires_at,updated_at`
const eligible = `t.status IN ('inbox','planned') AND t.node_pref='' AND t.project_id<>''
 AND EXISTS(SELECT 1 FROM projects p WHERE p.id=t.project_id AND p.archived_at=0)
 AND NOT EXISTS(SELECT 1 FROM task_deps d LEFT JOIN tasks dep ON dep.id=d.depends_on
                  WHERE d.task_id=t.id AND (dep.status IS NULL OR dep.status<>'done'))`
const currentTask = `SELECT 1 FROM tasks t WHERE t.id=cloud_work.task_id AND t.updated_at=cloud_work.task_rev
 AND t.title=cloud_work.title AND t.detail=cloud_work.detail AND t.project_id=cloud_work.project_id AND ` + eligible

const leaseDuration = 45 * time.Second
const authorizationTTL = 30 * time.Minute
const maxQueued = 8
const maxJobs = 64

var requestID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type Queue struct {
	st             *store.Store
	now            func() time.Time
	tokenHash      string
	controllerHash string
	idleTimeout    time.Duration
}

func New(ctx context.Context, st *store.Store, token string) (*Queue, error) {
	if !ValidToken(token) {
		return nil, ErrBadInput
	}
	var managed bool
	if err := st.DB().QueryRowContext(ctx, `SELECT managed FROM cloud_capacity WHERE slot=1`).Scan(&managed); err != nil {
		return nil, err
	}
	if managed {
		return nil, errors.New("managed cloud capacity cannot fall back to a static worker credential")
	}
	return &Queue{st: st, now: time.Now, tokenHash: Hash([]byte(token))}, nil
}

func readJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.TaskID, &j.Status, &j.BaseJobID, &j.Files, &j.ExitCode, &j.CreatedAt, &j.ExpiresAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrMissing
	}
	return j, err
}

// No effect is restarted merely because the authoritative Hub restarts. A
// current worker may finish under its persisted, unexpired lease. After lease
// expiry the attempt is interrupted and never automatically requeued.
func (q *Queue) mutate(ctx context.Context, fn func(*sql.Tx, int64) error) error {
	_, _, err := q.st.Write(ctx, func(w *store.Writer) error {
		tx := w.Tx()
		now := q.now().UnixMilli()
		if _, err := tx.ExecContext(ctx, `UPDATE cloud_work SET status=CASE WHEN expires_at<=?1 THEN 'expired' ELSE 'stale' END,updated_at=?1,script='',input=X''
 WHERE status='queued' AND (expires_at<=?1 OR NOT EXISTS(`+currentTask+`))`, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cloud_work SET status='cancel_requested',updated_at=? WHERE status='running' AND NOT EXISTS(`+currentTask+`)`, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cloud_work SET status='interrupted',lease_hash='',lease_until=0,updated_at=?1,script='',input=X''
 WHERE status IN ('running','cancel_requested') AND lease_until<=?1`, now); err != nil {
			return err
		}
		return fn(tx, now)
	})
	return err
}

func (q *Queue) Enqueue(ctx context.Context, taskID string, grant Authorize) (j Job, err error) {
	if !ValidScript(grant.Script) || !requestID.MatchString(grant.RequestID) || grant.Revision <= 0 || len(grant.BaseJobID) > 64 || (grant.BaseJobID != "" && len(grant.Files) != 0) {
		return j, ErrBadInput
	}
	if grant.BaseJobID == "" {
		if err := grant.Files.Validate(false); err != nil {
			return j, err
		}
	}
	payload, err := json.Marshal(grant)
	if err != nil {
		return j, err
	}
	hash := Hash(payload)
	err = q.mutate(ctx, func(tx *sql.Tx, now int64) error {
		var prevID, prevTask, prevHash string
		err := tx.QueryRowContext(ctx, `SELECT id,task_id,request_hash FROM cloud_work WHERE request_id=?`, grant.RequestID).Scan(&prevID, &prevTask, &prevHash)
		if err == nil {
			if prevTask != taskID || prevHash != hash {
				return ErrConflict
			}
			j, err = readJob(tx.QueryRowContext(ctx, `SELECT `+fields+` FROM cloud_work WHERE id=?`, prevID))
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var total, queued, active int
		if err := tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE status='queued'),count(*) FILTER(WHERE task_id=? AND status IN ('queued','running','cancel_requested')) FROM cloud_work`, taskID).Scan(&total, &queued, &active); err != nil {
			return err
		}
		if active > 0 {
			return ErrConflict
		}
		if total >= maxJobs || queued >= maxQueued {
			return ErrCapacity
		}
		var project, title, detail string
		err = tx.QueryRowContext(ctx, `SELECT t.project_id,t.title,t.detail FROM tasks t WHERE t.id=? AND t.updated_at=? AND `+eligible, taskID, grant.Revision).Scan(&project, &title, &detail)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		var input []byte
		if grant.BaseJobID != "" {
			err = tx.QueryRowContext(ctx, `SELECT result FROM cloud_work WHERE id=? AND project_id=? AND status='succeeded' AND files>0`, grant.BaseJobID, project).Scan(&input)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
		} else {
			input, err = json.Marshal(grant.Files)
			if err != nil {
				return err
			}
		}
		id := ids.New()
		_, err = tx.ExecContext(ctx, `INSERT INTO cloud_work(id,request_id,request_hash,task_id,task_rev,title,detail,project_id,base_job_id,script,input,status,created_at,updated_at,expires_at)
 VALUES (?,?,?,?,?,?,?,?,?,?,?,'queued',?,?,?)`, id, grant.RequestID, hash, taskID, grant.Revision, title, detail, project, grant.BaseJobID, grant.Script, input, now, now, now+authorizationTTL.Milliseconds())
		if err != nil {
			return err
		}
		j, err = readJob(tx.QueryRowContext(ctx, `SELECT `+fields+` FROM cloud_work WHERE id=?`, id))
		return err
	})
	return j, err
}

func (q *Queue) Claim(ctx context.Context) (claim *Claim, err error) {
	err = q.mutate(ctx, func(tx *sql.Tx, now int64) error {
		if err := q.requireWorker(ctx, tx, now); err != nil {
			return err
		}
		var id, script string
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT id,script,input FROM cloud_work WHERE status='queued'
 AND NOT EXISTS(SELECT 1 FROM cloud_work WHERE status IN ('running','cancel_requested')) ORDER BY created_at,rowid LIMIT 1`).Scan(&id, &script, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return q.observeIdlePoll(ctx, tx, now)
		}
		if err != nil {
			return err
		}
		var files Bundle
		if err := json.Unmarshal(raw, &files); err != nil {
			return err
		}
		if err := files.Validate(false); err != nil {
			return err
		}
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			return err
		}
		token := hex.EncodeToString(secret[:])
		_, err = tx.ExecContext(ctx, `UPDATE cloud_work SET status='running',lease_hash=?,lease_until=?,updated_at=? WHERE id=?`, Hash([]byte(token)), now+leaseDuration.Milliseconds(), now, id)
		if err != nil {
			return err
		}
		claim = &Claim{id, Workload, token, script, files}
		if q.managed() {
			_, err = tx.ExecContext(ctx, `UPDATE cloud_capacity SET idle_since=0,worker_seen=0 WHERE slot=1`)
		}
		return err
	})
	return claim, err
}

func (q *Queue) Heartbeat(ctx context.Context, id, lease string) (stop bool, err error) {
	if !ValidToken(lease) || len(id) > 64 {
		return false, ErrBadInput
	}
	err = q.mutate(ctx, func(tx *sql.Tx, now int64) error {
		if err := q.requireWorker(ctx, tx, now); err != nil {
			return err
		}
		var status string
		hash := Hash([]byte(lease))
		err := tx.QueryRowContext(ctx, `SELECT status FROM cloud_work WHERE id=? AND lease_hash=? AND status IN ('running','cancel_requested')`, id, hash).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		stop = status == "cancel_requested"
		_, err = tx.ExecContext(ctx, `UPDATE cloud_work SET lease_until=? WHERE id=?`, now+leaseDuration.Milliseconds(), id)
		return err
	})
	return stop, err
}

func (q *Queue) Complete(ctx context.Context, c Complete) (j Job, err error) {
	if err := c.Validate(); err != nil {
		return j, err
	}
	raw, err := json.Marshal(c.Files)
	if err != nil {
		return j, err
	}
	digest := Hash(raw)
	err = q.mutate(ctx, func(tx *sql.Tx, now int64) error {
		if err := q.requireWorker(ctx, tx, now); err != nil {
			return err
		}
		hash := Hash([]byte(c.Lease))
		var prevStatus, prevHash string
		var exit int
		if err := tx.QueryRowContext(ctx, `SELECT status,result_hash,exit_code FROM cloud_work WHERE id=? AND lease_hash=?`, c.JobID, hash).Scan(&prevStatus, &prevHash, &exit); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			return err
		}
		if prevStatus != c.Outcome || prevHash != digest || exit != c.ExitCode {
			if prevStatus != "running" && prevStatus != "cancel_requested" {
				return ErrConflict
			}
			_, err := tx.ExecContext(ctx, `UPDATE cloud_work SET status=?,result=?,result_hash=?,files=?,exit_code=?,updated_at=?,script='',input=X'',lease_until=0 WHERE id=?`, c.Outcome, raw, digest, len(c.Files), c.ExitCode, now, c.JobID)
			if err != nil {
				return err
			}
		}
		j, err = readJob(tx.QueryRowContext(ctx, `SELECT `+fields+` FROM cloud_work WHERE id=?`, c.JobID))
		return err
	})
	return j, err
}

func (q *Queue) Cancel(ctx context.Context, id string) (j Job, err error) {
	err = q.mutate(ctx, func(tx *sql.Tx, now int64) error {
		_, err := tx.ExecContext(ctx, `UPDATE cloud_work SET status=CASE status WHEN 'queued' THEN 'cancelled' ELSE 'cancel_requested' END,
 script=CASE status WHEN 'queued' THEN '' ELSE script END,input=CASE status WHEN 'queued' THEN X'' ELSE input END,updated_at=? WHERE id=? AND status IN ('queued','running')`, now, id)
		if err != nil {
			return err
		}
		j, err = readJob(tx.QueryRowContext(ctx, `SELECT `+fields+` FROM cloud_work WHERE id=?`, id))
		return err
	})
	return j, err
}

func (q *Queue) Latest(ctx context.Context, taskID string) (*Job, error) {
	j, err := readJob(q.st.DB().QueryRowContext(ctx, `SELECT id,task_id,
 CASE WHEN status IN ('running','cancel_requested') AND lease_until<=?1 THEN 'interrupted'
 WHEN status='queued' AND expires_at<=?1 THEN 'expired'
 WHEN status='queued' AND NOT EXISTS(`+currentTask+`) THEN 'stale'
 WHEN status='running' AND NOT EXISTS(`+currentTask+`) THEN 'cancel_requested' ELSE status END,
 base_job_id,files,exit_code,created_at,expires_at,updated_at FROM cloud_work WHERE task_id=?2 ORDER BY created_at DESC,rowid DESC LIMIT 1`, q.now().UnixMilli(), taskID))
	if errors.Is(err, ErrMissing) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (q *Queue) Results(ctx context.Context, id string) (Bundle, error) {
	var raw []byte
	err := q.st.DB().QueryRowContext(ctx, `SELECT result FROM cloud_work WHERE id=? AND status IN ('succeeded','failed')`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMissing
	}
	if err != nil {
		return nil, err
	}
	var output Bundle
	if err := json.Unmarshal(raw, &output); err != nil {
		return nil, err
	}
	return output, nil
}

// ProjectResults returns candidate previously acknowledged snapshots (no contents).
func (q *Queue) ProjectResults(ctx context.Context, id string) ([]Job, error) {
	rows, err := q.st.DB().QueryContext(ctx, `SELECT `+fields+` FROM cloud_work WHERE project_id=? AND status='succeeded' AND files>0 ORDER BY created_at DESC,rowid DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := readJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
