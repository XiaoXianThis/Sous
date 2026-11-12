package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/acp"
	"github.com/XiaoXianThis/Sous/apps/core/internal/agents"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
)

// admin answers the Hub's node.request frames (work/specs/agent-admin):
// detecting, installing and logging in agents, their default session
// options, API keys, and their config files. Every command it runs and
// every file it touches is fixed in internal/agents; requests only name
// an agent and a file id.
type admin struct {
	d *Daemon

	// redetect asks the inventory loop to report at once.
	redetect chan struct{}

	mu      sync.Mutex
	jobs    map[string]*job
	options map[string]optionsEntry // by agent: what its last session reported
	fileMu  sync.Mutex              // one config write at a time
}

type optionsEntry struct {
	Options []proto.ConfigOption `json:"options"`
	At      int64                `json:"at"`
}

func newAdmin(d *Daemon) *admin {
	a := &admin{d: d, redetect: make(chan struct{}, 1), jobs: map[string]*job{}, options: map[string]optionsEntry{}}
	if raw, err := os.ReadFile(a.optionsPath()); err == nil {
		json.Unmarshal(raw, &a.options)
	}
	return a
}

func (a *admin) optionsPath() string { return filepath.Join(a.d.root, "agent-options.json") }

// kick makes the inventory loop detect and report again.
func (a *admin) kick() {
	select {
	case a.redetect <- struct{}{}:
	default:
	}
}

// describe adds what node.json and the caches know to detected agents.
func (a *admin) describe(infos []proto.AgentInfo) []proto.AgentInfo {
	cfg := a.d.conf()
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range infos {
		in := &infos[i]
		in.EnvSet = sortedKeys(cfg.AgentEnv[in.Name])
		if o, ok := a.options[in.Name]; ok {
			in.Options, in.OptionsAt = o.Options, o.At
		}
		if defs := cfg.AgentOptions[in.Name]; len(defs) > 0 {
			in.Defaults = defs
		}
		in.LaunchOver = len(cfg.Agents[in.Name]) > 0
		in.Disabled = cfg.AgentDisabled(in.Name)
		in.Default = cfg.DefaultAgent == in.Name
		for _, j := range a.jobs {
			j.mu.Lock()
			live := j.running
			j.mu.Unlock()
			if live && j.agent == in.Name {
				in.Job = &proto.JobStarted{JobID: j.id, Kind: j.kind, Agent: j.agent, Command: j.command}
			}
		}
	}
	return infos
}

// remember keeps the options an agent's session reported, for the page.
// Only the kinds the user picks a default for are kept: mode follows the
// run's permission mode.
func (a *admin) remember(spec agents.Spec, opts []acp.ConfigOption) {
	agent := spec.Name
	var keep []proto.ConfigOption
	for _, o := range opts {
		if o.Category == "mode" || isForced(spec, o.ID) {
			continue
		}
		po := proto.ConfigOption{ID: o.ID, Name: o.Name, Description: o.Description, Category: o.Category, Current: o.Current}
		for _, v := range o.Values {
			po.Values = append(po.Values, proto.ConfigValue{Value: v.Value, Name: v.Name, Description: v.Description, Group: v.Group})
		}
		keep = append(keep, po)
	}
	a.mu.Lock()
	prev, had := a.options[agent]
	a.options[agent] = optionsEntry{Options: keep, At: time.Now().UnixMilli()}
	raw, _ := json.MarshalIndent(a.options, "", "  ")
	a.mu.Unlock()
	if err := writeFileAtomic(a.optionsPath(), raw, 0o600); err != nil {
		slog.Warn("daemon: save agent options", "err", err)
	}
	if !had || !sameOptions(prev.Options, keep) {
		a.kick()
	}
}

func sameOptions(a, b []proto.ConfigOption) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// serve answers one request. It runs on its own goroutine.
func (a *admin) serve(ctx context.Context, reqID string, req proto.NodeRequest) {
	res, err := a.do(ctx, req)
	a.d.mu.Lock()
	nt := a.d.trust
	a.d.mu.Unlock()
	if nt != nil {
		nt.jobStarted(reqID, res)
	}
	resp := proto.NodeResponse{ReqID: reqID, OK: err == nil}
	if err != nil {
		var ne *proto.NodeError
		if !errors.As(err, &ne) {
			ne = &proto.NodeError{Code: proto.ErrCodeFailed, Message: err.Error()}
		}
		resp.Error = ne
		slog.Info("daemon: admin request failed", "op", req.Op, "err", err)
	} else if res != nil {
		resp.Result, _ = json.Marshal(res)
	}
	a.d.ob.ephemeral(proto.New(proto.TNodeResponse, resp))
}

func nodeErr(code, format string, args ...any) error {
	return &proto.NodeError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func (a *admin) do(ctx context.Context, req proto.NodeRequest) (any, error) {
	if !a.d.conf().AdminEnabled() {
		return nil, nodeErr(proto.ErrCodeDisabled, "这台机器在 node.json 里关闭了远程管理（remote_admin: false）")
	}
	switch req.Op {
	case proto.OpApprovalRead:
		var args proto.ApprovalReadArgs
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
		return a.approvalRead(ctx, args)
	case proto.OpDetect:
		return a.d.sendInventory(ctx), nil
	case proto.OpProbe:
		var args proto.AgentArgs
		json.Unmarshal(req.Args, &args)
		return a.probe(ctx, args.Agent)
	case proto.OpInstall, proto.OpLogin, proto.OpLogout:
		var args proto.AgentArgs
		json.Unmarshal(req.Args, &args)
		return a.startJob(req.Op, args)
	case proto.OpSetDisabled:
		var args proto.AgentArgs
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return nil, nodeErr(proto.ErrCodeInvalid, "无效的参数：%v", err)
		}
		if err := a.setDisabled(args.Agent, args.Disabled); err != nil {
			return nil, err
		}
		spec, _ := a.spec(args.Agent)
		for _, info := range a.describe(agents.Detect(ctx, []agents.Spec{spec})) {
			if info.Name == args.Agent {
				return info, nil
			}
		}
		return nil, nodeErr(proto.ErrCodeNotFound, "agent 不存在：%s", args.Agent)
	case proto.OpSetDefaults:
		var args proto.AgentArgs
		json.Unmarshal(req.Args, &args)
		return nil, a.setDefaults(args.Agent, args.Options)
	case proto.OpSetEnv:
		var args proto.AgentArgs
		json.Unmarshal(req.Args, &args)
		return nil, a.setEnv(args.Agent, args.Name, args.Value)
	case proto.OpJobInput, proto.OpJobResize, proto.OpJobCancel, proto.OpJobTail:
		var args proto.JobArgs
		json.Unmarshal(req.Args, &args)
		j := a.job(args.JobID)
		if j == nil || !j.ownedBy(args.Owner) {
			return nil, nodeErr(proto.ErrCodeNotFound, "这个作业已经不在了")
		}
		switch req.Op {
		case proto.OpJobInput:
			return nil, j.input(args.Data)
		case proto.OpJobResize:
			return nil, j.resize(args.Cols, args.Rows)
		case proto.OpJobCancel:
			j.stop()
			return nil, nil
		default:
			return j.tail(), nil
		}
	case proto.OpConfigRead:
		var args proto.ConfigArgs
		json.Unmarshal(req.Args, &args)
		return a.readConfig(args.Agent, args.File)
	case proto.OpConfigWrite:
		var args proto.ConfigArgs
		json.Unmarshal(req.Args, &args)
		return a.writeConfig(args)
	case proto.OpProjectLs, proto.OpProjectRead, proto.OpProjectWrite:
		var args proto.ProjectArgs
		json.Unmarshal(req.Args, &args)
		return a.projectOp(req.Op, args)
	case proto.OpShellOpen, proto.OpShellList:
		var args proto.ShellArgs
		json.Unmarshal(req.Args, &args)
		if req.Op == proto.OpShellOpen {
			return a.openShell(args)
		}
		return a.listShells(args)
	case proto.OpRepoSync, proto.OpRepoWIP, proto.OpRepoFetch:
		var args proto.RepoArgs
		json.Unmarshal(req.Args, &args)
		return a.repoOp(ctx, req.Op, args.Root)
	case proto.OpScan:
		return a.d.sendRepos(ctx), nil
	case proto.OpSetWorkspaces:
		var args proto.WorkspacesArgs
		json.Unmarshal(req.Args, &args)
		return a.setWorkspaces(ctx, args.Workspaces)
	case proto.OpSetRole:
		var args proto.RoleArgs
		json.Unmarshal(req.Args, &args)
		return nil, a.setRole(args.Role)
	case proto.OpPRState:
		var args proto.PRArgs
		json.Unmarshal(req.Args, &args)
		return a.prState(ctx, args.URL)
	case proto.OpGitStatus:
		var args proto.RepoArgs
		json.Unmarshal(req.Args, &args)
		return a.gitStatus(ctx, args.Root)
	case proto.OpLook:
		var args proto.LookArgs
		json.Unmarshal(req.Args, &args)
		return a.look(ctx, args)
	case proto.OpProjectGrep:
		var args proto.GrepArgs
		json.Unmarshal(req.Args, &args)
		return a.grep(ctx, args)
	}
	return nil, nodeErr(proto.ErrCodeInvalid, "不认识的操作 %q，可能需要更新 sous", req.Op)
}

func (a *admin) spec(name string) (agents.Spec, error) {
	s, ok := a.d.findSpec(name)
	if !ok {
		return s, nodeErr(proto.ErrCodeNotFound, "这台机器不认识 agent %q", name)
	}
	return s, nil
}

const probeTimeout = 2 * time.Minute

// probe starts the agent, opens a session in a scratch directory to read
// its config options, and closes it again. Opening a session does not
// talk to the model.
func (a *admin) probe(ctx context.Context, name string) (any, error) {
	spec, err := a.spec(name)
	if err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(filepath.Join(a.d.root, "scratch", "probe-"+safeFile(name)))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	cmd, err := spec.Command(dir)
	if err != nil {
		return nil, nodeErr(proto.ErrCodeFailed, "找不到 %s 的启动命令 %q", spec.Title, spec.Launch[0])
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	client, err := acp.Start(ctx, cmd, acp.Callbacks{})
	if err != nil {
		return nil, fmt.Errorf("启动 %s 失败：%w", spec.Title, err)
	}
	defer client.Close()
	s, err := client.NewSession(ctx, dir)
	if err != nil {
		if errors.Is(err, acp.ErrNotLoggedIn) {
			return nil, nodeErr(proto.ErrCodeFailed, "%s 还没有登录", spec.Title)
		}
		return nil, err
	}
	a.remember(spec, s.Options)
	a.mu.Lock()
	e := a.options[name]
	a.mu.Unlock()
	return e, nil
}

// applyDefaults sets the session options node.json asks for, model first
// (the others may depend on it). Values the agent no longer offers are
// skipped with a warning; it returns the options as they end up.
//
// forced are options sous itself sets for the run's permission mode
// (Spec.AskOptions / OpenOptions); they win over the user's defaults, and
// failing to set one fails the run, since the agent would then act
// without asking.
func (a *admin) applyDefaults(ctx context.Context, client *acp.Client, s acp.Session, spec agents.Spec, forced, persona map[string]string, warn func(string)) ([]acp.ConfigOption, error) {
	opts := s.Options
	if len(opts) == 0 {
		return opts, nil
	}
	defs := map[string]string{}
	for k, v := range a.d.conf().AgentOptions[spec.Name] {
		if !isForced(spec, k) {
			defs[k] = v
		}
	}
	// A persona's options win over the machine's defaults, never over what
	// sous forces for the permission mode.
	for k, v := range persona {
		if v != "" && !isForced(spec, k) {
			if _, f := forced[k]; !f {
				defs[k] = v
			}
		}
	}
	// Decision 0024: a task names its model and effort without knowing the
	// agent's option ids; they replace the preset's by category.
	for _, alias := range []string{"model", "effort"} {
		v, ok := defs[alias]
		if !ok || slices.ContainsFunc(opts, func(o acp.ConfigOption) bool { return o.ID == alias }) {
			continue
		}
		delete(defs, alias)
		if id := aliasOption(opts, alias); id != "" && !isForced(spec, id) {
			if _, f := forced[id]; !f {
				defs[id] = v
			}
		} else if id == "" {
			warn(fmt.Sprintf("%s 没有可以设置的%s，这次用它自己的默认值", spec.Title, aliasName[alias]))
		}
	}
	for k, v := range forced {
		i := slices.IndexFunc(opts, func(o acp.ConfigOption) bool { return o.ID == k })
		if i < 0 {
			slog.Warn("daemon: the agent no longer reports an option sous sets", "agent", spec.Name, "option", k)
			continue
		}
		if opts[i].Current == v {
			continue
		}
		s.Options = opts
		next, err := client.SetConfigOption(ctx, s, k, v)
		if err != nil {
			return opts, fmt.Errorf("没能把 %s 的「%s」设为 %s，为了不绕过 sous 的权限规则，不继续执行：%w", spec.Title, opts[i].Name, v, err)
		}
		opts = next
	}
	if len(defs) == 0 {
		return opts, nil
	}
	ids := make([]string, 0, len(defs))
	for id := range defs {
		ids = append(ids, id)
	}
	category := func(id string) string {
		for _, o := range opts {
			if o.ID == id {
				return o.Category
			}
		}
		return ""
	}
	sort.SliceStable(ids, func(i, j int) bool {
		return category(ids[i]) == "model" && category(ids[j]) != "model"
	})
	for _, id := range ids {
		want := defs[id]
		i := slices.IndexFunc(opts, func(o acp.ConfigOption) bool { return o.ID == id })
		if i < 0 || want == "" || opts[i].Current == want {
			continue
		}
		if !opts[i].Has(want) {
			if v := matchValue(opts[i], want); v != "" {
				want = v
			} else {
				warn(fmt.Sprintf("%s「%s」不可用，这次用 agent 自己的默认值", opts[i].Name, want))
				continue
			}
			if opts[i].Current == want {
				continue
			}
		}
		s.Options = opts
		next, err := client.SetConfigOption(ctx, s, id, want)
		if err != nil {
			warn(fmt.Sprintf("没能把%s设为「%s」：%v", opts[i].Name, want, err))
			continue
		}
		opts = next
	}
	return opts, nil
}

var aliasName = map[string]string{"model": "模型选项", "effort": "思考强度选项"}

// aliasOption finds the option a task-level key means: the model option,
// or the reasoning level for effort.
func aliasOption(opts []acp.ConfigOption, alias string) string {
	want := map[string]string{"model": "model", "effort": "thought_level"}[alias]
	for _, o := range opts {
		if o.Category == want {
			return o.ID
		}
	}
	for _, o := range opts {
		id := strings.ToLower(o.ID)
		if alias == "effort" && (strings.Contains(id, "effort") || strings.Contains(id, "reasoning") || strings.Contains(id, "thought")) {
			return o.ID
		}
		if alias == "model" && id == "model_id" {
			return o.ID
		}
	}
	return ""
}

// matchValue finds the value a loosely written choice means: same value
// or name ignoring case, then a single value that contains it.
func matchValue(o acp.ConfigOption, want string) string {
	w := strings.ToLower(strings.TrimSpace(want))
	for _, v := range o.Values {
		if strings.ToLower(v.Value) == w || strings.ToLower(v.Name) == w {
			return v.Value
		}
	}
	found := ""
	for _, v := range o.Values {
		if strings.Contains(strings.ToLower(v.Value), w) || strings.Contains(strings.ToLower(v.Name), w) {
			if found != "" {
				return "" // ambiguous
			}
			found = v.Value
		}
	}
	return found
}

// isForced reports whether sous sets the option itself.
func isForced(spec agents.Spec, id string) bool {
	_, ask := spec.AskOptions[id]
	_, open := spec.OpenOptions[id]
	return ask || open
}

func (a *admin) setDisabled(agent string, disabled bool) error {
	if _, err := a.spec(agent); err != nil {
		return err
	}
	return a.updateConfig(func(c *Config) {
		var names []string
		for _, name := range c.DisabledAgents {
			if name == agent && !disabled {
				continue
			}
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		if disabled && !slices.Contains(names, agent) {
			names = append(names, agent)
		}
		c.DisabledAgents = names
	}, func(c *Config) error {
		if disabled && c.DefaultAgent == agent {
			return nodeErr(proto.ErrCodeInvalid, "这台电脑的默认 agent 是 %s，先在 node.json 换掉 default_agent，再停用它", agent)
		}
		return nil
	})
}

func (a *admin) setDefaults(agent string, options map[string]string) error {
	spec, err := a.spec(agent)
	if err != nil {
		return err
	}
	clean := map[string]string{}
	for k, v := range options {
		if k != "" && v != "" && !isForced(spec, k) {
			clean[k] = v
		}
	}
	return a.updateConfig(func(c *Config) {
		if len(clean) == 0 {
			delete(c.AgentOptions, agent)
			return
		}
		if c.AgentOptions == nil {
			c.AgentOptions = map[string]map[string]string{}
		}
		c.AgentOptions[agent] = clean
	})
}

func (a *admin) setEnv(agent, name, value string) error {
	spec, err := a.spec(agent)
	if err != nil {
		return err
	}
	if !slices.Contains(spec.KeyEnv, name) {
		return nodeErr(proto.ErrCodeInvalid, "%s 不能设置环境变量 %q", spec.Title, name)
	}
	return a.updateConfig(func(c *Config) {
		if value == "" {
			delete(c.AgentEnv[agent], name)
			if len(c.AgentEnv[agent]) == 0 {
				delete(c.AgentEnv, agent)
			}
			return
		}
		if c.AgentEnv == nil {
			c.AgentEnv = map[string]map[string]string{}
		}
		if c.AgentEnv[agent] == nil {
			c.AgentEnv[agent] = map[string]string{}
		}
		c.AgentEnv[agent][name] = value
	})
}

// updateConfig validates and changes node.json under one lock, applies it, and reports.
func (a *admin) updateConfig(change func(*Config), checks ...func(*Config) error) error {
	a.fileMu.Lock()
	defer a.fileMu.Unlock()
	cfg, err := LoadConfig(a.d.root)
	if err != nil {
		return err
	}
	for _, check := range checks {
		if err := check(&cfg); err != nil {
			return err
		}
	}
	change(&cfg)
	if err := SaveConfig(a.d.root, cfg); err != nil {
		return err
	}
	a.d.apply(cfg)
	a.kick()
	return nil
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".sous-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
