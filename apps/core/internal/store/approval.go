package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

var ErrApprovalBudget = errors.New("approval budget exhausted")
var ErrApprovalClosed = errors.New("approval no longer pending")

const allowCountSQL = `SELECT count(*) FROM approvals a, json_each(a.options) o
 WHERE a.run_id=? AND a.status='decided' AND a.decided_by='approver'
 AND json_extract(o.value,'$.option_id')=a.option_id
 AND json_extract(o.value,'$.kind') IN ('allow_once','allow_always')`

// ApprovalHistory fetches only model context plus the latest matching explicit
// user answer. Authorization never depends on a UI page or a history cap.
func (s *Store) ApprovalHistory(ctx context.Context, current Approval, recent int) ([]Approval, error) {
	key := ""
	if current.Facts != nil {
		key = current.Facts.OperationKey
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+approvalCols+` FROM approvals WHERE rowid IN (
 SELECT rowid FROM approvals WHERE task_id=? ORDER BY created_at DESC,rowid DESC LIMIT ?
 ) OR rowid = (
 SELECT rowid FROM approvals WHERE task_id=? AND id<>? AND status='decided' AND decided_by='user'
 AND ((category=? AND title=? AND detail=?) OR (?<>'' AND json_extract(CASE WHEN json_valid(facts) THEN facts ELSE '{}' END,'$.operation_key')=?))
 ORDER BY created_at DESC,rowid DESC LIMIT 1
 ) ORDER BY created_at,rowid`, current.TaskID, recent, current.TaskID, current.ID, current.Category, current.Title, current.Detail, key, key)
	if err != nil {
		return nil, err
	}
	return scanApprovals(rows)
}

// ApprovalCounts uses the full history, including approvals beyond the UI page.
func (s *Store) ApprovalCounts(ctx context.Context, run, task, category, title string) (allows, denies int, err error) {
	err = s.db.QueryRowContext(ctx, allowCountSQL, run).Scan(&allows)
	if err != nil {
		return
	}
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM approvals a, json_each(a.options) o
 WHERE a.task_id=? AND a.category=? AND a.title=? AND a.status='decided' AND a.decided_by='approver'
 AND json_extract(o.value,'$.option_id')=a.option_id AND json_extract(o.value,'$.kind') IN ('reject_once','reject_always')`, task, category, title).Scan(&denies)
	return
}

// CheckApprovalDecision must run in the SAME transaction as the decision event.
func (w *Writer) CheckApprovalDecision(id, option, by string, limit int) error {
	var status, run, category, kind, factsJSON string
	err := w.tx.QueryRowContext(w.ctx, `SELECT a.status,a.run_id,a.category,a.facts,coalesce(json_extract(o.value,'$.kind'),'')
 FROM approvals a LEFT JOIN json_each(a.options) o ON json_extract(o.value,'$.option_id')=? WHERE a.id=?`, option, id).Scan(&status, &run, &category, &factsJSON, &kind)
	if err != nil {
		return err
	}
	if status != "pending" {
		return ErrApprovalClosed
	}
	if by != event.ActorApprover || (kind != "allow_once" && kind != "allow_always") {
		return nil
	}
	var runStatus string
	var paused bool
	if err := w.tx.QueryRowContext(w.ctx, `SELECT status,paused FROM runs WHERE id=?`, run).Scan(&runStatus, &paused); err != nil {
		return err
	}
	if paused || (runStatus != "running" && runStatus != "queued") {
		return ErrApprovalClosed
	}
	if category == "floor" || category == "irreversible" {
		return fmt.Errorf("%s cannot be approved by AI", category)
	}
	var facts *proto.PolicyFacts
	if json.Unmarshal([]byte(factsJSON), &facts) == nil && facts != nil {
		if len(facts.Hard) > 0 {
			return fmt.Errorf("hard signal requires user")
		}
		if facts.Role == "server" {
			if facts.OpaqueExecution {
				return fmt.Errorf("server unknown execution requires user")
			}
			for _, effect := range facts.Effects {
				if effect == "edit-worktree" || effect == "delete" || effect == "service" || effect == "irreversible" {
					return fmt.Errorf("server effects require user")
				}
			}
		}
	}
	if limit < 150 || limit > 300 {
		return fmt.Errorf("AI approval limit must be 150..300")
	}
	var n int
	if err := w.tx.QueryRowContext(w.ctx, allowCountSQL, run).Scan(&n); err != nil {
		return err
	}
	if n >= limit {
		return ErrApprovalBudget
	}
	return nil
}
