package node_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/nodehub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/store"
	"github.com/XiaoXianThis/Sous/apps/core/internal/task"
)

// A direct chat (decision 0028): the user's words go to the task's agent in
// its own session, and the run reports whether it changed the work tree.
func TestChatRunResumesTheSession(t *testing.T) {
	te := newTaskEnv(t)
	prompts := filepath.Join(t.TempDir(), "prompts")
	_, stop := te.startDaemonWith("edit", "FAKE_ACP_LOAD=1", "FAKE_ACP_PROMPTS="+prompts)
	defer stop()
	ctx := context.Background()

	tk, err := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "改 README", Dispatch: true}))
	if err != nil {
		t.Fatal(err)
	}
	te.waitStatus(tk.ID, event.TaskDone)
	work := te.runs(tk.ID)[0]
	if work.Session == "" {
		t.Fatalf("the work run left no session: %+v", work)
	}
	// The fake agent always writes the same hello.txt: clear it so this chat
	// has something to change.
	if err := os.Remove(filepath.Join(work.Workdir, "hello.txt")); err != nil {
		t.Fatal(err)
	}

	chat, err := te.hub.Create(ctx, nodehub.CreateRun{
		ThreadID: te.main, TaskID: tk.ID, NodeID: work.NodeID, Agent: "fake",
		RepoPath: te.repo, Prompt: "改一下 README 的标题", ContinuePrompt: "改一下 README 的标题",
		Purpose: event.PurposeChat, PermissionMode: work.Mode,
		Branch: work.Branch, BaseBranch: "main", BaseFrom: event.BaseLocal,
		ResumeSession: work.Session,
	})
	if err != nil {
		t.Fatal(err)
	}
	te.waitFor("the chat run to end", func() bool {
		r, err := te.log.Store.Run(ctx, chat.ID)
		return err == nil && (r.Status == event.RunStatusSucceeded || r.Status == event.RunStatusFailed)
	})
	done, _ := te.log.Store.Run(ctx, chat.ID)
	if done.Status != event.RunStatusSucceeded || done.Outcome != proto.OutcomeChanged {
		t.Fatalf("chat run: status %s outcome %q error %q", done.Status, done.Outcome, done.Error)
	}
	if done.Session == "" {
		t.Fatal("the chat run left no session for the next message")
	}

	raw, err := os.ReadFile(prompts)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "\n---\n")
	last := ""
	for i := len(parts) - 1; i >= 0; i-- {
		if strings.TrimSpace(parts[i]) != "" {
			last = parts[i]
			break
		}
	}
	if !strings.Contains(last, "改一下 README 的标题") || strings.Contains(last, "## 结果") {
		t.Fatalf("the agent got the full task prompt instead of the chat line: %q", last)
	}
}

// Without the earlier session a direct chat has no context, so it fails
// instead of starting a fresh one.
func TestChatRunWithoutItsSessionFails(t *testing.T) {
	te := newTaskEnv(t)
	_, stop := te.startDaemonWith("edit", "FAKE_ACP_LOAD=1", "FAKE_ACP_LOAD_FAIL=1")
	defer stop()
	ctx := context.Background()

	tk, err := te.tasks.Create(ctx, te.code(task.Input{ThreadID: te.main, Title: "改 README", Dispatch: true}))
	if err != nil {
		t.Fatal(err)
	}
	te.waitStatus(tk.ID, event.TaskDone)
	work := te.runs(tk.ID)[0]

	chat, err := te.hub.Create(ctx, nodehub.CreateRun{
		ThreadID: te.main, TaskID: tk.ID, NodeID: work.NodeID, Agent: "fake",
		RepoPath: te.repo, Prompt: "还在吗", ContinuePrompt: "还在吗",
		Purpose: event.PurposeChat, PermissionMode: work.Mode,
		Branch: work.Branch, BaseBranch: "main", BaseFrom: event.BaseLocal,
		ResumeSession: work.Session,
	})
	if err != nil {
		t.Fatal(err)
	}
	var done store.Run
	te.waitFor("the chat run to fail", func() bool {
		done, err = te.log.Store.Run(ctx, chat.ID)
		return err == nil && done.Status == event.RunStatusFailed
	})
	if !strings.Contains(done.Error, "接不上之前的会话") {
		t.Fatalf("error: %q", done.Error)
	}
}
