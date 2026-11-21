package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// HasCredentials is the cheap public discovery path; it does not load keys.
func (s *Store) HasCredentials(ctx context.Context) (bool, error) {
	var has bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credentials)`).Scan(&has)
	return has, err
}

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

// SessionExpires returns the deadline of a live login session.
func (s *Store) SessionExpires(ctx context.Context, tokenHash string) (time.Time, error) {
	var ms int64
	err := s.db.QueryRowContext(ctx, `SELECT expires_at FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, time.Now().UnixMilli()).Scan(&ms)
	return time.UnixMilli(ms), err
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

const (
	setupTokenKey   = "auth.setup_token_hash"
	setupExpiresKey = "auth.setup_token_expires"
)

func (s *Store) ReplacePasskeySetup(ctx context.Context, hash string, expires time.Time) error {
	_, _, err := s.Write(ctx, func(w *Writer) error {
		_, err := w.Tx().ExecContext(ctx, `INSERT INTO settings(key,value) VALUES (?,?),(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, setupTokenKey, hash, setupExpiresKey, expires.UTC().Format(time.RFC3339Nano))
		return err
	})
	return err
}

func (s *Store) PasskeySetup(ctx context.Context) (string, time.Time, error) {
	var hash, expiration string
	err := s.db.QueryRowContext(ctx, `SELECT tok.value,exp.value FROM settings AS tok JOIN settings AS exp ON exp.key=? WHERE tok.key=?`, setupExpiresKey, setupTokenKey).Scan(&hash, &expiration)
	if err != nil {
		return "", time.Time{}, err
	}
	expires, err := time.Parse(time.RFC3339Nano, expiration)
	return hash, expires, err
}

// RegisterWithSetup consumes the exact challenge authorization and writes the
// credential in one writer transaction. A parallel ceremony, token rotation,
// expiry, or another first enrollment cannot register a second owner key.
func (s *Store) RegisterWithSetup(ctx context.Context, hash, id, data string, firstOnly bool) error {
	_, _, err := s.Write(ctx, func(w *Writer) error {
		tx := w.Tx()
		var expiration string
		err := tx.QueryRowContext(ctx, `SELECT exp.value FROM settings AS tok JOIN settings AS exp ON exp.key=? WHERE tok.key=? AND tok.value=?`, setupExpiresKey, setupTokenKey, hash).Scan(&expiration)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		expires, err := time.Parse(time.RFC3339Nano, expiration)
		if err != nil || !time.Now().Before(expires) {
			return ErrNotFound
		}
		if firstOnly {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM credentials)`).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return ErrNotFound
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO credentials(id,data,created_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, id, data, time.Now().UnixMilli())
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key IN (?,?)`, setupTokenKey, setupExpiresKey)
		return err
	})
	return err
}
