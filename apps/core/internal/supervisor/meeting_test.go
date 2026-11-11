package supervisor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/eventlog"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestCheckMinutes(t *testing.T) {
	good := "## 议题\n- 议题 [#1]\n## 结论和决定\n- 决定 [#1]\n## 被否掉的方案和理由\n- 无\n## 未决问题\n- 无\n## 行动项\n- 行动 [#1]"
	for _, tc := range []struct {
		name, text string
		valid      bool
	}{{"good", good, true}, {"none", strings.ReplaceAll(good, "- 决定 [#1]", "- 无"), true}, {"missing", strings.Replace(good, " [#1]", "", 2), false}, {"unknown", strings.ReplaceAll(good, "[#1]", "[#2]"), false}, {"section", strings.Replace(good, "## 议题", "## 别的", 1), false}, {"order", strings.Replace(good, "## 结论和决定", "## 行动项", 1), false}} {
		t.Run(tc.name, func(t *testing.T) {
			got := checkMinutes(tc.text, map[int64]bool{1: true})
			if (len(got) == 0) != tc.valid {
				t.Fatalf("issues: %v", got)
			}
		})
	}
}
func TestMinutesRetryAndPost(t *testing.T) {
	calls := 0
	model := &answering{fn: func(req provider.Request, n int) (provider.Response, error) {
		calls++
		ref := ""
		if n > 0 {
			ref = " [#2]"
		}
		return provider.Response{Message: provider.Text(provider.RoleAssistant, "## 议题\n- 无\n## 结论和决定\n- 决定"+ref+"\n## 被否掉的方案和理由\n- 无\n## 未决问题\n- 无\n## 行动项\n- 无")}, nil
	}}
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := eventlog.New(st, event.NewBus())
	parent, err := log.EnsureMainThread(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Log: log, Provider: model, Compactor: model})
	s.ctx = ctx
	id := "meeting"
	opening := "opening"
	_, err = log.Append(ctx, event.New(event.ThreadCreated, event.ActorUser, id, event.ThreadCreatedPayload{Title: "议题", Kind: event.KindMeeting, ParentThreadID: parent, MeetingTopic: opening}), event.New(event.MessageUser, event.ActorUser, id, event.MessagePayload{MessageID: opening, Content: "讨论"}))
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := log.Store.MessagesAfter(ctx, id, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatal(err)
	}
	// The opening event's sequence is assigned globally; use it in the second answer.
	seq := msgs[0].Seq
	model.fn = func(req provider.Request, n int) (provider.Response, error) {
		calls++
		ref := ""
		if n > 0 {
			ref = fmt.Sprintf(" [#%d]", seq)
		}
		return provider.Response{Message: provider.Text(provider.RoleAssistant, "## 议题\n- 无\n## 结论和决定\n- 决定"+ref+"\n## 被否掉的方案和理由\n- 无\n## 未决问题\n- 无\n## 行动项\n- 无")}, nil
	}
	if err := s.makeMinutes(ctx, id); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls: %d", calls)
	}
	rows, err := log.Store.Minutes(ctx, id)
	if err != nil || len(rows) != 1 || rows[0].Status != "draft" {
		t.Fatalf("rows: %+v %v", rows, err)
	}
	mid, err := s.postMinutes(ctx, id, "")
	if err != nil || mid == "" {
		t.Fatalf("post: %s %v", mid, err)
	}
	linked, err := log.Store.LinkedMeeting(ctx, parent, id)
	if err != nil || !linked {
		t.Fatalf("link: %v %v", linked, err)
	}
}
