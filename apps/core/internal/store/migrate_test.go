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

func TestUpgradeFailureRollsBackTheWholeCandidateSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := len(migrations) - 2 // last two migrations are cloud_capacity, external_mcp
	if _, err := db.Exec(`CREATE TABLE schema_version(version INTEGER NOT NULL); INSERT INTO schema_version VALUES(?)`, old); err != nil {
		t.Fatal(err)
	}
	for _, statement := range migrations[:old] {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	// Preserve old data and force a conflict in the *second* pending migration.
	if _, err := db.Exec(`INSERT INTO threads(id,title,kind,created_at,updated_at) VALUES('old','Original','main',1,1);
 CREATE TABLE external_mcp_codes(fixture TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path); err == nil {
		_ = s.Close()
		t.Fatal("migration collision did not fail")
	}
	db, err = sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, capacity int
	var title string
	if err := db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil || version != old {
		t.Fatal("schema version changed after failure", version, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='cloud_capacity'`).Scan(&capacity); err != nil || capacity != 0 {
		t.Fatal("first candidate migration survived a failed upgrade", capacity, err)
	}
	if err := db.QueryRow(`SELECT title FROM threads WHERE id='old'`).Scan(&title); err != nil || title != "Original" {
		t.Fatal("old row was altered", title, err)
	}
}

func TestInitialIdentityIsCommittedWithSchemaAndCannotBeOverwritten(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hub.db")
	s, err := OpenInitialized(path, "workspace.local_identity", "local-v1", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if second, err := OpenInitialized(path, "workspace.hosted_origin", "https://wrong.test", true); err == nil {
		_ = second.Close()
		t.Fatal("replaced existing workspace identity")
	}
	found, empty, err := InspectWorkspace(ctx, path, "workspace.local_identity", "workspace.hosted_origin")
	if err != nil || empty || len(found) != 1 || found["workspace.local_identity"] != "local-v1" {
		t.Fatal("first identity was not retained", found, err)
	}
}
