package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

type ApprovalCheck struct{ ID, Result, Summary string }

func (s *Store) ApprovalCheckSummary(ctx context.Context, id string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT summary,result FROM approval_checks WHERE approval_id=? ORDER BY created_at,id LIMIT 4`, id)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var notes []string
	for rows.Next() {
		var summary, result string
		if err := rows.Scan(&summary, &result); err != nil {
			return "", err
		}
		if result == "" || strings.HasPrefix(result, "inspection failed:") {
			summary += "（未取得有效证据）"
		}
		notes = append(notes, summary)
	}
	return strings.Join(notes, "；"), rows.Err()
}

func (s *Store) ApprovalCheck(ctx context.Context, id string) (ApprovalCheck, error) {
	var c ApprovalCheck
	err := s.db.QueryRowContext(ctx, `SELECT id,result,summary FROM approval_checks WHERE id=?`, id).Scan(&c.ID, &c.Result, &c.Summary)
	return c, err
}
func (w *Writer) ReserveApprovalCheck(id, approval, run string) error {
	var existing int
	if err := w.tx.QueryRowContext(w.ctx, `SELECT count(*) FROM approval_checks WHERE id=?`, id).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return fmt.Errorf("inspection already reserved")
	}
	var status, runStatus string
	var paused int
	if err := w.tx.QueryRowContext(w.ctx, `SELECT a.status,r.status,r.paused FROM approvals a JOIN runs r ON r.id=a.run_id WHERE a.id=? AND a.run_id=?`, approval, run).Scan(&status, &runStatus, &paused); err != nil {
		return err
	}
	if status != "pending" || runStatus != "running" || paused != 0 {
		return ErrApprovalClosed
	}
	var perRun, perApproval, bytes int
	if err := w.tx.QueryRowContext(w.ctx, `SELECT count(*),coalesce(sum(CASE WHEN approval_id=? THEN 1 ELSE 0 END),0),coalesce(sum(CASE WHEN approval_id=? THEN length(CAST(result AS BLOB)) ELSE 0 END),0) FROM approval_checks WHERE run_id=?`, approval, approval, run).Scan(&perRun, &perApproval, &bytes); err != nil {
		return err
	}
	if perRun >= 40 || perApproval >= 4 || bytes >= 64<<10 {
		return fmt.Errorf("inspection budget exhausted")
	}
	return nil
}
func projectApprovalCheck(w *Writer, ev event.Event) error {
	var p event.ApprovalCheckPayload
	if err := ev.Decode(&p); err != nil {
		return err
	}
	_, err := w.tx.ExecContext(w.ctx, `INSERT INTO approval_checks(id,approval_id,run_id,tool,args_digest,summary,result,created_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET summary=excluded.summary,result=excluded.result WHERE approval_checks.result=''`, p.CheckID, p.ApprovalID, ev.RunID, p.Tool, p.ArgsDigest, p.Summary, p.Result, ev.TS)
	return err
}

func (s *Store) ApprovalModelCalls(ctx context.Context, id string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE type='approval.model_called' AND json_extract(payload,'$.approval_id')=?`, id).Scan(&n)
	return n, err
}
func (w *Writer) ReserveApprovalModel(id string) error {
	var status string
	if err := w.tx.QueryRowContext(w.ctx, `SELECT status FROM approvals WHERE id=?`, id).Scan(&status); err != nil {
		return err
	}
	if status != "pending" {
		return ErrApprovalClosed
	}
	var n int
	if err := w.tx.QueryRowContext(w.ctx, `SELECT count(*) FROM events WHERE type='approval.model_called' AND json_extract(payload,'$.approval_id')=?`, id).Scan(&n); err != nil {
		return err
	}
	if n >= 6 {
		return fmt.Errorf("approval model call budget exhausted")
	}
	return nil
}
