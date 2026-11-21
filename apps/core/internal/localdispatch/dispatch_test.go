package localdispatch

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func fixture(t *testing.T) (*Queue, context.Context, *time.Time) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, time.November, 20, 14, 0, 0, 0, time.UTC)
	q := New(st)
	q.now = func() time.Time { return now }
	ctx := context.Background()
	for _, id := range []string{"first", "second", "dependency"} {
		write(t, q, `INSERT INTO tasks(id,title,detail,status,kind,created_at,updated_at) VALUES(?,?,'not a command','inbox','general',1,1)`, id, "task "+id)
	}
	return q, ctx, &now
}

func write(t *testing.T, q *Queue, statement string, args ...any) {
	t.Helper()
	_, _, err := q.store.Write(context.Background(), func(w *store.Writer) error {
		_, err := w.Tx().Exec(statement, args...)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDurableIntentRevocationAndNoCrossLeaseCompletion(t *testing.T) {
	q, ctx, now := fixture(t)
	a, err := q.enqueue(ctx, "first")
	if err != nil || a.Status != "pending" {
		t.Fatalf("enqueue: %+v %v", a, err)
	}
	dup, _ := q.enqueue(ctx, "first")
	if dup.ID != a.ID {
		t.Fatal("duplicate request created a concurrent dispatch")
	}
	b, err := q.enqueue(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := q.pending(ctx)
	if err != nil || len(pending) != 2 || pending[0].Detail != "not a command" {
		t.Fatalf("list: %+v %v", pending, err)
	}
	lease, err := q.claim(ctx, a.ID)
	if err != nil || len(lease) != 64 {
		t.Fatalf("claim: %s %v", lease, err)
	}
	if _, err := q.claim(ctx, b.ID); !errors.Is(err, errBusy) {
		t.Fatalf("desktop exceeded single Guest cap: %v", err)
	}
	if _, err := q.complete(ctx, a.ID, strings.Repeat("x", 64), "ready", 1); !errors.Is(err, errConflict) {
		t.Fatalf("foreign lease accepted: %v", err)
	}
	v, err := q.cancel(ctx, a.ID)
	if err != nil || v.Status != "cancel_requested" {
		t.Fatalf("cancelled without waiting for cleanup: %+v %v", v, err)
	}
	if stop, err := q.heartbeat(ctx, a.ID, lease); err != nil || !stop {
		t.Fatalf("active desktop did not see cancellation: %t %v", stop, err)
	}
	if v, err := q.complete(ctx, a.ID, lease, "cancelled", 0); err != nil || v.Status != "cancelled" {
		t.Fatalf("settle: %+v %v", v, err)
	}
	if _, err := q.complete(ctx, a.ID, lease, "cancelled", 0); err != nil {
		t.Fatal("identical completion not idempotent", err)
	}
	if _, err := q.complete(ctx, a.ID, lease, "ready", 1); !errors.Is(err, errConflict) {
		t.Fatal("completion rewrote terminal outcome", err)
	}
	second, err := q.claim(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(31 * time.Second)
	if v, err := q.latest(ctx, "second"); err != nil || v == nil || v.Status != "interrupted" {
		t.Fatalf("lost heartbeat must not imply safe stop: %+v %v", v, err)
	}
	if _, err := q.complete(ctx, b.ID, second, "ready", 1); !errors.Is(err, errConflict) {
		t.Fatalf("old lease still writing: %v", err)
	}
	if v, err := q.latest(ctx, "first"); err != nil || v.Status != "cancelled" {
		t.Fatalf("lost history: %+v %v", v, err)
	}
	var status string
	if err := q.store.DB().QueryRow(`SELECT status FROM tasks WHERE id='first'`).Scan(&status); err != nil || status != "inbox" {
		t.Fatal("controller mutated task workflow status", err)
	}
}

func TestTaskEditDependencyAndRestartFence(t *testing.T) {
	q, ctx, _ := fixture(t)
	write(t, q, `INSERT INTO task_deps(task_id,depends_on) VALUES('first','dependency')`)
	if _, err := q.enqueue(ctx, "first"); !errors.Is(err, errIneligible) {
		t.Fatalf("ignored dependency: %v", err)
	}
	write(t, q, `UPDATE tasks SET status='done' WHERE id='dependency'`)
	old, err := q.enqueue(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	// Content is checked exactly even if tasks have the same timestamp.
	write(t, q, `UPDATE tasks SET detail='edited' WHERE id='first'`)
	if _, err := q.claim(ctx, old.ID); !errors.Is(err, errConflict) {
		t.Fatalf("approved stale intent: %v", err)
	}
	if v, err := q.latest(ctx, "first"); err != nil || v == nil || v.Status != "stale" {
		t.Fatalf("edited intent remained pending: %+v %v", v, err)
	}
	fresh, err := q.enqueue(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	lease, e := q.claim(ctx, fresh.ID)
	if e != nil {
		t.Fatal(e)
	}
	if err := q.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if v, err := q.latest(ctx, "first"); err != nil || v.Status != "interrupted" {
		t.Fatalf("restart did not fence running claim: %+v %v", v, err)
	}
	if _, err := q.heartbeat(ctx, fresh.ID, lease); !errors.Is(err, errConflict) {
		t.Fatal("old desktop still owns claim", err)
	}
	if _, err := q.latest(ctx, "unknown"); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
}

func TestPipeRejectsPathOrCommandInput(t *testing.T) {
	q, ctx, _ := fixture(t)
	v, err := q.enqueue(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := q.Serve(ctx, strings.NewReader(`{"id":1,"action":"claim","dispatch_id":"`+v.ID+`","source":"/Users/example/private","command":"ls"}`+"\n"), &output); !errors.Is(err, errInvalid) || output.Len() != 0 {
		t.Fatalf("unsafe input accepted: %s %v", output.String(), err)
	}
	if err := q.Serve(ctx, strings.NewReader(`{"id":2,"action":"list"}`+"\n"), &output); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Data []Intent `json:"data"`
	}
	if err := json.Unmarshal(output.Bytes(), &body); err != nil || len(body.Data) != 1 || body.Data[0].ID != v.ID {
		t.Fatalf("intent unexpectedly changed: %s %v", output.String(), err)
	}
}
