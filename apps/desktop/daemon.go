package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// The desktop talks to the machine's sous daemon only over the IPC contract
// (docs/api/core-daemon-ipc.md). The daemon is the `sous` binary shipped
// next to this app (or found on PATH); if none answers, the app starts one
// and stops it again on quit.

// Status is the daemon's `status` result (the fields the tray shows).
type Status struct {
	Version      string `json:"version"`
	NodeID       string `json:"node_id"`
	Name         string `json:"name"`
	HubConnected bool   `json:"hub_connected"`
	RelayURL     string `json:"relay_url"`
	PageURL      string `json:"page_url"`
	Brain        string `json:"brain"`
	Runs         []struct {
		RunID string `json:"run_id"`
	} `json:"runs"`
}

// UpdateInfo is the `update.check` result.
type UpdateInfo struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Newer   bool   `json:"newer"`
}

func sousHome() string {
	if h := os.Getenv("SOUS_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sous")
}

// socketPath follows the contract: <home>/daemon.sock, or a hashed name in
// the temp dir when that path is too long for a Unix socket.
func socketPath(home string) string {
	p := filepath.Join(home, "daemon.sock")
	if len(p) <= 100 {
		return p
	}
	sum := sha256.Sum256([]byte(home))
	return filepath.Join(os.TempDir(), "sous-"+hex.EncodeToString(sum[:])[:16]+".sock")
}

// call makes one JSON-RPC call over a fresh connection.
func call(ctx context.Context, home, method string, params, out any) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath(home))
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		req["params"] = params
	}
	raw, _ := json.Marshal(req)
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		return err
	}
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return err
		}
		var res struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &res) != nil || res.ID == nil {
			continue // a notification
		}
		if res.Error != nil {
			return errors.New(res.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(res.Result, out)
	}
}

// sousBinary finds the daemon binary: shipped inside the app first.
func sousBinary() (string, error) {
	name := "sous"
	if runtime.GOOS == "windows" {
		name = "sous.exe"
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".local", "bin", name)
	if _, err := os.Stat(p); err == nil {
		return p, nil
	}
	return "", errors.New("找不到 sous：请重新安装桌面版，或先安装 sous 命令行")
}

// Daemon keeps a daemon running for as long as the app wants one.
type Daemon struct {
	Home string

	mu    sync.Mutex
	child *exec.Cmd // a daemon this app started, if any
}

func (d *Daemon) Status(ctx context.Context) (Status, error) {
	var st Status
	err := call(ctx, d.Home, "status", nil, &st)
	return st, err
}

func (d *Daemon) paired() bool {
	_, err := os.Stat(filepath.Join(d.Home, "node.json"))
	return err == nil
}

// Ensure starts the daemon when none answers and this machine is paired.
func (d *Daemon) Ensure(ctx context.Context) error {
	if _, err := d.Status(ctx); err == nil {
		return nil
	}
	if !d.paired() {
		return errNotPaired
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.child != nil && d.child.ProcessState == nil {
		return nil // starting
	}
	bin, err := sousBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "daemon", "--home", d.Home)
	hideWindow(cmd)
	logf, err := os.OpenFile(filepath.Join(d.Home, "desktop-daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		cmd.Stdout, cmd.Stderr = logf, logf
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 sous daemon 失败：%w", err)
	}
	d.child = cmd
	go func() {
		cmd.Wait()
		if logf != nil {
			logf.Close()
		}
	}()
	return nil
}

// Stop ends the daemon this app started (a service keeps running).
func (d *Daemon) Stop() {
	d.mu.Lock()
	cmd := d.child
	d.child = nil
	d.mu.Unlock()
	if cmd == nil || cmd.Process == nil || cmd.ProcessState != nil {
		return
	}
	stopProcess(cmd.Process)
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
	}
}

// Started reports whether this app runs the daemon (rather than a service).
func (d *Daemon) Started() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.child != nil && d.child.ProcessState == nil
}

// Service runs `sous service <verb>` (install, start, uninstall, ...).
func (d *Daemon) Service(verb string) error {
	bin, err := sousBinary()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "service", verb, "--home", d.Home)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v：%s", err, out)
	}
	return nil
}

var errNotPaired = errors.New("这台电脑还没有配对")
