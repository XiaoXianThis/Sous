package nodehub

import (
	"context"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestMentionsHost(t *testing.T) {
	for _, c := range []struct {
		text, host string
		want       bool
	}{
		{"ssh root@10.0.0.5 'cd /srv && make'", "10.0.0.5", true},
		{"ssh prod-1 uptime", "prod-1", true},
		{"rsync -a dist/ deploy@prod-1.example.com:/srv/app", "prod-1.example.com", true},
		{"ssh 10.0.0.50 uptime", "10.0.0.5", false},
		{"ssh prod-10 uptime", "prod-1", false},
		{"ssh localhost", "localhost", false},
	} {
		if got := mentionsHost(c.text, c.host); got != c.want {
			t.Errorf("mentionsHost(%q, %q) = %v", c.text, c.host, got)
		}
	}
}

// An ssh to another connected machine gets a hint; other commands and
// unknown hosts do not (decision 0025: a hint, not a rule).
func TestPeerNote(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	log := eventlog.New(st, event.NewBus())
	h, err := New(log, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, n := range []event.NodePayload{{NodeID: "mac", Name: "mac", Hostname: "Mac.local"}, {NodeID: "srv", Name: "prod", Hostname: "prod-1.example.com"}} {
		if _, err := log.Append(ctx, event.New(event.NodePaired, event.ActorSystem, "", n)); err != nil {
			t.Fatal(err)
		}
	}
	h.conns["srv"] = &nodeConn{addrs: []string{"203.0.113.7"}}

	if note := h.peerNote(ctx, "mac", "执行命令\nssh deploy@203.0.113.7 'systemctl restart app'"); !strings.Contains(note, "「prod」") {
		t.Fatalf("by address: %q", note)
	}
	if note := h.peerNote(ctx, "mac", "ssh prod-1 'cd /srv/app && git pull'"); !strings.Contains(note, "「prod」") {
		t.Fatalf("by short host name: %q", note)
	}
	if note := h.peerNote(ctx, "mac", "curl https://prod-1.example.com/health"); note != "" {
		t.Fatalf("not a remote login: %q", note)
	}
	if note := h.peerNote(ctx, "mac", "ssh git@github.com"); note != "" {
		t.Fatalf("not a peer: %q", note)
	}
	if note := h.peerNote(ctx, "srv", "ssh prod-1 uptime"); note != "" {
		t.Fatalf("the machine itself: %q", note)
	}
}
