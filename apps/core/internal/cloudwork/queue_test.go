package cloudwork

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestQueueFencesAuthorizationLeasesAndIdempotentReceipts(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	token := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	q, err := New(ctx, st, token)
	if err != nil {
		t.Fatal(err)
	}
	q.now = func() time.Time { return now }
	write := func(sql string, args ...any) {
		t.Helper()
		_, _, err := st.Write(ctx, func(w *store.Writer) error { _, e := w.Tx().Exec(sql, args...); return e })
		if err != nil {
			t.Fatal(err)
		}
	}
	write(`INSERT INTO projects(id,name,archived_at,created_at,updated_at) VALUES('p','Cloud',0,1,1)`)
	write(`INSERT INTO tasks(id,title,detail,status,kind,project_id,updated_at,created_at) VALUES('t','cloud task','review input','inbox','general','p',100,100)`)
	files := Bundle{"input.txt": []byte("one\n")}
	job, err := q.Enqueue(ctx, "t", Authorize{RequestID: "123e4567-e89b-12d3-a456-426614174000", Revision: 100, Script: "cat input.txt > output.txt", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := q.Claim(ctx)
	if err != nil || claim == nil {
		t.Fatalf("claim %v", err)
	}
	if claim.JobID != job.ID || claim.Workload != Workload || string(claim.Files["input.txt"]) != "one\n" {
		t.Fatalf("bad claim %+v", claim)
	}
	if stop, err := q.Heartbeat(ctx, claim.JobID, claim.Lease); err != nil || stop {
		t.Fatalf("heartbeat %v %v", stop, err)
	}
	result := Complete{JobID: claim.JobID, Lease: claim.Lease, Outcome: "succeeded", ExitCode: 0, Files: Bundle{"output.txt": []byte("one\n")}}
	if _, err := q.Complete(ctx, result); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Complete(ctx, result); err != nil {
		t.Fatalf("idempotent receipt: %v", err)
	}
	if _, err := q.Complete(ctx, Complete{JobID: claim.JobID, Lease: claim.Lease, Outcome: "failed", ExitCode: 1}); err == nil {
		t.Fatal("rewrote terminal receipt")
	}
	if _, err := q.Enqueue(ctx, "t", Authorize{RequestID: "123e4567-e89b-12d3-a456-426614174001", Revision: 100, BaseJobID: claim.JobID, Script: "cat output.txt"}); err != nil {
		t.Fatalf("base job: %v", err)
	}
	write(`UPDATE tasks SET detail='edited',updated_at=101 WHERE id='t'`)
	if claim, err := q.Claim(ctx); err != nil || claim != nil {
		t.Fatalf("claimed stale or conflicting work: %+v %v", claim, err)
	}
}

func TestCloudGrantHasSeparateAuthorityAndExpiry(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	q, err := New(ctx, st, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	q.now = func() time.Time { return start }
	write := func(stmt string, args ...any) {
		t.Helper()
		_, _, err := st.Write(ctx, func(w *store.Writer) error { _, err := w.Tx().Exec(stmt, args...); return err })
		if err != nil {
			t.Fatal(err)
		}
	}
	write(`INSERT INTO projects(id,name,created_at,updated_at) VALUES('one','One',1,1),('two','Two',1,1)`)
	write(`INSERT INTO tasks(id,title,project_id,status,kind,node_pref,updated_at,created_at) VALUES('a','a','one','planned','general','',1,1),('b','b','two','planned','general','',1,1),('c','local','one','planned','general','desktop',1,1)`)
	write(`INSERT INTO task_deps(task_id,depends_on) VALUES('a','b')`)
	grant := Authorize{RequestID: "123e4567-e89b-12d3-a456-426614174002", Revision: 1, Script: "printf result", Files: Bundle{"input.txt": []byte("fixture")}}
	if _, err := q.Enqueue(ctx, "a", grant); !errors.Is(err, ErrConflict) {
		t.Fatalf("an unmet dependency triggered compute: %v", err)
	}
	if _, err := q.Enqueue(ctx, "c", grant); !errors.Is(err, ErrConflict) {
		t.Fatalf("redirected a local task: %v", err)
	}
	write(`DELETE FROM task_deps WHERE task_id='a'`)
	job, err := q.Enqueue(ctx, "a", grant)
	if err != nil {
		t.Fatal(err)
	}
	dup, err := q.Enqueue(ctx, "a", grant)
	if err != nil || dup.ID != job.ID {
		t.Fatalf("same request did not deduplicate: %+v %v", dup, err)
	}
	modified := grant
	modified.Script = "echo changed"
	if _, err := q.Enqueue(ctx, "a", modified); !errors.Is(err, ErrConflict) {
		t.Fatal("idempotency key allowed a different command")
	}
	claim, err := q.Claim(ctx)
	if err != nil || claim == nil {
		t.Fatal("missing claim", err)
	}
	if another, err := q.Claim(ctx); err != nil || another != nil {
		t.Fatalf("second worker received an active grant: %+v %v", another, err)
	}
	start = start.Add(leaseDuration + time.Second)
	latest, err := q.Latest(ctx, "a")
	if err != nil || latest.Status != "interrupted" {
		t.Fatalf("lease expiry: %+v %v", latest, err)
	}
	if _, err := q.Complete(ctx, Complete{JobID: claim.JobID, Lease: claim.Lease, Outcome: "succeeded", ExitCode: 0, Files: Bundle{"old.txt": []byte("result")}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired worker could publish result: %v", err)
	}
	if next, err := q.Claim(ctx); err != nil || next != nil {
		t.Fatal("uncertain execution automatically replayed", err)
	}
	grant.RequestID = "123e4567-e89b-12d3-a456-426614174003"
	if _, err := q.Enqueue(ctx, "a", grant); err != nil {
		t.Fatal(err)
	}
	claim, err = q.Claim(ctx)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	if _, err := q.Complete(ctx, Complete{JobID: claim.JobID, Lease: claim.Lease, Outcome: "succeeded", ExitCode: 0, Files: Bundle{"a.txt": []byte("one")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, "b", Authorize{RequestID: "123e4567-e89b-12d3-a456-426614174004", Revision: 1, Script: "cat a.txt", BaseJobID: claim.JobID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused files across project authorization: %v", err)
	}
}

func TestCloudBundlePortableAndBounded(t *testing.T) {
	for _, f := range []Bundle{
		{".env": []byte("fixture")}, {"../out": nil}, {"A": nil, "a": nil},
		{"Dir/a": nil, "dir/b": nil}, {"a": nil, "a/file": nil},
		{"huge": make([]byte, MaxBytes+1)},
	} {
		if err := f.Validate(false); !errors.Is(err, ErrBadInput) {
			t.Fatal("accepted out-of-policy bundle")
		}
	}
	if err := (Bundle{"docs/a.txt": []byte("one"), "docs/b.txt": []byte("two")}).Validate(false); err != nil {
		t.Fatal(err)
	}
}
