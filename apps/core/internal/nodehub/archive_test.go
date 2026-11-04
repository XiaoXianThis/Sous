package nodehub

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/ids"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestArchiveClosedTasks(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	log := eventlog.New(st, event.NewBus())
	h, err := New(log, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mk := func(final string) string {
		tk := event.New(event.TaskCreated, event.ActorUser, "", event.TaskCreatedPayload{Kind: "general", Title: "t", Status: "planned"})
		tk.TaskID = ids.New()
		q := event.New(event.RunQueued, event.ActorSystem, "", event.RunQueuedPayload{NodeID: "n", Agent: "a", Kind: "general", Prompt: "p"})
		q.RunID, q.TaskID = ids.New(), tk.TaskID
		end := event.New(event.RunEnded, event.ActorSystem, "", event.RunEndedPayload{Status: "succeeded"})
		end.RunID, end.TaskID = q.RunID, tk.TaskID
		s := event.New(event.TaskStatus, event.ActorSystem, "", event.TaskStatusPayload{From: "running", To: final})
		s.TaskID = tk.TaskID
		if _, err := log.Append(ctx, tk, q, end, s); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(h.LogPath(q.RunID), []byte("log of "+q.RunID), 0o600)
		os.WriteFile(h.DiffPath(q.RunID), []byte("+diff"), 0o600)
		return q.RunID
	}
	done, open := mk(event.TaskDone), mk(event.TaskBlocked)

	n, err := h.Archive(ctx, time.Now().Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("archived %d, %v", n, err)
	}
	if _, err := os.Stat(h.LogPath(done)); !os.IsNotExist(err) {
		t.Fatal("the log of the closed task is still there")
	}
	if _, err := os.Stat(filepath.Join(h.ArchiveDir(), done+".log.gz")); err != nil {
		t.Fatal(err)
	}
	if b, _ := h.ReadRunFile(h.LogPath(done)); string(b) != "log of "+done {
		t.Fatalf("archived log reads back as %q", b)
	}
	if b, _ := h.ReadRunFile(h.DiffPath(done)); string(b) != "+diff" {
		t.Fatalf("archived diff reads back as %q", b)
	}
	if _, err := os.Stat(h.LogPath(open)); err != nil {
		t.Fatal("an open task's log was archived")
	}
	if r, _ := st.Run(ctx, done); !r.Archived {
		t.Fatal("run not marked archived")
	}
	if n, _ := h.Archive(ctx, time.Now().Add(time.Hour)); n != 0 {
		t.Fatalf("archived twice: %d", n)
	}
	// Newer than the cutoff: kept.
	recent := mk(event.TaskDone)
	if n, _ := h.Archive(ctx, time.Now().Add(-time.Hour)); n != 0 {
		t.Fatalf("archived a recent task: %d", n)
	}
	if _, err := os.Stat(h.LogPath(recent)); err != nil {
		t.Fatal(err)
	}
}
