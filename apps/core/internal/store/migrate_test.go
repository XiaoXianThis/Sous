package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// A database created by an earlier build upgrades in place and keeps its data.
func TestUpgradeFromSchema1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE schema_version (version INTEGER NOT NULL)`,
		`INSERT INTO schema_version(version) VALUES (1)`,
		migrations[0],
		`INSERT INTO threads(id, title, kind, created_at, updated_at) VALUES ('t', '主线', 'main', 1, 1)`,
		`INSERT INTO messages(id, thread_id, role, content, event_seq, created_at) VALUES ('m', 't', 'user', '旧消息', 1, 1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&v)
	if v != len(migrations) {
		t.Fatalf("version %d, want %d", v, len(migrations))
	}
	msgs, err := s.Messages(context.Background(), "t", 0, 10)
	if err != nil || len(msgs) != 1 || msgs[0].Content != "旧消息" || msgs[0].Kind != "" {
		t.Fatalf("messages after upgrade: %v %+v", err, msgs)
	}
}
