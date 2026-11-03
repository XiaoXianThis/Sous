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
//	auth   like write, but session/new fails until authenticate is called
//	nologin session/new always needs a login
//	slow   stream a message, then wait until session/cancel
//	crash  exit in the middle of a prompt
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

	prompt := func(id *json.RawMessage, sid string) {
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
		kind, input := "execute", map[string]string{"command": "sh -c 'echo hi > hello.txt'"}
		switch mode {
		case "edit":
			kind, input = "edit", map[string]string{"path": "hello.txt"}
		case "floor":
			input = map[string]string{"command": "rm -rf ~"}
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
			res := map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": false},
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
			result(m.ID, map[string]any{"sessionId": "fake-session", "modes": map[string]any{
				"currentModeId": "auto", "availableModes": []map[string]string{{"id": "default", "name": "Ask"}, {"id": "auto", "name": "Auto"}}}})
		case "session/set_mode":
			result(m.ID, map[string]any{})
		case "session/prompt":
			var p struct {
				SessionID string `json:"sessionId"`
			}
			json.Unmarshal(m.Params, &p)
			go prompt(m.ID, p.SessionID)
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
