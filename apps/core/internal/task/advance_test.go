package task_test

import (
	"context"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

func TestReviewRunFallback(t *testing.T) {
	for _, tc := range []struct {
		name, status, summary string
		model, work           bool
	}{
		{"failed-model", event.RunStatusFailed, "崩溃", true, true},
		{"failed-no-model", event.RunStatusFailed, "崩溃", false, true},
		{"invalid-model", event.RunStatusSucceeded, "没有结论", true, true},
		{"missing-work", event.RunStatusFailed, "崩溃", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, log := engine(t)
			ctx := context.Background()
			m := &canned{answer: ` {"pass":true,"reason":"模型验收通过"}`}
			if tc.model {
				e.Reviewer = task.Model{Provider: m, Name: "r"}
			}
			tk := event.New(event.TaskCreated, event.ActorUser, "", event.TaskCreatedPayload{Kind: "general", Title: "验收兜底", Status: event.TaskRunning, Place: event.PlaceScratch, Review: event.ReviewAgent})
			tk.TaskID = "fallback-task"
			appendEvent := func(ev event.Event) {
				t.Helper()
				if _, err := log.Append(ctx, ev); err != nil {
					t.Fatal(err)
				}
			}
			appendEvent(tk)
			if tc.work {
				q := event.New(event.RunQueued, event.ActorSystem, "", event.RunQueuedPayload{NodeID: "n", Agent: "a", Kind: "general", Purpose: event.PurposeWork, Prompt: "工作输入"})
				q.TaskID, q.RunID = tk.TaskID, "work"
				appendEvent(q)
				end := event.New(event.RunEnded, event.ActorSystem, "", event.RunEndedPayload{Status: event.RunStatusSucceeded, Summary: "工作完成"})
				end.TaskID, end.RunID = tk.TaskID, "work"
				appendEvent(end)
			}
			q := event.New(event.RunQueued, event.ActorSystem, "", event.RunQueuedPayload{NodeID: "n", Agent: "a", Kind: "general", Purpose: event.PurposeReview})
			q.TaskID, q.RunID = tk.TaskID, "review"
			appendEvent(q)
			end := event.New(event.RunEnded, event.ActorSystem, "", event.RunEndedPayload{Status: tc.status, Summary: tc.summary})
			end.TaskID, end.RunID = tk.TaskID, "review"
			appendEvent(end)
			if err := e.Advance(ctx, store.Effect{Payload: []byte("review")}); err != nil {
				t.Fatal(err)
			}
			got, err := log.Store.Task(ctx, tk.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.model && tc.work {
				if got.Status != event.TaskDone || m.calls() != 1 {
					t.Fatalf("task %+v calls %d", got, m.calls())
				}
				if !strings.Contains(m.inputs[0], "工作完成") || !strings.Contains(m.inputs[0], "工作输入") {
					t.Fatal("model did not receive work run")
				}
			} else if got.Status != event.TaskReview || !strings.Contains(got.StatusReason, "等你验收") || m.calls() != 0 {
				t.Fatalf("task %+v calls %d", got, m.calls())
			}
		})
	}
}
