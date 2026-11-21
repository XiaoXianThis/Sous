package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const desktopPage = localOrigin + "/desktop"
const desktopPrefix = "sous.desktop:"
const debugCodeTTL = 2 * time.Minute

type desktopRequest struct {
	ID     uint32 `json:"id"`
	Action string `json:"action"`
	Code   string `json:"code,omitempty"`
}

type desktopReply struct {
	ID      uint32 `json:"id"`
	Debug   bool   `json:"debug,omitempty"`
	Sandbox bool   `json:"sandbox,omitempty"`
	Grant   string `json:"grant,omitempty"`
	Error   string `json:"error,omitempty"`
}

type debugCode struct {
	value string
	until time.Time
	tries int
}

func (c *debugCode) issue(now time.Time) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(100_000_000))
	if err != nil {
		return "", err
	}
	*c = debugCode{fmt.Sprintf("%08d", n.Int64()), now.Add(debugCodeTTL), 5}
	return c.value, nil
}

func (c *debugCode) verify(value string, now time.Time) bool {
	if c.tries == 0 || !now.Before(c.until) {
		*c = debugCode{}
		return false
	}
	c.tries--
	if subtle.ConstantTimeCompare([]byte(c.value), []byte(value)) != 1 {
		return false
	}
	*c = debugCode{}
	return true
}

type desktopWorkspace struct {
	app         *application.App
	hub         *LocalHub
	debug       bool
	openSandbox func()
	mu          sync.Mutex
	window      *application.WebviewWindow
	busy        bool
	code        debugCode // touched only inside the serialized auth operation
}

func (d *desktopWorkspace) open(ctx context.Context) error {
	if err := d.hub.Ensure(ctx); err != nil {
		return err
	}
	page := d.hub.Page()
	if page == "" {
		return errors.New("本机工作区已关闭，请重试")
	}
	if d.hub.NativeAuth {
		page = desktopPage
	}
	application.InvokeSync(func() {
		d.mu.Lock()
		w := d.window
		if w == nil {
			title := "Sous"
			if d.debug {
				title = "Sous · 调试工作区"
			}
			w = d.app.Window.NewWithOptions(application.WebviewWindowOptions{
				Name: "local-workspace", Title: title, URL: page, Width: 1180, Height: 780, MinWidth: 680, MinHeight: 480,
			})
			w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) { w.Hide(); e.Cancel() })
			d.window = w
		}
		d.mu.Unlock()
		w.Show()
		w.Focus()
	})
	return nil
}

func desktopFrame(origin *application.OriginInfo) bool {
	if origin == nil || !origin.IsMainFrame {
		return false
	}
	check := func(source string) bool {
		u, err := url.Parse(source)
		return err == nil && u.Scheme == "http" && u.Host == "localhost:8787" && u.User == nil && u.EscapedPath() == "/desktop" && u.RawQuery == "" && !u.ForceQuery
	}
	return check(origin.Origin) && (origin.TopOrigin == "" || check(origin.TopOrigin))
}

func (d *desktopWorkspace) raw(w application.Window, message string, origin *application.OriginInfo) {
	if !d.hub.NativeAuth || !strings.HasPrefix(message, desktopPrefix) || len(message) > 240 || !desktopFrame(origin) {
		return
	}
	var r desktopRequest
	if json.Unmarshal([]byte(strings.TrimPrefix(message, desktopPrefix)), &r) != nil || r.ID == 0 {
		return
	}
	d.mu.Lock()
	if d.window == nil || w != d.window {
		d.mu.Unlock()
		return
	}
	target := d.window
	reply := desktopReply{ID: r.ID}
	if r.Action == "status" && r.Code == "" {
		d.mu.Unlock()
		reply.Debug = d.debug
		reply.Sandbox = sandboxSupported() && sandboxBinary() != ""
		d.send(target, reply)
		return
	}
	if d.busy {
		d.mu.Unlock()
		reply.Error = "请先完成或取消当前身份验证。"
		d.send(target, reply)
		return
	}
	d.busy = true
	d.mu.Unlock()
	go func() {
		if err := d.handle(target, r, &reply); err != nil {
			reply.Error = err.Error()
		}
		d.mu.Lock()
		d.busy = false
		d.mu.Unlock()
		d.send(target, reply)
	}()
}

func (d *desktopWorkspace) handle(w *application.WebviewWindow, r desktopRequest, reply *desktopReply) error {
	mode := "native"
	switch {
	case r.Action == "open-sandbox" && r.Code == "" && sandboxSupported() && sandboxBinary() != "":
		application.InvokeAsync(d.openSandbox)
		return nil
	case d.debug && r.Action == "show-code" && r.Code == "":
		value, err := d.code.issue(time.Now())
		if err != nil {
			return errors.New("无法生成验证码，请重试。")
		}
		application.InvokeAsync(func() {
			d.app.Dialog.Info().AttachToWindow(w).SetTitle("Sous 调试验证码").
				SetMessage(value + "\n\n2 分钟内有效，只能使用一次。关闭此提示后，在 Sous 窗口输入验证码。").Show()
		})
		return nil
	case d.debug && r.Action == "unlock-debug":
		if !d.code.verify(r.Code, time.Now()) {
			return errors.New("验证码不正确或已失效。请重试，或重新显示验证码。")
		}
		mode = "debug"
	case !d.debug && r.Action == "unlock" && r.Code == "" && nativeUnlockSupported:
		if !authenticateDeviceOwner() {
			return errors.New("没有完成身份验证。可以重试，或在系统提示中使用登录密码。")
		}
	default:
		return errors.New("此窗口不支持该操作。")
	}
	// The grant can be redeemed only on the managed Hub instance which
	// issued it. Do not place it in a URL, environment variable, or log.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.hub.dispatch(ctx, sandboxPipeRequest{Action: "desktop-login", Outcome: mode}, &reply.Grant); err != nil {
		return errors.New("本机工作区已断开。请重新打开 Sous 工作台。")
	}
	return nil
}

func (d *desktopWorkspace) send(w *application.WebviewWindow, reply desktopReply) {
	data, err := json.Marshal(reply)
	if err != nil {
		return
	}
	application.InvokeAsync(func() {
		w.ExecJS(`if (location.origin === "` + localOrigin + `" && location.pathname === '/desktop' && !location.search) window.dispatchEvent(new CustomEvent('sous-desktop-reply', { detail: ` + string(data) + ` }));`)
	})
}
