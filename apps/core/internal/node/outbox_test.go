package node

import (
	"path/filepath"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

func TestOutboxPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.jsonl")
	o, err := openOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		o.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{RunID: "r", Kind: "status", Text: "x"}))
	}
	o.ack(3)
	o.close()

	o, err = openOutbox(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := o.pendingCount(); n != 2 {
		t.Fatalf("pending after reopen: %d", n)
	}
	if o.pending[0].Seq != 4 {
		t.Fatalf("first pending seq: %d", o.pending[0].Seq)
	}
	// The seq keeps counting, even after everything was acknowledged.
	o.ack(5)
	o.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{RunID: "r"}))
	if o.pending[0].Seq != 6 {
		t.Fatalf("seq after reopen: %d", o.pending[0].Seq)
	}
	o.ack(6)
	o.close()
	o, _ = openOutbox(path)
	if o.pendingCount() != 0 || o.seq != 6 {
		t.Fatalf("pending %d seq %d", o.pendingCount(), o.seq)
	}
	o.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{RunID: "r"}))
	if o.pending[0].Seq != 7 {
		t.Fatalf("seq after compaction: %d", o.pending[0].Seq)
	}
}

func TestOutboxCompacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.jsonl")
	o, _ := openOutbox(path)
	for i := 1; i <= compactAfter+10; i++ {
		o.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{RunID: "r"}))
		o.ack(int64(i))
	}
	o.reliable(proto.New(proto.TRunUpdate, proto.RunUpdate{RunID: "r", Text: "last"}))
	o.close()
	o, _ = openOutbox(path)
	if o.pendingCount() != 1 || o.pending[0].Seq != compactAfter+11 {
		t.Fatalf("pending %+v", o.pending)
	}
}

func TestTransientErrors(t *testing.T) {
	for _, s := range []string{"npm ERR! network ECONNRESET", "HTTP 429 Too Many Requests", "502 Bad Gateway", "initialize: context deadline exceeded (timeout)"} {
		if !transient(errorString(s)) {
			t.Errorf("%q should be transient", s)
		}
	}
	for _, s := range []string{"找不到 claude 的启动命令", "agent is not logged in", "exit status 1"} {
		if transient(errorString(s)) {
			t.Errorf("%q should not be transient", s)
		}
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

func TestPickMode(t *testing.T) {
	modes := func(ids ...string) acp.Session {
		s := acp.Session{}
		for _, id := range ids {
			s.Modes = append(s.Modes, acp.Mode{ID: id})
		}
		return s
	}
	claude := modes("default", "acceptEdits", "plan", "bypassPermissions")
	cursor := modes("agent", "plan", "ask")
	cursorSpec, _ := agents.Find(agents.Builtin(), "cursor")
	cases := []struct {
		s    acp.Session
		mode string
		spec agents.Spec
		want string
	}{
		{claude, proto.ModeDefault, agents.Spec{}, "default"},
		{claude, proto.ModeOpen, agents.Spec{}, "bypassPermissions"},
		// Cursor's "ask" is read-only Q&A: never pick it.
		{cursor, proto.ModeDefault, agents.Spec{}, ""},
		{cursor, proto.ModeDefault, cursorSpec, "agent"},
		{cursor, proto.ModeOpen, cursorSpec, "agent"},
	}
	for _, c := range cases {
		if got := pickMode(c.s, c.mode, c.spec); got != c.want {
			t.Errorf("pickMode(%v, %s) = %q, want %q", c.s.Modes, c.mode, got, c.want)
		}
	}
}
