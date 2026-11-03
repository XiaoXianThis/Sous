package store

import (
	"context"
	"fmt"
)

// migrations only ever grow; each entry upgrades the schema by one version.
var migrations = []string{
	// 1: events, conversation views, effects, usage, auth and settings.
	`
CREATE TABLE events (
  seq       INTEGER PRIMARY KEY AUTOINCREMENT,
  id        TEXT NOT NULL UNIQUE,
  ts        INTEGER NOT NULL,
  type      TEXT NOT NULL,
  actor     TEXT NOT NULL,
  thread_id TEXT,
  task_id   TEXT,
  run_id    TEXT,
  payload   TEXT NOT NULL
);
CREATE INDEX events_thread ON events(thread_id, seq);

CREATE TABLE threads (
  id         TEXT PRIMARY KEY,
  title      TEXT NOT NULL,
  kind       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  archived   INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX threads_main ON threads(kind) WHERE kind = 'main';

CREATE TABLE messages (
  id         TEXT PRIMARY KEY,
  thread_id  TEXT NOT NULL REFERENCES threads(id),
  role       TEXT NOT NULL,
  content    TEXT NOT NULL,
  error      INTEGER NOT NULL DEFAULT 0,
  reply_to   INTEGER NOT NULL DEFAULT 0,
  event_seq  INTEGER NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX messages_thread ON messages(thread_id, event_seq);

CREATE TABLE usage (
  event_seq     INTEGER PRIMARY KEY,
  ts            INTEGER NOT NULL,
  provider      TEXT NOT NULL,
  model         TEXT NOT NULL,
  role          TEXT NOT NULL,
  input_tokens  INTEGER NOT NULL,
  output_tokens INTEGER NOT NULL,
  cached_tokens INTEGER NOT NULL,
  thread_id     TEXT,
  task_id       TEXT,
  run_id        TEXT
);

CREATE TABLE effects (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  kind       TEXT NOT NULL,
  key        TEXT NOT NULL,
  payload    BLOB,
  attempts   INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  due_at     INTEGER NOT NULL,
  UNIQUE(kind, key)
);
CREATE INDEX effects_due ON effects(due_at);

CREATE TABLE credentials (
  id         TEXT PRIMARY KEY,
  data       TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  csrf       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`,
	// 2: memories, and a kind for messages (notices, silent turns).
	`
ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT '';

CREATE TABLE memories (
  id           TEXT PRIMARY KEY,
  kind         TEXT NOT NULL,
  content      TEXT NOT NULL,
  source_event INTEGER NOT NULL,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
CREATE INDEX memories_kind ON memories(kind, updated_at);
`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v)
	if err != nil {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version(version) VALUES (0)`); err != nil {
			return err
		}
	}
	if v > len(migrations) {
		return fmt.Errorf("database schema %d is newer than this build (%d)", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
