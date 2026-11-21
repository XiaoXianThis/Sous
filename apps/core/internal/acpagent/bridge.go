package acpagent

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/event"
	"github.com/XiaoXianThis/Sous/apps/core/internal/hub"
	"github.com/XiaoXianThis/Sous/apps/core/internal/provider"
	"github.com/XiaoXianThis/Sous/apps/core/internal/tool"
)

const bridgeEnv = "SOUS_TURN_MCP_URL"
const tokenEnv = "SOUS_TURN_MCP_TOKEN"
const maxCalls = 12

type bridgeRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type bridge struct {
	ctx      context.Context
	cancel   context.CancelFunc
	server   *http.Server
	address  string
	token    string
	registry *tool.Registry
	internal bool
	mu       sync.Mutex
	calls    int
	notes    []event.ToolNote
}

func openBridge(ctx context.Context, registry *tool.Registry, internal bool) (*bridge, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	b := &bridge{ctx: ctx, cancel: cancel, address: ln.Addr().String(), token: hex.EncodeToString(randBytes()), registry: registry, internal: internal}
	b.server = &http.Server{Handler: http.HandlerFunc(b.serve), ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	go func() { _ = b.server.Serve(ln) }()
	return b, nil
}

func randBytes() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // Go >=1.24 always fills the buffer
	return b
}

func (b *bridge) close() []event.ToolNote {
	b.cancel()
	_ = b.server.Close()
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.notes
}

func (b *bridge) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" || r.Host != b.address || r.URL.Path != "/mcp" || r.Header.Get("Origin") != "" ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.token)) != 1 {
		http.NotFound(w, r)
		return
	}
	var req bridgeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid tool request", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ctx.Err() != nil {
		http.Error(w, "turn ended", http.StatusGone)
		return
	}
	var result any
	switch req.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "sous-workspace", "version": "1"}}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		tools := []map[string]any{}
		for _, spec := range b.registry.Specs() {
			if (b.internal && spec.Name == "task_create") || (tool.TurnFrom(b.ctx).ProjectID != "" && spec.Name == "memory_search") {
				continue
			}
			tools = append(tools, map[string]any{"name": spec.Name, "description": spec.Description, "inputSchema": spec.Schema})
		}
		result = map[string]any{"tools": tools}
	case "tools/call":
		result = b.call(r.Context(), req.Params)
	default:
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}

func (b *bridge) call(request context.Context, raw json.RawMessage) any {
	type content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	format := func(text string, failed bool) any {
		return map[string]any{"content": []content{{"text", text}}, "isError": failed}
	}
	var call struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil || call.Name == "" {
		return format("无效的工具参数", true)
	}
	if b.calls >= maxCalls {
		return format("本轮工具调用已达上限", true)
	}
	b.calls++
	if b.internal && call.Name == "task_create" {
		return format("没有用户输入的自动回合不能创建任务", true)
	}
	known := false
	for _, spec := range b.registry.Specs() {
		known = known || spec.Name == call.Name
	}
	if !known {
		return format("当前主管没有此工具", true)
	}
	if len(call.Args) == 0 {
		call.Args = json.RawMessage(`{}`)
	}
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	stop := context.AfterFunc(request, cancel)
	defer stop()
	defer cancel()
	if request.Err() != nil {
		return format("请求已取消", true)
	}
	res := b.registry.Call(ctx, provider.ToolCall{Name: call.Name, Args: call.Args})
	text := clip(res.Content, 12_000)
	// Store only operation name and (for drafts) the durable receipt; avoid
	// storing private lookup results and untrusted raw arguments twice.
	n := event.ToolNote{Name: call.Name}
	if call.Name == "task_create" && !res.IsError {
		n.Result = text
	}
	if res.IsError {
		n.Error = "工具未成功执行"
	}
	b.notes = append(b.notes, n)
	return format(text, res.IsError)
}

func clip(s string, size int) string {
	if len(s) <= size {
		return s
	}
	return strings.ToValidUTF8(s[:size], "") + "…"
}

type capability struct {
	spec   provider.ToolSpec
	invoke func(context.Context, json.RawMessage) (string, error)
}

func (t capability) Spec() provider.ToolSpec { return t.spec }
func (t capability) Call(ctx context.Context, args json.RawMessage) (string, error) {
	return t.invoke(ctx, args)
}

func strictArgs(raw json.RawMessage, target any) error {
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		return errors.New("参数必须为 JSON 对象")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return errors.New("参数包含不允许的字段或类型")
	}
	return nil
}

func registry(d hub.AgentDeps) *tool.Registry {
	r := d.Tools.Subset("current_time", "memory_search")
	// Project chats do not get a global free-text memory search. The current
	// storage does not carry per-project memory ownership.
	r.Register(capability{provider.ToolSpec{Name: "memory_search", Description: "在工作区主对话查找已保存的长期记忆。", Schema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`)}, func(ctx context.Context, raw json.RawMessage) (string, error) {
		if tool.TurnFrom(ctx).ProjectID != "" {
			return "", errors.New("项目对话不能读取全局记忆")
		}
		var a struct {
			Query string `json:"query"`
		}
		if err := strictArgs(raw, &a); err != nil {
			return "", err
		}
		if len(a.Query) > 500 || strings.TrimSpace(a.Query) == "" {
			return "", errors.New("请提供有效的搜索词")
		}
		res := d.Tools.Call(ctx, provider.ToolCall{Name: "memory_search", Args: raw})
		if res.IsError {
			return "", errors.New("记忆查询失败")
		}
		return res.Content, nil
	}})
	r.Register(capability{provider.ToolSpec{Name: "project_list", Description: "列出当前作用域内的未归档项目名称和 ID。不读取文件。", Schema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}, func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := strictArgs(raw, &struct{}{}); err != nil {
			return "", err
		}
		ps, err := d.Log.Store.Projects(ctx)
		if err != nil {
			return "", errors.New("项目查询失败")
		}
		type project struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		out := []project{}
		scope := tool.TurnFrom(ctx).ProjectID
		for _, p := range ps {
			if !p.Archived() && (scope == "" || p.ID == scope) {
				out = append(out, project{p.ID, clip(p.Name, 96)})
			}
			if len(out) == 60 {
				break
			}
		}
		data, _ := json.Marshal(out)
		return string(data), nil
	}})
	r.Register(capability{provider.ToolSpec{Name: "task_create", Description: "仅在用户明确要求时登记一条待分配任务。不会派发或执行。", Schema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"detail":{"type":"string"},"project_id":{"type":"string"}},"required":["title"],"additionalProperties":false}`)}, func(ctx context.Context, raw json.RawMessage) (string, error) {
		var a struct {
			Title   string `json:"title"`
			Detail  string `json:"detail"`
			Project string `json:"project_id"`
		}
		if err := strictArgs(raw, &a); err != nil {
			return "", err
		}
		t, err := d.Tasks.FileSupervisorDraft(ctx, a.Project, a.Title, a.Detail)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已登记待分配任务 %s（未派发）：%s", t.ID, t.Title), nil
	}})
	return r
}

// ServeBridge is the MCP stdio process declared in ACP session/new. It proxies
// only four MCP methods, to an ephemeral numeric loopback address with no redirects.
func ServeBridge(in io.Reader, out io.Writer) error {
	address, token := os.Getenv(bridgeEnv), os.Getenv(tokenEnv)
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "/mcp" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(token) != 64 {
		return errors.New("缺少本轮主管的工具授权")
	}
	client := &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	conn := acp.NewConn(in, out, func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		switch method {
		case "initialize", "ping", "tools/list", "tools/call":
		case "notifications/initialized":
			return nil, nil
		default:
			return nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: "unsupported MCP method"}
		}
		data, err := json.Marshal(bridgeRequest{method, raw})
		if err != nil {
			return nil, err
		}
		r, err := http.NewRequestWithContext(ctx, "POST", address, strings.NewReader(string(data)))
		if err != nil {
			return nil, err
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(r)
		if err != nil {
			return nil, errors.New("本轮主管工具连接已关闭")
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return nil, errors.New("当前工具授权不可用")
		}
		var result json.RawMessage
		err = json.NewDecoder(io.LimitReader(res.Body, 128<<10)).Decode(&result)
		return result, err
	})
	<-conn.Done()
	if errors.Is(conn.Err(), io.EOF) {
		return nil
	}
	return conn.Err()
}
