// Sous desktop hosts the bundled local workspace and separate native tools.
// Legacy remote workspaces remain an explicit secondary entry.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed assets/tray.png
var trayIcon []byte

//go:embed all:assets/page
var fallbackPage embed.FS

// version is set by the build (-ldflags "-X main.version=...").
var version = "dev"

// Release packaging can opt into the isolated debug workspace for alphas.
var debugLoginBuild = "off"

// pageURL is the default page when the daemon does not know one yet
// (-ldflags "-X main.pageURL=https://..."; SOUS_PAGE_URL overrides it).
var pageURL = ""

// Passkeys need the page's own origin. WebView2 (Windows) supports WebAuthn
// with Windows Hello, so the page opens in the app's window there. WKWebView
// only allows WebAuthn for apps with an Apple associated domain, and
// WebKitGTK has none, so macOS and Linux open the page in the browser.
func inWindow() bool {
	if v := os.Getenv("SOUS_DESKTOP_WINDOW"); v != "" {
		return v == "1"
	}
	return runtime.GOOS == "windows"
}

type tray struct {
	app     *application.App
	t       *application.SystemTray
	win     *application.WebviewWindow
	winURL  string
	d       *Daemon
	status  *application.MenuItem
	brain   *application.MenuItem
	runs    *application.MenuItem
	update  *application.MenuItem
	svc     *application.MenuItem
	local   *LocalHub
	display *desktopWorkspace
	sandbox *sandboxHost
	last    Status
	ok      bool
}

func main() {
	debug := (version == "dev" || debugLoginBuild == "on") && slices.Contains(os.Args[1:], "--debug-login")
	root := sousHome()
	if debug {
		root = filepath.Join(root, "desktop-debug")
	}
	local := &LocalHub{Home: root, NativeAuth: nativeUnlockSupported || debug, Debug: debug}
	sandbox := &sandboxHost{home: root, hub: local}
	display := &desktopWorkspace{hub: local, debug: debug, openSandbox: sandbox.open}
	app := application.New(application.Options{
		RawMessageHandler: func(w application.Window, message string, origin *application.OriginInfo) {
			display.raw(w, message, origin)
			sandbox.raw(w, message, origin)
		},
		Name:        "sous",
		Description: "sous 桌面版",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(mustSub(fallbackPage, "assets/page")),
		},
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyRegular,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "cc.wxian.sous.desktop",
		},
	})
	sandbox.app, display.app = app, app
	sandbox.openWorkspace = display.open
	tr := &tray{app: app, d: &Daemon{Home: root}, local: local, sandbox: sandbox, display: display}
	tr.build()
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { go tr.openLocal() })
	if runtime.GOOS == "darwin" {
		app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { go tr.openLocal() })
	}
	if sandboxSupported() && slices.Contains(os.Args[1:], "--open-sandbox") {
		app.Event.OnApplicationEvent(events.Mac.ApplicationDidFinishLaunching, func(*application.ApplicationEvent) { sandbox.open() })
	}
	app.OnShutdown(func() {
		sandbox.stop()
		tr.d.Stop()
		if tr.local != nil {
			tr.local.Stop()
		}
	})
	go tr.loop()
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

func (tr *tray) build() {
	tr.t = tr.app.SystemTray.New()
	tr.t.SetIcon(trayIcon)
	tr.t.SetTooltip("sous")
	m := tr.app.NewMenu()
	m.Add("打开 Sous 工作台").OnClick(func(*application.Context) { go tr.openLocal() })
	if !tr.local.Debug {
		m.Add("打开旧版配对工作区").OnClick(func(*application.Context) { tr.openRemote() })
	}
	if sandboxSupported() && sandboxBinary() != "" {
		m.Add("本机沙盒与审阅…").OnClick(func(*application.Context) { tr.sandbox.open() })
	}
	m.AddSeparator()
	tr.status = m.Add("正在连接本机的 sous…").SetEnabled(false)
	tr.brain = m.Add("").SetEnabled(false).SetHidden(true)
	tr.runs = m.Add("").SetEnabled(false).SetHidden(true)
	m.AddSeparator()
	if !tr.local.Debug {
		tr.update = m.Add("检查更新").OnClick(func(*application.Context) { go tr.checkUpdate() })
		tr.svc = m.Add("开机自动运行（安装为后台服务）").OnClick(func(*application.Context) { go tr.installService() })
	}
	m.AddSeparator()
	m.Add(fmt.Sprintf("sous 桌面版 %s", version)).SetEnabled(false)
	m.Add("退出").OnClick(func(*application.Context) { tr.app.Quit() })
	tr.t.SetMenu(m)
	tr.t.OnClick(func() { go tr.openLocal() })
}

func (tr *tray) page() string {
	if v := os.Getenv("SOUS_PAGE_URL"); v != "" {
		return v
	}
	if tr.ok && tr.last.PageURL != "" {
		return tr.last.PageURL
	}
	return pageURL
}

func (tr *tray) openRemote() {
	if inWindow() {
		tr.openWindow()
		return
	}
	if p := tr.page(); p != "" {
		if err := tr.app.Browser.OpenURL(p); err == nil {
			return
		}
	}
	tr.openWindow()
}

func (tr *tray) openLocal() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tr.display.open(ctx); err != nil {
		tr.info("无法打开本机工作区", err.Error())
	}
}

func (tr *tray) openWindow() {
	url := tr.page()
	if url == "" {
		url = "/" // fallback.html: how to pair this machine
	}
	if tr.win == nil {
		tr.win = tr.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:   "main",
			Title:  "sous",
			Width:  1180,
			Height: 780,
			URL:    url,
		})
		// Closing hides the window; the tray keeps the app running.
		tr.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			tr.win.Hide()
			e.Cancel()
		})
		tr.winURL = url
	} else if tr.winURL != url {
		tr.win.SetURL(url)
		tr.winURL = url
	}
	tr.win.Show()
	tr.win.Focus()
}

// loop keeps the daemon up and the menu current.
func (tr *tray) loop() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if !tr.local.Debug && tr.d.paired() {
			err := tr.d.Ensure(ctx)
			st, serr := tr.d.Status(ctx)
			application.InvokeAsync(func() { tr.show(st, serr, err) })
		} else {
			application.InvokeAsync(func() {
				tr.ok, tr.last = false, Status{}
				label := "本机工作区 · 点击打开"
				if tr.local.Page() != "" {
					label = "本机工作区 · 已运行"
				}
				tr.status.SetLabel(label)
				tr.brain.SetHidden(true)
				tr.runs.SetHidden(true)
				tr.t.SetTooltip("sous · 本机工作区")
			})
		}
		cancel()
		time.Sleep(5 * time.Second)
	}
}

func (tr *tray) show(st Status, serr, eerr error) {
	tr.ok, tr.last = serr == nil, st
	switch {
	case eerr == errNotPaired:
		tr.status.SetLabel("这台电脑还没有配对：打开 sous → 设置 → 机器 → 添加机器")
	case serr != nil && eerr != nil:
		tr.status.SetLabel("本机的 sous 没有运行：" + eerr.Error())
	case serr != nil:
		tr.status.SetLabel("正在启动本机的 sous…")
	case st.HubConnected:
		tr.status.SetLabel(fmt.Sprintf("%s · 已连接", st.Name))
	default:
		tr.status.SetLabel(fmt.Sprintf("%s · 等待大脑上线", st.Name))
	}
	switch {
	case serr != nil || st.Brain == "":
		tr.brain.SetHidden(true)
	case st.Brain == "leading":
		tr.brain.SetLabel("本机正在当大脑").SetHidden(false)
	default:
		tr.brain.SetLabel("本机是备用大脑").SetHidden(false)
	}
	if serr == nil && len(st.Runs) > 0 {
		tr.runs.SetLabel(fmt.Sprintf("正在执行 %d 个任务", len(st.Runs))).SetHidden(false)
	} else {
		tr.runs.SetHidden(true)
	}
	tip := "sous"
	if serr == nil {
		tip = "sous · " + st.Name
		if st.Brain == "leading" {
			tip += " · 大脑"
		}
	}
	tr.t.SetTooltip(tip)
}

func (tr *tray) checkUpdate() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var u UpdateInfo
	if err := call(ctx, tr.d.Home, "update.check", nil, &u); err != nil {
		tr.info("检查更新失败", err.Error())
		return
	}
	if !u.Newer {
		tr.info("已是最新版本", "sous "+u.Current)
		return
	}
	application.InvokeAsync(func() {
		q := tr.app.Dialog.Question().SetTitle("有新版本").SetMessage(fmt.Sprintf("sous %s 可以更新到 %s。更新是关键操作：机器会在页面上请你用 Face ID / 指纹 / PIN 确认。现在打开 sous 去更新吗？", u.Current, u.Latest))
		q.AddButton("打开 sous").SetAsDefault().OnClick(func() { tr.openRemote() })
		q.AddButton("以后再说")
		q.Show()
	})
}

func (tr *tray) installService() {
	if !tr.d.paired() {
		tr.info("本机工作区", "本机工作区会随桌面版运行；后台服务安装目前只适用于已配对的执行机器。")
		return
	}
	started := tr.d.Started()
	if err := tr.d.Service("install"); err != nil {
		tr.info("安装后台服务失败", err.Error())
		return
	}
	if started {
		tr.d.Stop() // the service takes over
	}
	if err := tr.d.Service("start"); err != nil {
		tr.info("启动后台服务失败", err.Error())
		return
	}
	tr.info("已安装", "sous 会在开机后自动运行，退出桌面版也不会停止。")
}

func (tr *tray) info(title, msg string) {
	application.InvokeAsync(func() {
		tr.app.Dialog.Info().SetTitle(title).SetMessage(msg).Show()
	})
}
