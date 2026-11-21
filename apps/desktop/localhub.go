package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const localOrigin = "http://localhost:8787"

// LocalHub owns a relay-free workspace. The control pipes carry a single
// readiness record and give the Hub a lifetime tied to the desktop process.
type LocalHub struct {
	Home       string
	NativeAuth bool
	Debug      bool
	mu         sync.Mutex
	stop       io.WriteCloser
	proc       *os.Process
	done       chan struct{}
	page       string
	pipe       *sandboxPipe
}

func localWebDir() (string, error) {
	if dir := os.Getenv("SOUS_WEB_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(exe)
	if runtime.GOOS == "darwin" {
		dir = filepath.Join(dir, "..", "Resources")
	}
	return filepath.Join(dir, "web"), nil
}

func (h *LocalHub) Ensure(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	bin, err := sousBinary()
	if err != nil {
		return err
	}
	dir, err := localWebDir()
	if err != nil {
		return err
	}
	if st, err := os.Stat(filepath.Join(dir, "index.html")); err != nil || !st.Mode().IsRegular() {
		return errors.New("没有找到随桌面版提供的工作台，请重新安装或设置 SOUS_WEB_DIR")
	}
	agent := "legacy"
	if h.Debug {
		agent = "none"
	}
	args := []string{"hub", "--home", h.Home, "--web-dir", dir, "--agent", agent, "--app-managed", "--desktop-agent-switch"}
	if h.NativeAuth {
		args = append(args, "--desktop-native-auth")
	}
	enableSandbox := sandboxBinary() != ""
	if enableSandbox {
		args = append(args, "--desktop-sandbox-dispatch")
	}
	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr
	hideWindow(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return err
	}
	type handshake struct {
		page string
		pipe *sandboxPipe
		err  error
	}
	ready := make(chan handshake, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 8192), 1<<20)
		if !scanner.Scan() {
			ready <- handshake{err: errors.New("本机 Hub 未返回启动记录")}
			_ = stdin.Close()
			_ = cmd.Wait()
			return
		}
		line := scanner.Bytes()
		page, err := localPage(line)
		var protocol struct {
			Version     int `json:"sandbox_dispatch"`
			DesktopAuth int `json:"desktop_auth"`
		}
		if err == nil {
			err = json.Unmarshal(line, &protocol)
		}
		if err == nil && (enableSandbox && protocol.Version != 1 || !enableSandbox && protocol.Version != 0) {
			err = errors.New("本机 Hub 的沙盒派发协议不匹配")
		}
		if err == nil && (h.NativeAuth && protocol.DesktopAuth != 1 || !h.NativeAuth && protocol.DesktopAuth != 0) {
			err = errors.New("本机 Hub 的桌面身份协议不匹配，请重新安装完整应用")
		}
		var pipe *sandboxPipe
		if err == nil && (enableSandbox || h.NativeAuth) {
			pipe = newSandboxPipe(stdin)
		}
		ready <- handshake{page: page, pipe: pipe, err: err}
		if pipe != nil {
			pipe.pump(scanner)
		} else {
			_, _ = io.Copy(io.Discard, stdout)
		}
		_ = cmd.Wait()
	}()
	select {
	case r := <-ready:
		if r.err == nil {
			h.stop, h.proc, h.done, h.page, h.pipe = stdin, cmd.Process, done, r.page, r.pipe
			return nil
		}
		err = r.err
	case <-ctx.Done():
		err = ctx.Err()
	}
	_ = stdin.Close()
	_ = cmd.Process.Kill()
	<-done
	return fmt.Errorf("启动本机工作区失败（请检查 8787 端口和桌面版日志）：%w", err)
}

func localPage(line []byte) (string, error) {
	var ready struct {
		Origin   string `json:"origin"`
		SetupURL string `json:"setup_url"`
	}
	if err := json.Unmarshal(line, &ready); err != nil {
		return "", err
	}
	if ready.Origin != localOrigin {
		return "", errors.New("本机 Hub 返回了不匹配的地址")
	}
	if ready.SetupURL == "" {
		return ready.Origin, nil
	}
	u, err := url.Parse(ready.SetupURL)
	if err != nil || u.Scheme+"://"+u.Host != ready.Origin || u.Path != "/setup" || u.RawQuery != "" || u.User != nil {
		return "", errors.New("本机 Hub 返回了无效的注册链接")
	}
	fragment, err := url.ParseQuery(u.Fragment)
	if err != nil || len(fragment) != 1 || len(fragment["token"]) != 1 || fragment.Get("token") == "" {
		return "", errors.New("本机 Hub 返回的注册链接缺少一次性令牌")
	}
	return ready.SetupURL, nil
}

func (h *LocalHub) running() bool {
	if h.done == nil {
		return false
	}
	select {
	case <-h.done:
		return false
	default:
		return true
	}
}

func (h *LocalHub) Page() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.running() {
		return ""
	}
	return h.page
}

func (h *LocalHub) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stop == nil {
		return
	}
	_ = h.stop.Close()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		_ = h.proc.Kill()
		<-h.done
	}
	h.stop, h.proc, h.done, h.page, h.pipe = nil, nil, nil, "", nil
}
