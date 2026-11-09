package node

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sousmcp"
)

// Decision 0025: every work run's agent gets sous's own MCP server
// (`sous mcp`), which calls back into this daemon over the local socket.

// MaxAsks bounds the questions one run may ask the supervisor.
const MaxAsks = 3

// AskWait is how long ask_supervisor waits for an answer.
var AskWait = 20 * time.Minute

// sousState is a run's side of the sous MCP server.
type sousState struct {
	builtin   bool
	token     string
	asks      int
	ask       chan string // the question waiting for an answer, if any
	result    *sousmcp.Call
	completed bool // explicit progress declaration; see the compatibility rule below
}

// sousServers is the MCP server list for a run's agent sessions.
func (d *Daemon) sousServers(r *run) []acp.MCPServer {
	if r.spec.Purpose != proto.PurposeWork {
		return nil
	}
	cmd := d.conf().SousMCP
	if len(cmd) == 1 && cmd[0] == "off" {
		return nil
	}
	if len(cmd) == 0 {
		exe, err := os.Executable()
		if err != nil {
			return nil
		}
		cmd = []string{exe, "mcp"}
	}
	r.mu.Lock()
	r.sous.builtin = len(d.conf().SousMCP) == 0
	r.mu.Unlock()
	args := append(append([]string(nil), cmd[1:]...), "--home", d.root, "--run", r.spec.RunID, "--token", r.sousToken())
	return []acp.MCPServer{{Name: "sous", Command: cmd[0], Args: args, Env: []acp.EnvVar{}}}
}

func (r *run) sousToken() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sous.token == "" {
		b := make([]byte, 16)
		rand.Read(b)
		r.sous.token = hex.EncodeToString(b)
	}
	return r.sous.token
}

var errNoRun = errors.New("这个任务的执行已经结束了")

// sousCall answers `sous mcp` on the daemon's socket.
func (d *Daemon) sousCall(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var c sousmcp.Call
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	r := d.run(c.RunID)
	if r == nil {
		return nil, errNoRun
	}
	r.mu.Lock()
	ok := r.sous.token != "" && subtle.ConstantTimeCompare([]byte(r.sous.token), []byte(c.Token)) == 1
	r.mu.Unlock()
	if !ok {
		return nil, errNoRun
	}
	switch method {
	case sousmcp.MethodProgress:
		if c.Status != "" && c.Status != "completed" {
			return nil, errors.New("invalid progress status")
		}
		r.mu.Lock()
		// New callers declare status=completed. Older callers may use the
		// exact leading declaration "主体工作已完成"/"任务已完成" followed by
		// punctuation/end. Do not infer completion from arbitrary words such
		// as "测试通过", "服务启动" or a completed intermediate step.
		r.sous.completed = c.Status == "completed" || completedProgressRE.MatchString(strings.TrimSpace(c.Text))
		r.mu.Unlock()
		r.save()
		r.status("进度：" + truncate(strings.TrimSpace(c.Text), 300))
		return sousmcp.Answer{Text: "已报告"}, nil
	case sousmcp.MethodAsk:
		return r.askSupervisor(ctx, strings.TrimSpace(c.Text))
	case sousmcp.MethodResult:
		if c.Status != "" && c.Status != "completed" && c.Status != "failed" {
			return nil, errors.New("invalid result status")
		}
		r.mu.Lock()
		r.sous.result = &c
		r.mu.Unlock()
		r.save()
		return sousmcp.Answer{Text: "已收到结果。用几句话收尾就可以结束了。"}, nil
	}
	return nil, fmt.Errorf("unknown method %s", method)
}

// askSupervisor sends a question to the Hub and waits for the answer: the
// next message to the run (run_message from the supervisor, or the user's
// reply on the task card) is it.
func (r *run) askSupervisor(ctx context.Context, q string) (any, error) {
	r.mu.Lock()
	if r.sous.asks >= MaxAsks {
		r.mu.Unlock()
		return sousmcp.Answer{Text: fmt.Sprintf("这个任务已经问过 %d 次了，不能再问：按你最合理的判断继续，并在结果里写明你的假设。", MaxAsks)}, nil
	}
	if r.sous.ask != nil {
		r.mu.Unlock()
		return nil, errors.New("上一个问题还在等回答")
	}
	r.sous.asks++
	n := r.sous.asks
	ch := make(chan string, 1)
	r.sous.ask = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		if r.sous.ask == ch {
			r.sous.ask = nil
		}
		r.mu.Unlock()
	}()
	r.status("在问主管：" + truncate(q, 300))
	r.d.ob.reliable(proto.New(proto.TRunAsk, proto.RunAsk{RunID: r.spec.RunID, Seq: n, Question: truncate(q, 2000)}))
	select {
	case a := <-ch:
		return sousmcp.Answer{Text: "回答：" + a}, nil
	case <-time.After(AskWait):
		r.status("没有等到回答，agent 按自己的判断继续")
		return sousmcp.Answer{Text: "没有等到回答。按你最合理的判断继续，并在结果里写明你的假设。"}, nil
	case <-r.ctx.Done():
		return nil, errNoRun
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// answerAsk hands a message to the question waiting for one; false when
// none waits.
func (r *run) answerAsk(text string) bool {
	r.mu.Lock()
	ch := r.sous.ask
	r.sous.ask = nil
	r.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- text
	r.status("收到回答，已交给 agent")
	return true
}

// resultSection renders a result handed in with report_result, for a
// summary that has no "## 结果" section of its own.
func resultSection(c *sousmcp.Call) string {
	var b strings.Builder
	b.WriteString("## 结果\n")
	line := func(label, v string) {
		if v = strings.TrimSpace(v); v == "" {
			v = "无"
		}
		fmt.Fprintf(&b, "- %s：%s\n", label, strings.ReplaceAll(v, "\n", " "))
	}
	line("做了", c.Done)
	line("改动", c.Changed)
	line("验证", c.Verified)
	line("没做完 / 风险", c.Left)
	line("需要决定", c.Decide)
	return b.String()
}

func (r *run) isBuiltinSousRequest(req acp.PermissionRequest) bool {
	r.mu.Lock()
	bound := r.sous.builtin && r.sous.token != ""
	r.mu.Unlock()
	if r.spec.Agent != "claude" {
		return false
	}
	if !bound || r.spec.Purpose != proto.PurposeWork || len(req.ToolCall.Locations) > 0 || !oneString(req.ToolCall.Kind, "", "other", "think") {
		return false
	}
	if !oneString(req.ToolCall.Title, "mcp__sous__report_progress", "mcp__sous__report_result", "mcp__sous__ask_supervisor") {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(req.ToolCall.RawInput, &m) != nil {
		return false
	}
	allowed := map[string]bool{}
	switch req.ToolCall.Title {
	case "mcp__sous__report_progress":
		allowed["text"] = true
		allowed["status"] = true
	case "mcp__sous__ask_supervisor":
		allowed["question"] = true
	case "mcp__sous__report_result":
		for _, k := range []string{"done", "changed", "verified", "left", "decide", "status"} {
			allowed[k] = true
		}
	}
	for key, v := range m {
		if !allowed[key] {
			return false
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			return false
		}
	}

	return len(m) > 0
}
