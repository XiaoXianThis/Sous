package supervisor

import (
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestMentionsReachTheModel(t *testing.T) {
	msgs := []store.Message{{ID: "1", Role: "user", Seq: 1, Content: "@blog 要发版",
		Context: &event.MessageContext{Mentions: []event.Mention{{ProjectID: "p2", Name: "blog"}, {ProjectID: "p9", Name: "gone"}}}}}
	label := func(c event.MessageContext) string {
		if c.ProjectID == "p2" {
			return "项目 blog（id p2）"
		}
		return ""
	}
	got := toProvider(msgs, label)[0].Parts[0].Text
	if !strings.Contains(got, "@blog = 项目 blog（id p2）") || !strings.Contains(got, "@gone = 项目 gone（id p9）") || !strings.HasSuffix(got, "\n@blog 要发版") {
		t.Fatalf("text: %q", got)
	}
	if strings.Contains(got, "正在工作台上看") {
		t.Fatalf("mentions alone are not what the user is looking at: %q", got)
	}

	// Without a label the ids still reach the model.
	if bare := toProvider(msgs)[0].Parts[0].Text; !strings.Contains(bare, "（id p2）") {
		t.Fatalf("bare: %q", bare)
	}
}
