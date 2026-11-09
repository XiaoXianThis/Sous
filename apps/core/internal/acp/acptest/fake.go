// Package acptest provides a fake ACP agent for tests. The fake runs inside
// the test binary itself: call RunIfFake from TestMain, then start agents
// with Command.
//
// Behaviors (FAKE_ACP_MODE):
//
//	write  (default) stream a message, ask permission to run a shell command
//	       that writes hello.txt, write it when allowed, end the turn
//	edit   like write, but ask to edit hello.txt (a worktree edit, which
//	       the node's policy allows by itself)
//	floor  like write, but ask to run rm -rf ~ (refused by the floor rules)
//	secret like write, but ask to run cat ~/.ssh/id_rsa (a hard signal: the
//	       user decides, never the approver agent)
//	auth   like write, but session/new fails until authenticate is called
//	nologin session/new always needs a login
//	slow   stream a message, then wait until session/cancel
//	crash  exit in the middle of a prompt
//	loop   call the same tool over and over until session/cancel; later
//	       prompts in the same session end the turn at once
//	reject like write, but a refused permission ends the turn as
//	       "cancelled" (as OpenCode does); a later prompt in the same
//	       session writes hello.txt another way and ends the turn
//	quota  say "Upgrade your plan to continue" and end the turn, without
//	       a tool call (an agent out of its plan's usage)
//	result-error / progress-error report result/completion, then ACP error
//	result-failed report explicit failure and end normally
//	result-refused report a completed deploy, then request an unrelated check
//	change-disconnect write a file, then drop the stream
//	noop   say a few words and end the turn, without a tool call
//	answer answer every prompt in words (a consultant or a reviewer): ask
//	       to read README.md first; when the prompt contains "改一下",
//	       also ask to edit it; when it asks for a verdict ("结论 JSON"),
//	       end with {"pass": …} (FAKE_ACP_VERDICT=fail fails). The answer
//	       says which prompt of the session it is, and whether the session
//	       was loaded.
//	ask    start the "sous" MCP server the session was given, report
//	       progress, ask_supervisor a question, write the answer to
//	       answer.txt, report_result and end the turn (decision 0025)
//
// FAKE_ACP_LOAD=1 advertises loadSession and accepts session/load; a prompt
// in a loaded session writes hello.txt and ends the turn at once.
// FAKE_ACP_PROMPTS=<file> appends every prompt the agent receives to file.
// FAKE_ACP_OPTIONS=1 reports configOptions (model: fast / smart in two
// groups, effort: low / medium / high) and accepts
// session/set_config_option, which is also logged to FAKE_ACP_PROMPTS as
// "set <id>=<value>". FAKE_ACP_OPTION_IDS="<model id>,<effort id>" renames
// the two options, as agents name them differently.
package acptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/sousmcp"
)

const envFake = "SOUS_FAKE_ACP_AGENT"

// Command returns a command that starts the fake agent in mode.
func Command(mode, dir string) acp.Command {
	exe, _ := os.Executable()
	return acp.Command{Path: exe, Args: []string{"-test.run=^$"}, Dir: dir, Env: []string{envFake + "=1", "FAKE_ACP_MODE=" + mode}}
}

// EnvSousMCP makes the test binary act as `sous mcp` when started with
// "mcp" (RunIfFake). A daemon inside a test starts its own executable with
// "mcp" by default, so setting this variable in the test is enough.
const EnvSousMCP = "SOUS_FAKE_SOUS_MCP"

// RunIfFake turns the process into the fake agent when started by Command.
func RunIfFake() {
	if os.Getenv(EnvSousMCP) == "1" {
		// Only the process started as "<test binary> mcp …": the fake
		// agent inherits the variable too.
		for i, a := range os.Args {
			if a == "mcp" {
				os.Exit(sousmcp.Main(os.Args[i+1:]))
			}
		}
	}
	if os.Getenv(envFake) != "1" {
		return
	}
	runFake(os.Getenv("FAKE_ACP_MODE"))
	os.Exit(0)
}

type msg struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *acp.Error       `json:"error,omitempty"`
}

func runFake(mode string) {
	if mode == "" {
		mode = "write"
	}
	var wmu sync.Mutex
	out := bufio.NewWriter(os.Stdout)
	send := func(m msg) {
		m.JSONRPC = "2.0"
		raw, _ := json.Marshal(m)
		wmu.Lock()
		out.Write(append(raw, '\n'))
		out.Flush()
		wmu.Unlock()
	}
	result := func(id *json.RawMessage, v any) {
		raw, _ := json.Marshal(v)
		send(msg{ID: id, Result: raw})
	}
	update := func(sid string, u map[string]any) {
		raw, _ := json.Marshal(map[string]any{"sessionId": sid, "update": u})
		send(msg{Method: "session/update", Params: raw})
	}
	chunk := func(sid, text string) {
		update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}})
	}

	var (
		mu        sync.Mutex
		authed    = mode != "auth"
		cwd       string
		nextReq   = 1000
		responses = map[string]chan msg{}
		cancelled = make(chan struct{}, 1)
		loaded    bool
		prompts   int
		servers   []acp.MCPServer // the session's MCP servers (mode ask)
		canLoad   = os.Getenv("FAKE_ACP_LOAD") == "1"
		// FAKE_ACP_OPTIONS=1: report a model and a reasoning level.
		withOptions = os.Getenv("FAKE_ACP_OPTIONS") == "1"
		optModel    = "fast"
		optEffort   = "medium"
	)
	request := func(method string, params any) msg {
		mu.Lock()
		nextReq++
		id := json.RawMessage(fmt.Sprint(nextReq))
		ch := make(chan msg, 1)
		responses[string(id)] = ch
		mu.Unlock()
		raw, _ := json.Marshal(params)
		send(msg{ID: &id, Method: method, Params: raw})
		return <-ch
	}

	prompt := func(id *json.RawMessage, sid string, text string) {
		mu.Lock()
		prompts++
		n, wasLoaded := prompts, loaded
		mu.Unlock()
		if f := os.Getenv("FAKE_ACP_PROMPTS"); f != "" {
			if fh, err := os.OpenFile(f, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
				fmt.Fprintf(fh, "%s\n---\n", text)
				fh.Close()
			}
		}
		if mode == "answer" {
			ask := func(tid, title, kind string, input map[string]string) bool {
				update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": tid, "title": title, "kind": kind, "status": "pending"})
				resp := request("session/request_permission", map[string]any{
					"sessionId": sid,
					"toolCall":  map[string]any{"toolCallId": tid, "title": title, "kind": kind, "rawInput": input, "locations": []map[string]string{{"path": filepath.Join(cwd, "README.md")}}},
					"options": []map[string]string{
						{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
						{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
					},
				})
				var outcome struct {
					Outcome struct{ Outcome, OptionID string } `json:"outcome"`
				}
				json.Unmarshal(resp.Result, &outcome)
				return outcome.Outcome.Outcome == "selected" && outcome.Outcome.OptionID == "allow"
			}
			read := ask(fmt.Sprintf("r%d", n), "Read README.md", "read", map[string]string{"path": "README.md"})
			var b strings.Builder
			fmt.Fprintf(&b, "第 %d 个问题", n)
			if wasLoaded {
				b.WriteString("（接着之前的会话）")
			}
			if read {
				b.WriteString("，读了 README.md")
			}
			if strings.Contains(text, "改一下") {
				if ask(fmt.Sprintf("e%d", n), "Edit README.md", "edit", map[string]string{"path": "README.md"}) {
					os.WriteFile(filepath.Join(cwd, "README.md"), []byte("changed\n"), 0o644)
					b.WriteString("，改了 README.md")
				} else {
					b.WriteString("，不能改文件")
				}
			}
			q := []rune(strings.TrimSpace(text))
			if len(q) > 40 {
				q = q[len(q)-40:]
			}
			fmt.Fprintf(&b, "：收到「%s」。", string(q))
			if strings.Contains(text, "结论 JSON") {
				pass := os.Getenv("FAKE_ACP_VERDICT") != "fail"
				fmt.Fprintf(&b, "\n{\"pass\": %v, \"reason\": \"%s\"}", pass, map[bool]string{true: "测试都通过了", false: "缺少测试"}[pass])
			}
			chunk(sid, b.String())
			result(id, map[string]string{"stopReason": "end_turn"})
			return
		}
		if mode == "result-error" || mode == "progress-error" || mode == "progress-text-error" || mode == "result-failed" || mode == "result-refused" || mode == "result-refused-cancel" || mode == "result-slow" {
			mu.Lock()
			list := servers
			mu.Unlock()
			_, err := withSous(list, func(tool func(string, map[string]string) (string, error)) (string, error) {
				if mode == "progress-text-error" {
					return tool("report_progress", map[string]string{"text": "主体工作已完成：文件已替换，服务已启动，验证通过"})
				}
				if mode == "progress-error" {
					return tool("report_progress", map[string]string{"text": "文件已替换，服务已启动，验证通过", "status": "completed"})
				}
				status := "completed"
				if mode == "result-failed" {
					status = "failed"
				}
				return tool("report_result", map[string]string{"done": "部署执行结果", "verified": "服务检查", "status": status})
			})
			if err != nil {
				send(msg{ID: id, Error: &acp.Error{Code: -32603, Message: err.Error()}})
				return
			}
			if mode == "result-slow" {
				<-cancelled
				result(id, map[string]string{"stopReason": "cancelled"})
				return
			}
			if mode == "result-error" || mode == "progress-error" || mode == "progress-text-error" {
				send(msg{ID: id, Error: &acp.Error{Code: -32603, Message: "stream disconnected before completion"}})
				return
			}
			if mode == "result-failed" {
				result(id, map[string]string{"stopReason": "end_turn"})
				return
			}
		}
		if mode == "change-disconnect" {
			os.WriteFile(filepath.Join(cwd, "hello.txt"), []byte("completed before disconnect\n"), 0o644)
			os.Exit(3)
		}
		if mode == "ask" {
			mu.Lock()
			list := servers
			mu.Unlock()
			answer, err := askThroughSous(list)
			if err != nil {
				chunk(sid, "没能用 sous 的工具："+err.Error())
				result(id, map[string]string{"stopReason": "end_turn"})
				return
			}
			os.WriteFile(filepath.Join(cwd, "answer.txt"), []byte(answer+"\n"), 0o644)
			chunk(sid, "按主管的回答写了 answer.txt："+answer)
			result(id, map[string]string{"stopReason": "end_turn"})
			return
		}
		if wasLoaded {
			os.WriteFile(filepath.Join(cwd, "hello.txt"), []byte("hi\n"), 0o644)
			chunk(sid, "从中断的地方继续，已经创建 hello.txt。")
			result(id, map[string]string{"stopReason": "end_turn"})
			return
		}
		if mode == "quota" {
			chunk(sid, "Upgrade your plan to continue")
			result(id, map[string]string{"stopReason": "end_turn"})
			return
		}
		if mode == "noop" {
			chunk(sid, "好的，我看了一下。")
			result(id, map[string]string{"stopReason": "end_turn"})
			return
		}
		if mode == "loop" {
			if n > 1 {
				chunk(sid, "好的，换个办法，已经完成。")
				result(id, map[string]string{"stopReason": "end_turn"})
				return
			}
			chunk(sid, "我先看一下 go.mod。")
			for i := 1; ; i++ {
				tid := fmt.Sprintf("loop-%d", i)
				update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": tid, "title": "Read go.mod", "kind": "read", "status": "in_progress"})
				update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": tid, "status": "completed"})
				select {
				case <-cancelled:
					result(id, map[string]string{"stopReason": "cancelled"})
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
		}
		if mode == "reject" && n > 1 {
			os.WriteFile(filepath.Join(cwd, "hello.txt"), []byte("hi\n"), 0o644)
			chunk(sid, "好的，换个办法，直接写好了 hello.txt。")
			result(id, map[string]string{"stopReason": "end_turn"})
			return
		}
		chunk(sid, "我先看一下仓库。")
		switch mode {
		case "crash":
			os.Exit(3)
		case "slow":
			select {
			case <-cancelled:
				result(id, map[string]string{"stopReason": "cancelled"})
			case <-time.After(30 * time.Second):
				result(id, map[string]string{"stopReason": "end_turn"})
			}
			return
		}
		update(sid, map[string]any{"sessionUpdate": "plan", "entries": []map[string]string{{"content": "创建 hello.txt", "priority": "high", "status": "in_progress"}}})
		kind, input := "execute", map[string]string{"command": "./scripts/make-hello.sh hello.txt"}
		switch mode {
		case "result-refused", "result-refused-cancel":
			input = map[string]string{"command": "wok check"}
		case "approval-guard":
			input = map[string]string{"command": "env -u SOUS_SIGN_CERT_P12_PASSWORD sh scripts/make-hello.sh hello.txt"}
		case "edit":
			kind, input = "edit", map[string]string{"path": "hello.txt"}
		case "floor":
			input = map[string]string{"command": "rm -rf ~"}
		case "secret":
			input = map[string]string{"command": "cat ~/.ssh/id_rsa"}
		}
		update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "Write hello.txt", "kind": kind, "status": "pending"})
		resp := request("session/request_permission", map[string]any{
			"sessionId": sid,
			"toolCall":  map[string]any{"toolCallId": "t1", "title": "Write hello.txt", "kind": kind, "rawInput": input},
			"options": []map[string]string{
				{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
				{"optionId": "reject", "name": "Reject", "kind": "reject_once"},
			},
		})
		var outcome struct {
			Outcome struct{ Outcome, OptionID string } `json:"outcome"`
		}
		json.Unmarshal(resp.Result, &outcome)
		if outcome.Outcome.Outcome == "selected" && outcome.Outcome.OptionID == "allow" {
			os.WriteFile(filepath.Join(cwd, "hello.txt"), []byte("hi\n"), 0o644)
			update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "completed"})
			chunk(sid, "已经创建 hello.txt。")
		} else {
			update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "t1", "status": "failed"})
			if mode == "reject" || mode == "reject-stop" || mode == "result-refused-cancel" {
				result(id, map[string]string{"stopReason": "cancelled"})
				return
			}
			chunk(sid, "没有获得写入权限，已停止。")
		}
		result(id, map[string]string{"stopReason": "end_turn"})
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var m msg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Method == "" && m.ID != nil {
			mu.Lock()
			ch := responses[strings.TrimSpace(string(*m.ID))]
			mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		switch m.Method {
		case "initialize":
			// Like Cursor, insist on a complete clientInfo.
			var ip struct {
				ClientInfo *struct {
					Name    string  `json:"name"`
					Version *string `json:"version"`
				} `json:"clientInfo"`
			}
			json.Unmarshal(m.Params, &ip)
			if ip.ClientInfo == nil || ip.ClientInfo.Version == nil {
				send(msg{ID: m.ID, Error: &acp.Error{Code: -32603, Message: "Internal error: clientInfo.version is required"}})
				continue
			}
			res := map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": canLoad},
				"agentInfo": map[string]string{"name": "fake", "version": "1.0"}}
			if mode == "auth" || mode == "nologin" {
				res["authMethods"] = []map[string]string{{"id": "fake-login", "name": "Login", "description": "run fake login"}}
			}
			result(m.ID, res)
		case "authenticate":
			if mode == "nologin" {
				send(msg{ID: m.ID, Error: &acp.Error{Code: acp.CodeAuthRequired, Message: "Authentication required"}})
				continue
			}
			mu.Lock()
			authed = true
			mu.Unlock()
			result(m.ID, map[string]any{})
		case "session/new":
			mu.Lock()
			ok := authed && mode != "nologin"
			mu.Unlock()
			if !ok {
				send(msg{ID: m.ID, Error: &acp.Error{Code: acp.CodeAuthRequired, Message: "Authentication required"}})
				continue
			}
			var p struct {
				Cwd        string
				McpServers []acp.MCPServer `json:"mcpServers"`
			}
			json.Unmarshal(m.Params, &p)
			cwd = p.Cwd
			mu.Lock()
			servers = p.McpServers
			mu.Unlock()
			res := map[string]any{"sessionId": "fake-session", "modes": map[string]any{
				"currentModeId": "auto", "availableModes": []map[string]string{{"id": "default", "name": "Ask"}, {"id": "auto", "name": "Auto"}}}}
			if withOptions {
				mu.Lock()
				res["configOptions"] = configOptions(optModel, optEffort)
				mu.Unlock()
			}
			result(m.ID, res)
		case "session/set_config_option":
			var p struct{ ConfigID, Value string }
			json.Unmarshal(m.Params, &p)
			mu.Lock()
			switch p.ConfigID {
			case optionIDs()[0]:
				optModel = p.Value
			case optionIDs()[1]:
				optEffort = p.Value
			}
			opts := configOptions(optModel, optEffort)
			mu.Unlock()
			if f := os.Getenv("FAKE_ACP_PROMPTS"); f != "" {
				if fh, err := os.OpenFile(f, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
					fmt.Fprintf(fh, "set %s=%s\n---\n", p.ConfigID, p.Value)
					fh.Close()
				}
			}
			result(m.ID, map[string]any{"configOptions": opts})
		case "session/load":
			if !canLoad {
				send(msg{ID: m.ID, Error: &acp.Error{Code: acp.CodeMethodNotFound, Message: "session/load not supported"}})
				continue
			}
			var p struct{ Cwd string }
			json.Unmarshal(m.Params, &p)
			mu.Lock()
			cwd, loaded = p.Cwd, true
			mu.Unlock()
			// Replay the history, as real agents do.
			raw, _ := json.Marshal(map[string]any{"sessionId": "fake-session", "update": map[string]any{
				"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": "（回放的旧消息）"}}})
			send(msg{Method: "session/update", Params: raw})
			result(m.ID, map[string]any{"modes": map[string]any{
				"currentModeId": "default", "availableModes": []map[string]string{{"id": "default", "name": "Ask"}, {"id": "auto", "name": "Auto"}}}})
		case "session/set_mode":
			result(m.ID, map[string]any{})
		case "session/prompt":
			var p struct {
				SessionID string `json:"sessionId"`
				Prompt    []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			}
			json.Unmarshal(m.Params, &p)
			text := ""
			for _, b := range p.Prompt {
				text += b.Text
			}
			go prompt(m.ID, p.SessionID, text)
		case "session/cancel":
			select {
			case cancelled <- struct{}{}:
			default:
			}
		default:
			if m.ID != nil {
				send(msg{ID: m.ID, Error: &acp.Error{Code: acp.CodeMethodNotFound, Message: "unknown " + m.Method}})
			}
		}
	}
}

// optionIDs are the ids of the model and effort options.
func optionIDs() [2]string {
	if v := os.Getenv("FAKE_ACP_OPTION_IDS"); v != "" {
		if a, b, ok := strings.Cut(v, ","); ok {
			return [2]string{a, b}
		}
	}
	return [2]string{"model", "effort"}
}

func configOptions(model, effort string) []map[string]any {
	ids := optionIDs()
	return []map[string]any{
		{"id": ids[0], "name": "Model", "category": "model", "type": "select", "currentValue": model, "options": []map[string]any{
			{"group": "quick", "name": "Quick", "options": []map[string]string{{"value": "fast", "name": "Fast"}}},
			{"group": "deep", "name": "Deep", "options": []map[string]string{{"value": "smart", "name": "Smart", "description": "slower"}}},
		}},
		{"id": ids[1], "name": "Reasoning", "category": "thought_level", "type": "select", "currentValue": effort, "options": []map[string]string{
			{"value": "low", "name": "Low"}, {"value": "medium", "name": "Medium"}, {"value": "high", "name": "High"},
		}},
		{"id": "fancy", "name": "Toggle", "type": "boolean", "currentValue": true},
	}
}

// askThroughSous plays an agent using sous's MCP server: it starts the
// server named "sous", reports progress, asks the supervisor a question,
// hands in a result and returns the answer.
func withSous(list []acp.MCPServer, use func(func(string, map[string]string) (string, error)) (string, error)) (string, error) {
	var srv *acp.MCPServer
	for i := range list {
		if list[i].Name == "sous" {
			srv = &list[i]
		}
	}
	if srv == nil {
		return "", fmt.Errorf("session has no sous MCP server")
	}
	cmd := exec.Command(srv.Command, srv.Args...)
	cmd.Env = os.Environ()
	for _, e := range srv.Env {
		cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
	}
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	defer func() { stdin.Close(); cmd.Wait() }()
	rd := bufio.NewScanner(stdout)
	rd.Buffer(make([]byte, 64*1024), 1<<20)
	id := 0
	call := func(method string, params any) (json.RawMessage, error) {
		id++
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		stdin.Write(append(raw, '\n'))
		if !rd.Scan() {
			return nil, fmt.Errorf("sous mcp closed")
		}
		var m msg
		if err := json.Unmarshal(rd.Bytes(), &m); err != nil {
			return nil, err
		}
		if m.Error != nil {
			return nil, m.Error
		}
		return m.Result, nil
	}
	tool := func(name string, args map[string]string) (string, error) {
		raw, err := call("tools/call", map[string]any{"name": name, "arguments": args})
		if err != nil {
			return "", err
		}
		var r struct {
			Content []struct{ Text string } `json:"content"`
			IsError bool                    `json:"isError"`
		}
		json.Unmarshal(raw, &r)
		text := ""
		if len(r.Content) > 0 {
			text = r.Content[0].Text
		}
		if r.IsError {
			return "", fmt.Errorf("%s: %s", name, text)
		}
		return text, nil
	}
	if _, err := call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "fake", "version": "1"}}); err != nil {
		return "", err
	}
	stdin.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"))
	return use(tool)
}

func askThroughSous(list []acp.MCPServer) (string, error) {
	return withSous(list, func(tool func(string, map[string]string) (string, error)) (string, error) {
		if _, err := tool("report_progress", map[string]string{"text": "看完了代码，准备改端口"}); err != nil {
			return "", err
		}
		answer, err := tool("ask_supervisor", map[string]string{"question": "端口用 8080 还是 3000？我倾向 8080。"})
		if err != nil {
			return "", err
		}
		if _, err := tool("report_result", map[string]string{"done": "按回答写了 answer.txt", "verified": "没有验证", "decide": "无"}); err != nil {
			return "", err
		}
		return answer, nil
	})
}
