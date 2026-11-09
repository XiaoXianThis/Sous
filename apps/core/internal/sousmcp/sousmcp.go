// Package sousmcp is `sous mcp`: a stdio MCP server the node gives every
// work run's agent (decision 0025), so a sub-agent can report progress,
// ask the supervisor a question and hand in its result. It does nothing on
// its own: each tool is one call to the daemon's local socket, for the run
// named by --run, proved by the run's --token.
package sousmcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/ipc"
	"github.com/XiaoXianThis/Sous/apps/core/internal/version"
)

// Daemon methods (docs/api/core-daemon-ipc.md).
const (
	MethodProgress = "run.progress"
	MethodAsk      = "run.ask"
	MethodResult   = "run.result"
)

// Call is the run's identity plus a tool's arguments, as sent to the daemon.
type Call struct {
	RunID string `json:"run_id"`
	Token string `json:"token"`
	Text  string `json:"text,omitempty"`
	// Optional declaration: completed for progress/results, failed for results.
	// Empty keeps older agents compatible; it never implies task acceptance.
	Status string `json:"status,omitempty"`
	// run.result
	Done     string `json:"done,omitempty"`
	Changed  string `json:"changed,omitempty"`
	Verified string `json:"verified,omitempty"`
	Left     string `json:"left,omitempty"`
	Decide   string `json:"decide,omitempty"`
}

// Answer is the daemon's reply to a tool call.
type Answer struct {
	Text string `json:"text"`
}

// Caller sends one call to the daemon.
type Caller func(ctx context.Context, method string, c Call) (Answer, error)

// Main runs `sous mcp --home <dir> --run <id> --token <t>` on stdin/stdout.
func Main(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	home := fs.String("home", "", "the sous home of the daemon")
	run := fs.String("run", "", "the run this agent works on")
	token := fs.String("token", "", "the run's token")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *home == "" || *run == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "usage: sous mcp --home <dir> --run <id> --token <token>")
		return 2
	}
	path := ipc.Path(*home)
	call := func(ctx context.Context, method string, c Call) (Answer, error) {
		cl, err := ipc.Dial(path)
		if err != nil {
			return Answer{}, err
		}
		defer cl.Close()
		c.RunID, c.Token = *run, *token
		var a Answer
		err = cl.Call(ctx, method, c, &a)
		return a, err
	}
	if err := Serve(context.Background(), os.Stdin, os.Stdout, call); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, "sous mcp:", err)
		return 1
	}
	return 0
}

type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// AskTimeout bounds how long ask_supervisor waits for the daemon.
var AskTimeout = 30 * time.Minute

// Serve speaks MCP (newline-delimited JSON-RPC) until in ends.
func Serve(ctx context.Context, in io.Reader, out io.Writer, call Caller) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			enc.Encode(message{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		if m.ID == nil {
			continue // notifications/initialized and others
		}
		res, rerr := handle(ctx, m, call)
		reply := message{JSONRPC: "2.0", ID: m.ID}
		if rerr != nil {
			reply.Error = rerr
		} else {
			reply.Result = res
		}
		if err := enc.Encode(reply); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

func handle(ctx context.Context, m message, call Caller) (any, *rpcError) {
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(m.Params, &p)
		pv := p.ProtocolVersion
		if pv == "" {
			pv = "2025-06-18"
		}
		return map[string]any{
			"protocolVersion": pv,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "sous", "version": version.Version},
			"instructions": "sous 是派给你这个任务的系统。用 report_progress 报告阶段性进展；遇到只有用户或主管能决定的事，用 ask_supervisor 问一句再继续；" +
				"做完时用 report_result 交结果。",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
		text, err := callTool(ctx, p.Name, p.Arguments, call)
		if err != nil {
			return toolText(err.Error(), true), nil
		}
		return toolText(text, false), nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + m.Method}
}

func toolText(text string, isErr bool) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}, "isError": isErr}
}

func callTool(ctx context.Context, name string, raw json.RawMessage, call Caller) (string, error) {
	var a struct {
		Text     string `json:"text"`
		Question string `json:"question"`
		Done     string `json:"done"`
		Changed  string `json:"changed"`
		Verified string `json:"verified"`
		Left     string `json:"left"`
		Decide   string `json:"decide"`
		Status   string `json:"status"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("参数不对：%v", err)
		}
	}
	var (
		ans Answer
		err error
	)
	switch name {
	case "report_progress":
		if a.Status != "" && a.Status != "completed" {
			return "", errors.New("status 只能是 completed")
		}
		if strings.TrimSpace(a.Text) == "" {
			return "", errors.New("text 不能为空")
		}
		ans, err = call(ctx, MethodProgress, Call{Text: a.Text, Status: a.Status})
	case "ask_supervisor":
		if strings.TrimSpace(a.Question) == "" {
			return "", errors.New("question 不能为空")
		}
		actx, cancel := context.WithTimeout(ctx, AskTimeout+time.Minute)
		defer cancel()
		ans, err = call(actx, MethodAsk, Call{Text: a.Question})
	case "report_result":
		if a.Status != "" && a.Status != "completed" && a.Status != "failed" {
			return "", errors.New("status 只能是 completed 或 failed")
		}
		ans, err = call(ctx, MethodResult, Call{Done: a.Done, Changed: a.Changed, Verified: a.Verified, Left: a.Left, Decide: a.Decide, Status: a.Status})
	default:
		return "", fmt.Errorf("没有工具 %s", name)
	}
	if err != nil {
		return "", err
	}
	return ans.Text, nil
}

var tools = []map[string]any{
	{
		"name":        "report_progress",
		"description": "用一句话报告任务做到了哪一步（比如“依赖装好了，正在跑测试”）。会显示在任务卡上，主管也能看到；不用等回答。只在阶段变化时报告，不要每一步都报。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"text":   map[string]any{"type": "string", "description": "现在做到哪了"},
			"status": map[string]any{"type": "string", "enum": []string{"completed"}, "description": "仅当主体工作已完成时填写 completed；不表示任务已验收"}}, "required": []string{"text"}},
	},
	{
		"name": "ask_supervisor",
		"description": "遇到只有用户或主管能决定的事（需求有两种理解、要不要动某个不在范围里的东西、缺少账号或配置）时，问一句。" +
			"会等主管或用户回答，回答就是这个工具的结果；等不到时按你最合理的判断继续，并在结果里写明假设。自己能查清楚、能判断的不要问。一个任务最多问 3 次。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"question": map[string]any{"type": "string", "description": "一个具体的问题，带上你倾向的选项和理由"}}, "required": []string{"question"}},
	},
	{
		"name":        "report_result",
		"description": "做完时交结果（也可以在最后的总结里写“## 结果”一节，二选一）。每项一两句，没有就写“无”。",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
			"status":   map[string]any{"type": "string", "enum": []string{"completed", "failed"}, "description": "执行结果；明确失败必须填 failed，completed 不表示任务已验收"},
			"done":     map[string]any{"type": "string", "description": "做了什么"},
			"changed":  map[string]any{"type": "string", "description": "改了哪些文件或执行了哪些命令"},
			"verified": map[string]any{"type": "string", "description": "怎么验证的、结果如何；没验证就写“没有验证”"},
			"left":     map[string]any{"type": "string", "description": "没做完的和风险"},
			"decide":   map[string]any{"type": "string", "description": "需要用户或主管拿主意的事"},
		}, "required": []string{"done", "verified"}},
	},
}
