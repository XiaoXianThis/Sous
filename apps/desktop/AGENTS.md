# desktop

- Purpose: sous 的桌面版：托盘图标 + 打开 sous 页面，并在下面保持本机的 sous daemon（工作机，名册指定时也是大脑）运行。用户只装这一个程序。
- Stack: Go + Wails v3（v3.1.0）
- Depends on: core 的构建产物 `build/core/sous`（打包进应用，不引用 core 源码）；契约 `docs/api/core-daemon-ipc.md`。页面就是 web 部署在 Vercel 上的那一份，不内嵌 web 的构建产物（Passkey 绑定页面域名）。

## Tasks

Commands run in `apps/desktop/`. Leave a command empty to use what `wok` detects; `wok ls desktop` shows the result.

| task | command | windows |
|---|---|---|
| install | go mod download | |
| dev | go run . | |
| test | go vet ./... | |
| build | go run ./tools/package -goos darwin -sous "$WOK_ROOT/build/core/sous" -out "$WOK_OUT" | go run ./tools/package -goos windows -sous "$WOK_ROOT/build/core/sous.exe" -out "$WOK_OUT" |
| build-windows | go run ./tools/package -goos windows -goarch amd64 -sous "$WOK_ROOT/build/core/windows-amd64/sous.exe" -out "$WOK_OUT" | |

## Conventions

- 和 daemon 只通过本地 IPC 契约通信（`daemon.go`，Unix socket 上的按行 JSON-RPC），不 import core 的包。
- `sous` 二进制找法：应用旁边（macOS 是 `sous.app/Contents/MacOS/sous`，Windows 是同目录的 `sous.exe`）→ PATH → `~/.local/bin/sous`。没有 daemon 应答、而且这台电脑已经配对（`~/.sous/node.json` 存在）时，由桌面版启动 `sous daemon`，退出时停掉它；用户在菜单里选“开机自动运行”后改由 `sous service` 管理，退出桌面版不影响它。
- 页面地址：daemon `status` 的 `page_url`（名册里的第一个页面地址）→ 构建时 `-X main.pageURL`（`SOUS_PAGE_URL`）→ 内嵌的 `assets/page/index.html`（提示先配对）。
- Passkey 需要页面自己的域名：Windows 的 WebView2 支持 WebAuthn（Windows Hello），在应用窗口里打开；macOS 的 WKWebView 只给有 Apple 关联域名的应用用 WebAuthn，自签名做不到，所以 macOS（以及 Linux）用默认浏览器打开，菜单里留一个“在应用窗口中打开（实验）”。`SOUS_DESKTOP_WINDOW=1/0` 可以强制。
- macOS 打包（`tools/package`）：`sous.app` 是 `LSUIElement`（只有托盘，没有 Dock 图标），用和 core 相同的自签名身份「Sous Dev Signing」签名，标识 `cc.wxian.sous.desktop`；里面的 `sous` 直接复制 core 已签名的产物，保持 `cc.wxian.sous`，不改变它的隐私授权。
- Windows 打包是 zip（`sous/sous-desktop.exe` + `sous/sous.exe`），`-H windowsgui` 不弹控制台；`sous.exe` 用 core 的交叉编译产物（先 `wok run core build-windows`）。
