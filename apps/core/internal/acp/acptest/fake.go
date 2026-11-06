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
//	noop   say a few words and end the turn, without a tool call
//	answer answer every prompt in words (a consultant or a reviewer): ask
//	       to read README.md first; when the prompt contains "改一下",
//	       also ask to edit it; when it asks for a verdict ("结论 JSON"),
//	       end with {"pass": …} (FAKE_ACP_VERDICT=fail fails). The answer
//	       says which prompt of the session it is, and whether the session
//	       was loaded.
//
// FAKE_ACP_LOAD=1 advertises loadSession and accepts session/load; a prompt
// in a loaded session writes hello.txt and ends the turn at once.
// FAKE_ACP_PROMPTS=<file> appends every prompt the agent receives to file.
// FAKE_ACP_OPTIONS=1 reports configOptions (model: fast / smart in two
// groups, effort: low / medium / high) and accepts
// session/set_config_option, which is also logged to FAKE_ACP_PROMPTS as
// "set <id>=<value>".
package acptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
)

const envFake = "SOUS_FAKE_ACP_AGENT"

// Command returns a command that starts the fake agent in mode.
func Command(mode, dir string) acp.Command {
	exe, _ := os.Executable()
	return acp.Command{Path: exe, Args: []string{"-test.run=^$"}, Dir: dir, Env: []string{envFake + "=1", "FAKE_ACP_MODE=" + mode}}
}

// RunIfFake turns the process into the fake agent when started by Command.
func RunIfFake() {
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
			if mode == "reject" {
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
			var p struct{ Cwd string }
			json.Unmarshal(m.Params, &p)
			cwd = p.Cwd
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
			case "model":
				optModel = p.Value
			case "effort":
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

func configOptions(model, effort string) []map[string]any {
	return []map[string]any{
		{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": model, "options": []map[string]any{
			{"group": "quick", "name": "Quick", "options": []map[string]string{{"value": "fast", "name": "Fast"}}},
			{"group": "deep", "name": "Deep", "options": []map[string]string{{"value": "smart", "name": "Smart", "description": "slower"}}},
		}},
		{"id": "effort", "name": "Reasoning", "category": "thought_level", "type": "select", "currentValue": effort, "options": []map[string]string{
			{"value": "low", "name": "Low"}, {"value": "medium", "name": "Medium"}, {"value": "high", "name": "High"},
		}},
		{"id": "fancy", "name": "Toggle", "type": "boolean", "currentValue": true},
	}
}
