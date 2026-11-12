// sous desktop: a tray icon and the sous page in one app, with the
// machine's sous daemon (worker, and brain when the roster says so) kept
// running underneath. The page is the same one the browser shows (apps/web
// on Vercel); the app only adds the tray, the window and the daemon.
package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"runtime"
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
	app    *application.App
	t      *application.SystemTray
	win    *application.WebviewWindow
	winURL string
	d      *Daemon
	status *application.MenuItem
	brain  *application.MenuItem
	runs   *application.MenuItem
	update *application.MenuItem
	svc    *application.MenuItem
	last   Status
	ok     bool
}

func main() {
	app := application.New(application.Options{
		Name:        "sous",
		Description: "sous 桌面版",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(mustSub(fallbackPage, "assets/page")),
		},
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "cc.wxian.sous.desktop",
		},
	})
	tr := &tray{app: app, d: &Daemon{Home: sousHome()}}
	tr.build()
	app.OnShutdown(tr.d.Stop)
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
	m.Add("打开 sous").OnClick(func(*application.Context) { tr.open() })
	m.AddSeparator()
	tr.status = m.Add("正在连接本机的 sous…").SetEnabled(false)
	tr.brain = m.Add("").SetEnabled(false).SetHidden(true)
	tr.runs = m.Add("").SetEnabled(false).SetHidden(true)
	m.AddSeparator()
	tr.update = m.Add("检查更新").OnClick(func(*application.Context) { go tr.checkUpdate() })
	tr.svc = m.Add("开机自动运行（安装为后台服务）").OnClick(func(*application.Context) { go tr.installService() })
	if !inWindow() {
		m.Add("在应用窗口中打开（实验）").OnClick(func(*application.Context) { tr.openWindow() })
	}
	m.AddSeparator()
	m.Add(fmt.Sprintf("sous 桌面版 %s", version)).SetEnabled(false)
	m.Add("退出").OnClick(func(*application.Context) { tr.app.Quit() })
	tr.t.SetMenu(m)
	tr.t.OnClick(func() { tr.open() })
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

func (tr *tray) open() {
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
	} else if tr.winURL == "/" && url != "/" {
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
		err := tr.d.Ensure(ctx)
		st, serr := tr.d.Status(ctx)
		cancel()
		application.InvokeAsync(func() { tr.show(st, serr, err) })
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
		q.AddButton("打开 sous").SetAsDefault().OnClick(func() { tr.open() })
		q.AddButton("以后再说")
		q.Show()
	})
}

func (tr *tray) installService() {
	if !tr.d.paired() {
		tr.info("还不能安装", errNotPaired.Error())
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
