package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/XiaoXianThis/Sous/apps/core/internal/policy"
	"github.com/XiaoXianThis/Sous/apps/core/internal/project"
	"github.com/XiaoXianThis/Sous/apps/core/internal/proto"
	"github.com/XiaoXianThis/Sous/apps/core/internal/workspace"
)

// Milestone M5 on the node: the machine's role, the repositories in its
// workspaces, and setup runs that clone or create a project.

// role is the machine's role from node.json.
func (d *Daemon) role() string { return d.conf().RoleName() }

// reposState remembers the last report, so an unchanged scan is not sent.
type reposState struct {
	mu   sync.Mutex
	last string
	kick chan struct{}
}

// reposLoop scans the workspaces when the connection starts, every
// inventoryEvery, and whenever something asks (a workspace changed, a
// project was cloned or created).
func (d *Daemon) reposLoop(ctx context.Context) {
	t := time.NewTicker(inventoryEvery)
	defer t.Stop()
	d.repos.mu.Lock()
	d.repos.last = "" // a new connection: report once again
	d.repos.mu.Unlock()
	for {
		d.sendRepos(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-d.repos.kick:
		}
	}
}

// rescan asks the repos loop to scan again soon.
func (d *Daemon) rescan() {
	select {
	case d.repos.kick <- struct{}{}:
	default:
	}
}

// scanRepos scans the workspaces now.
func (d *Daemon) scanRepos(ctx context.Context) proto.NodeRepos {
	ws := d.conf().WorkspaceDirs()
	repos := workspace.Scan(ctx, ws)
	if repos == nil {
		repos = []proto.Repo{}
	}
	return proto.NodeRepos{Workspaces: ws, Repos: repos}
}

// sendRepos reports the scan when it changed since the last report.
func (d *Daemon) sendRepos(ctx context.Context) proto.NodeRepos {
	nr := d.scanRepos(ctx)
	if ctx.Err() != nil {
		return nr
	}
	raw, _ := json.Marshal(nr)
	d.repos.mu.Lock()
	same := d.repos.last == string(raw)
	d.repos.last = string(raw)
	d.repos.mu.Unlock()
	if !same {
		d.ob.reliable(proto.New(proto.TNodeRepos, nr))
	}
	return nr
}

var wokVersionRE = regexp.MustCompile(`v?\d+\.\d+(\.\d+)?[\w.+-]*`)

// wokVersion is the version of wok on PATH, or "" when it is missing.
func wokVersion(ctx context.Context) string {
	path, err := exec.LookPath("wok")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "unknown"
	}
	if v := wokVersionRE.FindString(string(out)); v != "" {
		return v
	}
	return strings.TrimSpace(string(out))
}

// setWorkspaces replaces the workspaces in node.json and scans them.
func (a *admin) setWorkspaces(ctx context.Context, ws []string) (proto.NodeRepos, error) {
	var clean []string
	for _, w := range ws {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		if !strings.HasPrefix(w, "~") && !filepath.IsAbs(w) {
			return proto.NodeRepos{}, nodeErr(proto.ErrCodeInvalid, "工作空间要用绝对路径或 ~/ 开头：%s", w)
		}
		if fi, err := os.Stat(ExpandPath(w)); err != nil || !fi.IsDir() {
			return proto.NodeRepos{}, nodeErr(proto.ErrCodeInvalid, "这台机器上没有目录 %s", w)
		}
		clean = append(clean, w)
	}
	if err := a.updateConfig(func(c *Config) { c.Workspaces = clean }); err != nil {
		return proto.NodeRepos{}, err
	}
	return a.d.sendRepos(ctx), nil
}

// setRole changes the machine's role. A dev machine may become a server
// remotely; a server becomes dev again only by editing node.json here.
func (a *admin) setRole(role string) error {
	if role != proto.RoleDev && role != proto.RoleServer {
		return nodeErr(proto.ErrCodeInvalid, "角色只能是 dev 或 server")
	}
	if role == a.d.role() {
		return nil
	}
	if role == proto.RoleDev {
		return nodeErr(proto.ErrCodeDisabled, "服务器不能远程改回开发机：请在这台机器的 node.json 里删掉 \"role\"")
	}
	return a.updateConfig(func(c *Config) { c.Role = role })
}

var prURLRE = regexp.MustCompile(`^https://github\.com/[\w.-]+/[\w.-]+/pull/\d+$`)

// prState asks gh whether a pull request was merged.
func (a *admin) prState(ctx context.Context, url string) (proto.PRState, error) {
	if !prURLRE.MatchString(url) {
		return proto.PRState{}, nodeErr(proto.ErrCodeInvalid, "不是 GitHub 的 PR 地址：%s", url)
	}
	out, err := workspace.Run(ctx, "", "gh", "pr", "view", url, "--json", "state,mergedAt")
	if err != nil {
		return proto.PRState{}, nodeErr(proto.ErrCodeFailed, "gh pr view 失败：%v", err)
	}
	var v struct {
		State    string `json:"state"`
		MergedAt string `json:"mergedAt"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return proto.PRState{}, err
	}
	return proto.PRState{State: v.State, MergedAt: v.MergedAt}, nil
}

// permit asks for one command the way an agent's request would be: the
// policy classifies it and the run's mode decides; the user or the approver
// answers. It reports whether the command may run.
func (r *run) permit(ask, command, dir, detail string) (bool, error) {
	raw, _ := json.Marshal(map[string]string{"command": command})
	req := policy.Request{ToolKind: "execute", RawInput: raw, Workdir: dir}
	cat, why := r.d.pol().Classify(req)
	act, assess, reason := r.judge(req, cat)
	if note := assess.Note(); note != "" && act != policy.Allow {
		detail = note + "\n\n" + detail
	}
	switch act {
	case policy.Deny:
		r.recordPolicy(ask+"（"+why+"）", cat, "deny")
		return false, errors.New("底线规则拒绝了：" + why)
	case policy.Allow:
		r.recordPolicy(ask+reasonSuffix(reason), cat, "allow")
		return true, nil
	case policy.AskAI:
		r.status("等审批 agent 判断能否" + ask)
		return r.askOK(ask, detail, cat, "ai"), nil
	}
	r.status("等你批准" + ask)
	return r.askOK(ask, detail, cat, "user"), nil
}

func (r *run) askOK(ask, detail string, cat policy.Category, review string) bool {
	options := []proto.Option{{OptionID: "allow", Name: "批准", Kind: "allow_once"}, {OptionID: "reject", Name: "拒绝", Kind: "reject_once"}}
	opt, _ := r.ask(ask, "execute", detail, cat, review, options)
	return opt == "allow"
}

// setup clones a project, or creates a new one, in the first workspace.
// The commands are fixed here; the Hub only names the project, and every
// command goes through the policy like an agent's.
func (r *run) setup() {
	st := r.spec.Setup
	fail := func(err error) { r.end(r.failureStatus(), "", "", "", err) }
	if st == nil || !project.ValidName(st.Name) {
		fail(errors.New("项目名只能是小写字母、数字和连字符"))
		return
	}
	ws := r.d.conf().WorkspaceDirs()
	if len(ws) == 0 {
		fail(errors.New("这台机器还没有设置工作空间（node.json 的 workspaces）"))
		return
	}
	root := ws[0]
	if err := os.MkdirAll(root, 0o755); err != nil {
		fail(err)
		return
	}
	dir := filepath.Join(root, st.Name)
	if _, err := os.Stat(dir); err == nil {
		fail(fmt.Errorf("%s 已经存在", dir))
		return
	}
	r.markStarted(workspace.Prepared{Dir: dir})

	step := func(ask, cwd, name string, args ...string) bool {
		cmd := name + " " + strings.Join(quoteArgs(args), " ")
		ok, err := r.permit(ask, cmd, cwd, "将在 "+cwd+" 执行：\n  "+cmd)
		if err != nil {
			fail(err)
			return false
		}
		if !ok {
			if r.ctx.Err() != nil {
				r.end("cancelled", "", "", "", errors.New(r.stopReasonText()))
			} else {
				r.end("cancelled", "", "", "", errors.New("没有获得"+ask+"的许可"))
			}
			return false
		}
		r.status("正在" + ask)
		ctx, cancel := context.WithTimeout(r.ctx, 10*time.Minute)
		defer cancel()
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = cwd
		out, err := c.CombinedOutput()
		if err != nil {
			fail(fmt.Errorf("%s失败：%v\n%s", ask, err, truncate(strings.TrimSpace(string(out)), 2000)))
			return false
		}
		return true
	}

	var summary string
	if r.spec.Purpose == proto.PurposeClone {
		if strings.HasPrefix(st.Remote, "-") || project.Key(st.Remote) == "" && !filepath.IsAbs(st.Remote) {
			fail(fmt.Errorf("不是可以 clone 的远端地址：%s", st.Remote))
			return
		}
		if !step("clone "+st.Remote, root, "git", "clone", "--", st.Remote, st.Name) {
			return
		}
		summary = "已 clone 到 " + dir
	} else {
		if st.Layout == project.LayoutWokspace {
			if _, err := exec.LookPath("wok"); err != nil {
				fail(errors.New("这台机器上没有安装 wok：https://github.com/XiaoXianThis/wokspace"))
				return
			}
			if !step("用 wok 创建 "+st.Name, root, "wok", "init", st.Name, "--name", st.Name) {
				return
			}
			if !step("检查 wokspace 结构", dir, "wok", "check") {
				return
			}
		} else {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				fail(err)
				return
			}
			if !step("git init "+st.Name, dir, "git", "init") {
				return
			}
		}
		summary = "已创建 " + dir
		// Every task branches from HEAD, so a new repository needs a root
		// commit now; without one, every run in the project fails at once.
		if err := initialCommit(r.ctx, dir); err != nil {
			fail(err)
			return
		}
		if st.Owner != "" {
			if !regexp.MustCompile(`^[\w.-]+$`).MatchString(st.Owner) {
				fail(fmt.Errorf("GitHub 用户或组织名不对：%s", st.Owner))
				return
			}
			vis := "--private"
			if !st.Private {
				vis = "--public"
			}
			if !step("创建 GitHub 仓库 "+st.Owner+"/"+st.Name+" 并 push", dir, "gh", "repo", "create", st.Owner+"/"+st.Name, vis, "--source", ".", "--push") {
				return
			}
			summary += "，GitHub 仓库 " + st.Owner + "/" + st.Name
		}
	}
	r.d.rescan()
	r.end("succeeded", "", summary, "", nil)
	slog.Info("daemon: project setup done", "run", r.spec.RunID, "dir", dir)
}

// initialCommit commits everything in a new repository as its root commit.
// It is a no-op when HEAD already exists.
func initialCommit(ctx context.Context, dir string) error {
	if _, err := workspace.Run(ctx, dir, "git", "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		return nil
	}
	if _, err := workspace.Run(ctx, dir, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add 失败：%w", err)
	}
	if _, err := workspace.Run(ctx, dir, "git", "-c", "user.name=sous", "-c", "user.email=sous@localhost", "commit", "-q", "--allow-empty", "-m", "Initial commit"); err != nil {
		return fmt.Errorf("创建初始提交失败：%w", err)
	}
	return nil
}

func quoteArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			a = fmt.Sprintf("%q", a)
		}
		out[i] = a
	}
	return out
}

// fetchEvery is how often the repositories are fetched in the background.
const fetchEvery = 10 * time.Minute

// fetchLoop fetches every repository in the workspaces that has an origin,
// one at a time, then reports the scan, so the Hub knows which checkouts
// are behind. Fetching only updates remote-tracking branches.
func (d *Daemon) fetchLoop(ctx context.Context) {
	t := time.NewTimer(time.Minute) // not all at once with the first scan
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if c := d.conf(); c.AutoFetch == nil || *c.AutoFetch {
			fetched := 0
			for _, r := range workspace.Scan(ctx, c.WorkspaceDirs()) {
				if r.Remote == "" || ctx.Err() != nil {
					continue
				}
				if _, err := workspace.Fetch(ctx, r.Path); err == nil {
					fetched++
				}
			}
			if fetched > 0 {
				d.rescan()
			}
		}
		t.Reset(fetchEvery)
	}
}

// repoOp syncs a project checkout with its upstream, or saves its
// uncommitted work to a branch on the remote. The commands are fixed; the
// directory follows the rules of the project file endpoints.
func (a *admin) repoOp(ctx context.Context, op, root string) (any, error) {
	dir, _, _, err := a.projectPath(proto.ProjectArgs{Root: root})
	if err != nil {
		return nil, err
	}
	var res proto.RepoResult
	switch op {
	case proto.OpRepoFetch:
		if _, ferr := workspace.Fetch(ctx, dir); ferr != nil {
			err = ferr
		} else {
			res = proto.RepoResult{Outcome: "fetched", Message: "已从 origin 拉取最新的分支信息"}
		}
	case proto.OpRepoSync:
		res, err = workspace.Sync(ctx, dir)
	default:
		res, err = workspace.WIP(ctx, dir, a.d.conf().Name)
	}
	if err != nil {
		return nil, nodeErr(proto.ErrCodeFailed, "%v", err)
	}
	slog.Info("daemon: repository "+op, "dir", dir, "outcome", res.Outcome)
	a.d.rescan()
	return res, nil
}
