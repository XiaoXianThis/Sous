package store

import (
	"context"
	"time"
)

// DueEffects returns up to limit effects whose due time has passed, oldest first.
func (s *Store) DueEffects(ctx context.Context, now time.Time, limit int) ([]Effect, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, key, payload, attempts, last_error, due_at FROM effects
		 WHERE due_at <= ? ORDER BY id LIMIT ?`, now.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Effect
	for rows.Next() {
		var e Effect
		var due int64
		if err := rows.Scan(&e.ID, &e.Kind, &e.Key, &e.Payload, &e.Attempts, &e.LastError, &due); err != nil {
			return nil, err
		}
		e.DueAt = time.UnixMilli(due)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CompleteEffect removes an effect that ran successfully.
func (s *Store) CompleteEffect(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM effects WHERE id = ?`, id)
	return err
}

// RetryEffect records a failed attempt and reschedules the effect.
func (s *Store) RetryEffect(ctx context.Context, id int64, errMsg string, due time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE effects SET attempts = attempts + 1, last_error = ?, due_at = ? WHERE id = ?`,
		errMsg, due.UnixMilli(), id)
	return err
}

// FailingEffects returns effects that failed at least n times in a row.
func (s *Store) FailingEffects(ctx context.Context, n int) ([]Effect, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, key, payload, attempts, last_error, due_at FROM effects WHERE attempts >= ? ORDER BY id`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Effect
	for rows.Next() {
		var e Effect
		var due int64
		if err := rows.Scan(&e.ID, &e.Kind, &e.Key, &e.Payload, &e.Attempts, &e.LastError, &due); err != nil {
			return nil, err
		}
		e.DueAt = time.UnixMilli(due)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PendingEffects counts queued effects.
func (s *Store) PendingEffects(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM effects`).Scan(&n)
	return n, err
}
