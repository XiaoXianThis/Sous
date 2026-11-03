// Package store is the Hub's SQLite storage: the append-only event log, the
// views projected from it, the effects queue, and small auth/settings tables.
//
// All writes go through one Writer at a time (single writer). A write appends
// events, projects them into views and enqueues effects in one transaction.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// memSeq gives each in-memory database its own name, so tests stay isolated.
var memSeq atomic.Int64

type Store struct {
	db *sql.DB
	mu sync.Mutex // serializes writers
}

// Open opens (creating if needed) the database at path and runs migrations.
// Use ":memory:" for tests.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:mem%d?mode=memory&cache=shared", memSeq.Add(1))
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		// A plain path, not a file: URI, so Windows paths with backslashes work.
		dsn = path
	}
	dsn += sep(dsn) + "_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func sep(dsn string) string {
	for _, c := range dsn {
		if c == '?' {
			return "&"
		}
	}
	return "?"
}

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for read-only helpers in other packages' tests.
func (s *Store) DB() *sql.DB { return s.db }

// Backup writes a consistent snapshot of the database to path.
func (s *Store) Backup(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path)
	return err
}

// Effect is a side effect to run after the transaction that enqueued it commits.
type Effect struct {
	ID        int64
	Kind      string
	Key       string
	Payload   []byte
	Attempts  int
	LastError string
	DueAt     time.Time
}

// Writer appends events and enqueues effects inside one transaction.
type Writer struct {
	tx      *sql.Tx
	ctx     context.Context
	events  []event.Event
	effects int
}

// Append stores ev, projects it into the views and returns it with its seq.
// Ephemeral events are rejected; publish them on the bus instead.
func (w *Writer) Append(ev event.Event) (event.Event, error) {
	if ev.Type.Ephemeral() {
		return ev, fmt.Errorf("store: %s is ephemeral", ev.Type)
	}
	res, err := w.tx.ExecContext(w.ctx,
		`INSERT INTO events(id, ts, type, actor, thread_id, task_id, run_id, payload) VALUES (?,?,?,?,?,?,?,?)`,
		ev.ID, ev.TS, string(ev.Type), ev.Actor, null(ev.ThreadID), null(ev.TaskID), null(ev.RunID), string(ev.Payload))
	if err != nil {
		return ev, err
	}
	if ev.Seq, err = res.LastInsertId(); err != nil {
		return ev, err
	}
	if err := project(w, ev); err != nil {
		return ev, fmt.Errorf("project %s: %w", ev.Type, err)
	}
	w.events = append(w.events, ev)
	return ev, nil
}

// Enqueue adds an effect that runs after commit. Effects with the same kind and
// key are deduplicated while pending.
func (w *Writer) Enqueue(kind, key string, payload []byte) error {
	_, err := w.tx.ExecContext(w.ctx,
		`INSERT INTO effects(kind, key, payload, due_at) VALUES (?,?,?,?) ON CONFLICT(kind, key) DO NOTHING`,
		kind, key, payload, time.Now().UnixMilli())
	if err == nil {
		w.effects++
	}
	return err
}

// Tx exposes the transaction for projections that need extra reads.
func (w *Writer) Tx() *sql.Tx { return w.tx }

// Write runs fn in a transaction. On success it returns the events fn appended
// and whether any effects were enqueued.
func (s *Store) Write(ctx context.Context, fn func(w *Writer) error) ([]event.Event, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	w := &Writer{tx: tx, ctx: ctx}
	if err := fn(w); err != nil {
		tx.Rollback()
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return w.events, w.effects > 0, nil
}

func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}
