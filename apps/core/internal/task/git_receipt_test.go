package task

import (
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
)

func TestGitToolReceiptDistinguishesFailureAndNoop(t *testing.T) {
	for _, tc := range []struct {
		status, message string
		failed          bool
	}{
		{event.RunStatusSucceeded, "已把 feature 推送到 origin（1 个新提交）", false},
		{event.RunStatusSucceeded, "feature 已经和 origin/feature 一样，不用推送", false},
		{event.RunStatusSucceeded, "没有新的改动要提交（feature）", false},
		{event.RunStatusFailed, "rejected: non-fast-forward", true},
		{event.RunStatusCancelled, "没有获得许可", true},
		{event.RunStatusInterrupted, "连接中断", true},
	} {
		t.Run(tc.status+"/"+tc.message, func(t *testing.T) {
			r := store.Run{Status: tc.status, Error: tc.message, Git: &proto.GitOp{Op: proto.GitPush}, GitResult: &proto.GitResult{Op: proto.GitPush, Message: tc.message}}
			text, err := gitToolResult(r)
			if (err != nil) != tc.failed {
				t.Fatalf("text=%s err=%v", text, err)
			}
			if !tc.failed && text != tc.message {
				t.Fatalf("no-op receipt changed: %s", text)
			}
			if tc.failed && (err.Error() != GitReport(r) || text != "") {
				t.Fatalf("failure lost: %s %v", text, err)
			}
		})
	}
}
