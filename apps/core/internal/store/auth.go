package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Credentials returns the stored passkeys as opaque JSON blobs.
func (s *Store) Credentials(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM credentials ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PutCredential inserts or replaces a passkey.
func (s *Store) PutCredential(ctx context.Context, id, data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO credentials(id, data, created_at) VALUES (?,?,?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`,
		id, data, time.Now().UnixMilli())
	return err
}

// DeleteCredentials removes every passkey.
func (s *Store) DeleteCredentials(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM credentials`)
	return err
}

// CreateSession stores a login session by the hash of its token.
func (s *Store) CreateSession(ctx context.Context, tokenHash, csrf string, expires time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash, csrf, created_at, expires_at) VALUES (?,?,?,?)`,
		tokenHash, csrf, now, expires.UnixMilli())
	return err
}

// Session returns a live session's CSRF token, or ErrNotFound.
func (s *Store) Session(ctx context.Context, tokenHash string) (csrf string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT csrf FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, time.Now().UnixMilli()).Scan(&csrf)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return csrf, err
}

// SessionCreated returns when a live session logged in.
func (s *Store) SessionCreated(ctx context.Context, tokenHash string) (time.Time, error) {
	var ms int64
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, time.Now().UnixMilli()).Scan(&ms)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	return time.UnixMilli(ms), err
}

// RefreshSession records a new passkey login on a live session: it keeps
// its token (and so what belongs to it) and counts as just logged in.
func (s *Store) RefreshSession(ctx context.Context, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `UPDATE sessions SET created_at = ? WHERE token_hash = ? AND expires_at > ?`,
		time.Now().UnixMilli(), tokenHash, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteSessions logs out everywhere.
func (s *Store) DeleteSessions(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}
