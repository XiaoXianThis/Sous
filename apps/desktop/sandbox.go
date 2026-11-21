package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const sandboxURL = "wails://localhost/sandbox/index.html"
const sandboxPrefix = "sous.sandbox:"

func sandboxSupported() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" }

type sandboxRequest struct {
	ID      int    `json:"id"`
	Action  string `json:"action"`
	Value   string `json:"value,omitempty"`
	Offline bool   `json:"offline,omitempty"`
}

type sandboxReply struct {
	ID       int             `json:"id"`
	State    *sandboxView    `json:"state,omitempty"`
	Progress string          `json:"progress,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type sandboxView struct {
	Available   bool            `json:"available"`
	Ready       bool            `json:"ready"`
	MSB         string          `json:"msb,omitempty"`
	Firmware    string          `json:"firmware,omitempty"`
	Image       string          `json:"image,omitempty"`
	Source      string          `json:"source,omitempty"`
	Expires     int64           `json:"expires_ms,omitempty"`
	Result      string          `json:"result,omitempty"`
	Review      json.RawMessage `json:"review,omitempty"`
	Done        map[string]bool `json:"applied,omitempty"`
	TasksOnline bool            `json:"tasks_connected"`
	Tasks       []sandboxIntent `json:"tasks,omitempty"`
	Task        sandboxIntent   `json:"task"`
}

type sandboxGrant struct {
	path, identity string
	expires        time.Time
}

type sandboxHost struct {
	app                  *application.App
	home                 string
	mu                   sync.Mutex
	window               *application.WebviewWindow
	working              bool
	stopping             bool
	cancel               context.CancelFunc
	finished             chan struct{}
	grant                sandboxGrant
	msb, firmware, image string
	result               string
	review               json.RawMessage
	revision             string
	canApply             bool
	applied              map[string]bool
	previewed            string
	previewComplete      bool
	hub                  *LocalHub
	openWorkspace        func(context.Context) error
	tasks                []sandboxIntent
	task                 sandboxIntent
	tasksOnline          bool
}

func (s *sandboxHost) open() {
	if !sandboxSupported() {
		return
	}
	s.mu.Lock()
	w := s.window
	if w == nil {
		w = s.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name: "sandbox-workbench", Title: "Sous · 本机沙盒", URL: "/sandbox/index.html", Width: 940, Height: 770, MinWidth: 500, MinHeight: 480,
		})
		s.window = w
		s.loadSetup()
		w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { w.Hide(); e.Cancel() })
	}
	s.mu.Unlock()
	w.Show()
	w.Focus()
}

func parseSandboxRequest(message string, main bool, origin string) (sandboxRequest, bool) {
	var r sandboxRequest
	if !main || origin != sandboxURL || !strings.HasPrefix(message, sandboxPrefix) || len(message) > 16<<10 {
		return r, false
	}
	d := json.NewDecoder(strings.NewReader(strings.TrimPrefix(message, sandboxPrefix)))
	d.DisallowUnknownFields()
	var extra json.RawMessage
	ok := d.Decode(&r) == nil && r.ID > 0 && r.ID <= 1<<30 && errors.Is(d.Decode(&extra), io.EOF)
	return r, ok
}

func (s *sandboxHost) raw(w application.Window, message string, origin *application.OriginInfo) {
	if origin == nil || !sandboxSupported() {
		return
	}
	r, ok := parseSandboxRequest(message, origin.IsMainFrame, origin.Origin)
	if !ok {
		return
	}
	s.mu.Lock()
	if s.window == nil || s.window != w || s.stopping {
		s.mu.Unlock()
		return
	}
	target := s.window
	if r.Action == "cancel" && r.Value == "" {
		if s.cancel != nil {
			s.cancel()
		}
		s.mu.Unlock()
		s.send(target, sandboxReply{ID: r.ID})
		return
	}
	if s.working {
		s.mu.Unlock()
		s.send(target, sandboxReply{ID: r.ID, Error: "另一项本机操作尚未结束"})
		return
	}
	s.working = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.finished = done
	if r.Action == "run" || r.Action == "recover" {
		s.cancel = cancel
	}
	s.mu.Unlock()
	go func() {
		defer cancel()
		defer close(done)
		data, err := s.handle(ctx, target, r)
		var failure string
		if err != nil {
			failure = cleanSandboxText(err.Error(), 1200)
		}
		view := s.view()
		s.mu.Lock()
		s.working = false
		s.cancel = nil
		s.mu.Unlock()
		s.send(target, sandboxReply{ID: r.ID, State: &view, Data: data, Error: failure})
	}()
}

func (s *sandboxHost) send(w *application.WebviewWindow, reply sandboxReply) {
	data, err := json.Marshal(reply)
	if err != nil {
		return
	}
	application.InvokeAsync(func() {
		w.ExecJS(`if (window.location.href === "` + sandboxURL + `") window.sousSandboxReply?.(` + string(data) + ")")
	})
}

func (s *sandboxHost) stop() {
	s.mu.Lock()
	s.stopping = true
	cancel, done := s.cancel, s.finished
	s.mu.Unlock()
	if cancel == nil || done == nil {
		return
	}
	cancel()
	// Core supervises native VM destruction; don't block the event loop here.
	// The private stdin close also cancels if this process exits abruptly.
}

func (s *sandboxHost) view() sandboxView {
	bin := sandboxBinary()
	v := sandboxView{Available: bin != "", Ready: bin != "" && s.msb != "" && s.firmware != "", MSB: s.msb, Firmware: s.firmware, Image: s.image, Result: s.result, Review: s.review, Done: s.applied, TasksOnline: s.tasksOnline, Tasks: s.tasks, Task: s.task}
	if time.Now().Before(s.grant.expires) {
		v.Source, v.Expires = s.grant.path, s.grant.expires.UnixMilli()
	}
	if s.applied != nil {
		v.Done = make(map[string]bool, len(s.applied))
		for k, x := range s.applied {
			v.Done[k] = x
		}
	}
	return v
}

func (s *sandboxHost) handle(ctx context.Context, w *application.WebviewWindow, r sandboxRequest) (json.RawMessage, error) {
	if r.Action == "ready" && r.Value == "" {
		_ = s.refreshTasks(ctx)
		return nil, nil
	}
	bin := sandboxBinary()
	if bin == "" {
		return nil, errors.New("没有随 App 提供的沙盒模块；请使用带沙盒的构建")
	}
	pick := func(title string, files, dirs bool) (string, error) {
		selected, err := s.app.Dialog.OpenFile().AttachToWindow(w).SetTitle(title).CanChooseFiles(files).CanChooseDirectories(dirs).ResolvesAliases(false).SetButtonText("选择").PromptForSingleSelection()
		if err != nil || selected == "" {
			return "", err
		}
		st, err := os.Lstat(selected)
		if err != nil || (!files || !st.Mode().IsRegular()) && (!dirs || !st.IsDir()) {
			return "", errors.New("请选择文件或目录本身，不要选择链接")
		}
		// Finder can refer to /var or /tmp. Resolve its ancestors once at
		// the owner action, then the runner uses descriptor-only traversal.
		return filepath.EvalSymlinks(selected)
	}
	if r.Value != "" && r.Action != "run" && r.Action != "preview" && r.Action != "apply" && r.Action != "select-task" {
		return nil, errors.New("本机操作参数无效")
	}
	switch r.Action {
	case "open-workspace":
		if s.openWorkspace == nil {
			return nil, errNoSandboxPipe
		}
		openCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := s.openWorkspace(openCtx); err != nil {
			return nil, err
		}
		return nil, s.refreshTasks(openCtx)
	case "list-tasks":
		return nil, s.refreshTasks(ctx)
	case "select-task":
		return nil, s.selectTask(ctx, r.Value)
	case "pick-msb", "pick-firmware", "pick-image":
		labels := map[string]string{"pick-msb": "选择官方 msb v0.7.16", "pick-firmware": "选择匹配的 libkrunfw.dylib", "pick-image": "选择已校验的离线 OCI 镜像归档"}
		selected, err := pick(labels[r.Action], true, false)
		if err != nil || selected == "" {
			return nil, err
		}
		switch r.Action {
		case "pick-msb":
			s.msb = selected
		case "pick-firmware":
			s.firmware = selected
		case "pick-image":
			s.image = selected
		}
		return nil, s.saveSetup()
	case "clear-image":
		s.image = ""
		return nil, s.saveSetup()
	case "pick-source":
		if s.msb == "" || s.firmware == "" {
			return nil, errors.New("请先选择 Microsandbox 运行时和固件")
		}
		selected, err := pick("仅为下一次运行授权一个文件或目录", true, true)
		if err != nil || selected == "" {
			return nil, err
		}
		raw, err := callSandbox(ctx, bin, []string{"identify", selected}, false, nil)
		val := ""
		if err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &val) != nil || val == "" {
			return nil, errors.New("未能绑定所选文件或目录")
		}
		s.grant = sandboxGrant{path: selected, identity: val, expires: time.Now().Add(10 * time.Minute)}
	case "clear-source":
		s.grant = sandboxGrant{}
	case "pick-review":
		selected, err := pick("打开 Sous 生成的审阅目录", false, true)
		if err != nil || selected == "" {
			return nil, err
		}
		return nil, s.loadReview(ctx, bin, selected)
	case "run":
		return nil, s.runInSandbox(ctx, w, r, bin)
	case "recover":
		if s.msb == "" || s.firmware == "" {
			return nil, errors.New("先选择用于该运行时的运行时文件和固件")
		}
		_, err := callSandbox(ctx, bin, []string{"--parent-managed", "--recover", "--runtime-home", s.runtimeHome(), "--msb", s.msb, "--firmware", s.firmware}, true, nil)
		return nil, err
	case "reveal":
		if s.result == "" {
			return nil, errors.New("没有可查看的结果目录")
		}
		if st, err := os.Lstat(s.result); err != nil || !st.IsDir() {
			return nil, errors.New("结果目录已不存在")
		}
		return nil, s.app.Browser.OpenFile(s.result)
	case "logs":
		if s.result == "" || s.revision == "" {
			return nil, errors.New("没有可查看的运行日志")
		}
		return callSandbox(ctx, bin, []string{"logs", s.result, s.revision}, false, nil)
	case "preview", "apply":
		if s.result == "" || s.revision == "" || r.Value == "" || len(r.Value) > 512 || strings.ContainsRune(r.Value, 0) {
			return nil, errors.New("请选择审阅清单中的一个文件")
		}
		if r.Action == "apply" {
			if !s.canApply || s.applied[r.Value] || s.previewed != r.Value || !s.previewComplete {
				return nil, errors.New("此结果或文件不能再次写回")
			}
			if !s.confirm(ctx, w, "把这一项写回原项目？", cleanSandboxText(r.Value, 512)+"\n\n仅应用当前审阅版本的文件。请关闭正在编辑它的软件。变更前的原件会保留。", "写回这一项") {
				return nil, ctx.Err()
			}
		}
		data, err := callSandbox(ctx, bin, []string{r.Action, s.result, s.revision, r.Value}, false, nil)
		if r.Action == "preview" {
			s.previewed, s.previewComplete = "", false
			if err == nil {
				var info struct {
					Path      string `json:"path"`
					Binary    bool   `json:"binary"`
					Truncated bool   `json:"truncated"`
				}
				if json.Unmarshal(data, &info) != nil || info.Path != r.Value {
					return nil, errors.New("未能核验所选文件的预览")
				}
				s.previewed, s.previewComplete = info.Path, !info.Binary && !info.Truncated
			}
		} else if err == nil {
			s.applied[r.Value] = true
		}
		return data, err
	default:
		return nil, errors.New("不支持的本机沙盒操作")
	}
	return nil, nil
}

func (s *sandboxHost) runInSandbox(ctx context.Context, w *application.WebviewWindow, r sandboxRequest, bin string) (err error) {
	if !time.Now().Before(s.grant.expires) {
		return errors.New("授权已过期；请选择这次运行需要的文件或目录")
	}
	script := strings.TrimSpace(r.Value)
	if script == "" || len(script) > 8192 || strings.ContainsRune(script, 0) {
		return errors.New("请提供不超过 8 KiB 的 Guest 命令")
	}
	if s.msb == "" || s.firmware == "" {
		return errors.New("请先选择运行时和固件")
	}
	out, err := s.outputDirectory()
	if err != nil {
		return err
	}
	task := s.task
	note := s.grant.path + "\n\n只复制访问范围内的文件。本次授权仅用一次，Guest 不能联网，原件不会自动写回。"
	if task.ID != "" {
		note = "工作台任务：" + cleanSandboxText(task.Title, 256) + "\n\n" + note
	}
	if !r.Offline && s.image == "" {
		note += " 缺少镜像时宿主会匿名下载固定镜像。"
	}
	if !s.confirm(ctx, w, "允许这一次沙盒运行？", note, "授权并运行") {
		return ctx.Err()
	}
	if !time.Now().Before(s.grant.expires) {
		return errors.New("授权已过期；请重新选择")
	}
	grant := s.grant
	s.grant = sandboxGrant{}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var lease string
	outcome, changes := "interrupted", 0
	if task.ID != "" {
		if err := s.hub.dispatch(ctx, sandboxPipeRequest{Action: "claim", Dispatch: task.ID}, &lease); err != nil {
			_ = s.refreshTasks(ctx)
			return err
		}
		s.task = sandboxIntent{}
		s.tasks = nil
		watch := s.monitorTask(runCtx, task.ID, lease, cancel)
		defer func() { cancel(); <-watch; err = errors.Join(err, s.settleTask(task.ID, lease, outcome, changes)) }()
	}
	args := []string{"--parent-managed", "--runtime-home", s.runtimeHome(), "--source", grant.path, "--source-identity", grant.identity, "--output", out, "--msb", s.msb, "--firmware", s.firmware}
	if s.image != "" {
		args = append(args, "--image-archive", s.image)
	} else if r.Offline {
		args = append(args, "--offline")
	}
	args = append(args, "--", "/bin/sh", "-ec", script)
	raw, runErr := callSandbox(runCtx, bin, args, true, func(progress string) { s.send(w, sandboxReply{ID: r.ID, Progress: progress}) })
	if len(raw) > 0 {
		var result struct {
			Directory        string `json:"directory"`
			Cancelled        bool   `json:"cancelled"`
			CleanupConfirmed bool   `json:"cleanup_confirmed"`
		}
		if json.Unmarshal(raw, &result) != nil {
			return errors.New("沙盒未返回有效的运行状态")
		}
		if result.Cancelled && result.CleanupConfirmed {
			outcome = "cancelled"
			return errors.New("已停止运行，并确认 Guest 已清理")
		}
		if filepath.Dir(result.Directory) != out || !strings.HasPrefix(filepath.Base(result.Directory), "sous-result-") {
			return errors.New("沙盒未返回有效的本机审阅目录")
		}
		readCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer done()
		if err := s.loadReview(readCtx, bin, result.Directory); err != nil {
			return errors.Join(runErr, err)
		}
		if runErr == nil {
			var review struct {
				Changes []json.RawMessage `json:"changes"`
			}
			if err := json.Unmarshal(s.review, &review); err != nil {
				return err
			}
			outcome, changes = "ready", len(review.Changes)
		} else {
			outcome = "failed"
		}
	} else if runCtx.Err() == nil {
		outcome = "failed"
	}
	return runErr
}

func (s *sandboxHost) confirm(ctx context.Context, w *application.WebviewWindow, title, note, label string) bool {
	decision := make(chan bool, 1)
	dialog := s.app.Dialog.Warning().AttachToWindow(w).SetTitle(title).SetMessage(note)
	answer := func(v bool) {
		select {
		case decision <- v:
		default:
		}
	}
	dialog.AddButton("取消").SetAsDefault().SetAsCancel().OnClick(func() { answer(false) })
	dialog.AddButton(label).OnClick(func() { answer(true) })
	dialog.Show()
	select {
	case ok := <-decision:
		return ok && ctx.Err() == nil
	case <-ctx.Done():
		return false
	case <-time.After(10 * time.Minute):
		return false
	}
}

func (s *sandboxHost) loadReview(ctx context.Context, bin, dir string) error {
	data, err := callSandbox(ctx, bin, []string{"review", dir}, false, nil)
	if err != nil {
		return err
	}
	var meta struct {
		Revision string `json:"revision"`
		CanApply bool   `json:"can_apply"`
	}
	if json.Unmarshal(data, &meta) != nil || len(meta.Revision) != 64 {
		return errors.New("沙盒审阅格式无效")
	}
	s.result, s.review, s.revision, s.canApply = dir, data, meta.Revision, meta.CanApply
	s.applied = map[string]bool{}
	s.previewed, s.previewComplete = "", false
	return nil
}

type sandboxSetup struct {
	MSB      string `json:"msb"`
	Firmware string `json:"firmware"`
	Image    string `json:"image,omitempty"`
}

func (s *sandboxHost) loadSetup() {
	name := filepath.Join(s.home, "sandbox-runtime.json")
	if st, err := os.Lstat(name); err == nil && st.Mode().IsRegular() && st.Size() <= 4096 && st.Mode().Perm()&0o077 == 0 {
		if raw, err := os.ReadFile(name); err == nil {
			var cfg sandboxSetup
			if json.Unmarshal(raw, &cfg) == nil {
				s.msb, s.firmware, s.image = cfg.MSB, cfg.Firmware, cfg.Image
			}
		}
	}
	if override := os.Getenv("SOUS_SANDBOX_MSB"); override != "" {
		s.msb = override
	}
	if override := os.Getenv("SOUS_SANDBOX_FIRMWARE"); override != "" {
		s.firmware = override
	}
	if override := os.Getenv("SOUS_SANDBOX_IMAGE_ARCHIVE"); override != "" {
		s.image = override
	}
}

func (s *sandboxHost) saveSetup() error {
	// These are hints for the next launch, not project grants. Core verifies
	// runtime digests and the image when used. Never persist a source or task.
	if err := os.MkdirAll(s.home, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.home, ".sandbox-runtime-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	err = json.NewEncoder(tmp).Encode(sandboxSetup{s.msb, s.firmware, s.image})
	if err = errors.Join(err, tmp.Sync(), tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(s.home, "sandbox-runtime.json"))
}

func (s *sandboxHost) runtimeHome() string {
	// The guest's derived Unix sockets must fit even for a deeply nested
	// work/profile home. A private, deterministic directory also lets a
	// restarted desktop reconcile the same VM without reusing another home.
	sum := sha256.Sum256([]byte(s.home))
	return fmt.Sprintf("/private/tmp/sous-vm-%d-%x", os.Getuid(), sum[:6])
}

func (s *sandboxHost) outputDirectory() (string, error) {
	p := filepath.Join(s.home, "sandbox-results")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	st, err := os.Lstat(p)
	if err != nil || !st.IsDir() || st.Mode().Perm()&0o077 != 0 {
		return "", errors.New("结果目录需要是本机私有目录（0700），不能是链接")
	}
	return filepath.EvalSymlinks(p)
}

func sandboxBinary() string {
	if !sandboxSupported() {
		return ""
	}
	bin := os.Getenv("SOUS_SANDBOX_BIN")
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return ""
		}
		bin = filepath.Join(filepath.Dir(exe), "sous-sandbox")
	}
	if !filepath.IsAbs(bin) {
		return ""
	}
	st, err := os.Lstat(bin)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return bin
}

type sandboxOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (b *sandboxOutput) Write(p []byte) (int, error) {
	n := min(len(p), max(0, b.limit-b.Len()))
	_, _ = b.Buffer.Write(p[:n])
	b.overflow = b.overflow || n < len(p)
	return len(p), nil
}

func callSandbox(ctx context.Context, bin string, args []string, managed bool, progress func(string)) (json.RawMessage, error) {
	if !managed {
		deadline, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		ctx = deadline
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 45 * time.Second
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	for _, k := range []string{"HOME", "TMPDIR", "LANG", "LC_ALL"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	if managed {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		defer stdin.Close()
		cmd.Cancel = func() error { _ = stdin.Close(); return cmd.Process.Signal(os.Interrupt) }
	}
	out := &sandboxOutput{limit: 4 << 20}
	cmd.Stdout = out
	logs := &sandboxProgress{update: progress}
	cmd.Stderr = logs
	err := cmd.Run()
	if out.overflow {
		return nil, errors.New("沙盒响应超过大小限制")
	}
	data := bytes.TrimSpace(out.Bytes())
	if len(data) > 0 && !json.Valid(data) {
		data = nil
	}
	if ctx.Err() != nil {
		return data, errors.New("操作已停止；如未确认回收的 Guest，请使用恢复")
	}
	if err != nil {
		msg := logs.last
		if msg == "" {
			msg = err.Error()
		}
		return data, fmt.Errorf("%s", cleanSandboxText(msg, 1200))
	}
	return data, nil
}

type sandboxProgress struct {
	buffer string
	last   string
	update func(string)
}

func (p *sandboxProgress) Write(b []byte) (int, error) {
	p.buffer += string(b)
	for {
		line, rest, ok := strings.Cut(p.buffer, "\n")
		if !ok {
			break
		}
		p.buffer = rest
		if text := cleanSandboxText(line, 1200); text != "" {
			p.last = text
			if p.update != nil {
				p.update(text)
			}
		}
	}
	if len(p.buffer) > 4096 {
		p.buffer = p.buffer[:4096]
	}
	return len(b), nil
}

func cleanSandboxText(s string, n int) string {
	if len(s) > n {
		s = strings.ToValidUTF8(s[:n], "") + "…"
	}
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return '�'
		}
		return r
	}, s))
}
