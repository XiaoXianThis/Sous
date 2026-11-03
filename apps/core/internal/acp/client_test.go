package acp_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/acp/acptest"
)

func TestMain(m *testing.M) {
	acptest.RunIfFake()
	os.Exit(m.Run())
}

type recorder struct {
	mu      sync.Mutex
	updates []acp.Update
	asked   []acp.PermissionRequest
	answer  string
}

func (r *recorder) callbacks() acp.Callbacks {
	return acp.Callbacks{
		OnUpdate: func(_ string, u acp.Update) {
			r.mu.Lock()
			r.updates = append(r.updates, u)
			r.mu.Unlock()
		},
		OnPermission: func(_ context.Context, req acp.PermissionRequest) acp.Decision {
			r.mu.Lock()
			r.asked = append(r.asked, req)
			r.mu.Unlock()
			return acp.Decision{OptionID: r.answer}
		},
	}
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, u := range r.updates {
		if u.Kind == "agent_message_chunk" {
			b.WriteString(u.Text)
		}
	}
	return b.String()
}

func run(t *testing.T, mode, answer string) (*recorder, string, string) {
	t.Helper()
	dir := t.TempDir()
	rec := &recorder{answer: answer}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := acp.Start(ctx, acptest.Command(mode, dir), rec.callbacks())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := c.NewSession(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetMode(ctx, s.ID, "default"); err != nil {
		t.Fatal(err)
	}
	stop, err := c.Prompt(ctx, s.ID, "创建 hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	return rec, stop, dir
}

func TestPromptWithAllowedPermission(t *testing.T) {
	rec, stop, dir := run(t, "write", "allow")
	if stop != "end_turn" {
		t.Fatalf("stop reason %q", stop)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "hello.txt")); err != nil || string(b) != "hi\n" {
		t.Fatalf("file: %v %q", err, b)
	}
	if len(rec.asked) != 1 || rec.asked[0].ToolCall.Title != "Write hello.txt" || len(rec.asked[0].Options) != 2 {
		t.Fatalf("permission: %+v", rec.asked)
	}
	if !strings.Contains(rec.text(), "已经创建") {
		t.Fatalf("text: %q", rec.text())
	}
	var sawPlan, sawTool bool
	for _, u := range rec.updates {
		sawPlan = sawPlan || (u.Kind == "plan" && len(u.Plan) == 1)
		sawTool = sawTool || (u.Kind == "tool_call_update" && u.Status == "completed" && u.ToolCallID == "t1")
	}
	if !sawPlan || !sawTool {
		t.Fatalf("plan=%v tool=%v", sawPlan, sawTool)
	}
}

func TestPromptWithRejectedOrCancelledPermission(t *testing.T) {
	for _, answer := range []string{"reject", ""} {
		rec, _, dir := run(t, "write", answer)
		if _, err := os.Stat(filepath.Join(dir, "hello.txt")); err == nil {
			t.Fatalf("answer %q: file written without permission", answer)
		}
		if !strings.Contains(rec.text(), "没有获得写入权限") {
			t.Fatalf("answer %q: text %q", answer, rec.text())
		}
	}
}

func TestAuthenticateThenRetry(t *testing.T) {
	if _, stop, _ := run(t, "auth", "allow"); stop != "end_turn" {
		t.Fatalf("stop %q", stop)
	}
}

func TestNotLoggedIn(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	c, err := acp.Start(ctx, acptest.Command("nologin", dir), acp.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.NewSession(ctx, dir); !errors.Is(err, acp.ErrNotLoggedIn) || !strings.Contains(err.Error(), "run fake login") {
		t.Fatalf("err = %v", err)
	}
}

func TestCancel(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	rec := &recorder{}
	c, err := acp.Start(ctx, acptest.Command("slow", dir), rec.callbacks())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, _ := c.NewSession(ctx, dir)
	go func() {
		time.Sleep(200 * time.Millisecond)
		c.Cancel(s.ID)
	}()
	stop, err := c.Prompt(ctx, s.ID, "慢慢来")
	if err != nil || stop != "cancelled" {
		t.Fatalf("stop %q err %v", stop, err)
	}
}

func TestAgentCrashEndsPrompt(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := acp.Start(ctx, acptest.Command("crash", dir), acp.Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, _ := c.NewSession(ctx, dir)
	if _, err := c.Prompt(ctx, s.ID, "x"); err == nil || ctx.Err() != nil {
		t.Fatalf("expected a connection error, got %v (ctx %v)", err, ctx.Err())
	}
	select {
	case <-c.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit")
	}
}

func TestStartFailsForMissingBinary(t *testing.T) {
	if _, err := acp.Start(context.Background(), acp.Command{Path: "/nonexistent/agent"}, acp.Callbacks{}); err == nil {
		t.Fatal("expected error")
	}
}
