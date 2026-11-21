# desktop

- Purpose: sous 的桌面版：独立本机工作台窗口 + 托盘 + 单独的沙盒审阅工具，分别持有独立 Hub 或旧 daemon 的生命周期。用户只装这一个程序。
- Stack: Go + Wails v3（v3.1.0）
- Depends on: core 的构建产物 `build/core/sous`（打包进应用，不引用 core 源码）；契约 `docs/api/core-daemon-ipc.md`、`docs/api/core-hub-http.md`；Apple Silicon 的独立开发包可带 `build/core/sous-sandbox`，契约 `docs/api/core-sandbox-cli.md`、`docs/api/core-desktop-sandbox-dispatch.md`；随包带 `build/web`，本机 Hub 同源提供该页，配对模式仍打开原来的远程页面。

## Tasks

Commands run in `apps/desktop/`. Leave a command empty to use what `wok` detects; `wok ls desktop` shows the result.

| task | command | windows |
|---|---|---|
| install | go mod download | |
| dev | go run . | |
| test | go test ./... | |
| build | go run ./tools/package -goos darwin -sous "$WOK_ROOT/build/core/sous" -web-dir "$WOK_ROOT/build/web" -out "$WOK_OUT" | go run ./tools/package -goos windows -sous "$WOK_ROOT/build/core/sous.exe" -web-dir "$WOK_ROOT/build/web" -out "$WOK_OUT" |
| build-sandbox | go run ./tools/package -goos darwin -goarch arm64 -sous "$WOK_ROOT/build/core/sous" -sandbox "$WOK_ROOT/build/core/sous-sandbox" -web-dir "$WOK_ROOT/build/web" -out "$WOK_OUT/sandbox-preview" | |
| build-windows | go run ./tools/package -goos windows -goarch amd64 -sous "$WOK_ROOT/build/core/windows-amd64/sous.exe" -web-dir "$WOK_ROOT/build/web" -out "$WOK_OUT" | |

## Conventions

- 打包先构建 core 和 web；macOS 不修改源资源，直接把 `build/web` 放进 `Contents/Resources/web`；Windows 放入 zip 的 `sous/web/`。`SOUS_WEB_DIR` 可设开发用产物。托盘的「打开本机工作区」通过 `sous hub --app-managed --desktop-agent-switch` 拉起独立 Hub，固定回环端口，读取私有管道的启动记录，在应用的 WebView 载入包内工作台；关闭管道关闭所持有的进程。macOS 通过系统 LocalAuthentication 验证，由继承管道领取一次性登录授权。`dev` 和打包时显式允许的 Alpha 构建用 `--debug-login` 启动隔离的 `SOUS_HOME/desktop-debug`，不走生物识别。旧配对仍有独立菜单。

- 沙盒开发包：先 `wok run core build-sandbox`，再 `wok run desktop build-sandbox`。托盘入口打开独立本机 WKWebView，桥只接受该窗口的内置主页面。路径由系统选择器授权，`run` 与 `apply` 需通过原生确认；不向 WebUI 或远程页面开放宿主 shell、授权、审阅接口。工作台请求仅从其所持本机 Hub 的私有进程管道领取，任务说明不会当作命令。
- 和 daemon 只通过本地 IPC 契约通信（`daemon.go`，Unix socket 上的按行 JSON-RPC），不 import core 的包。
- `sous` 二进制找法：应用旁边（macOS 是 `sous.app/Contents/MacOS/sous`，Windows 是同目录的 `sous.exe`）→ PATH → `~/.local/bin/sous`。没有 daemon 应答、而且这台电脑已经配对（`~/.sous/node.json` 存在）时，由桌面版启动 `sous daemon`，退出时停掉它；用户在菜单里选“开机自动运行”后改由 `sous service` 管理，退出桌面版不影响它。
- 页面地址：daemon `status` 的 `page_url`（名册里的第一个页面地址）→ 构建时 `-X main.pageURL`（`SOUS_PAGE_URL`）→ 内嵌的 `assets/page/index.html`（提示先配对）。
- 本机工作台不调用系统浏览器。macOS 正常模式用系统身份，其他平台保留内置窗口的 Passkey 路径；`SOUS_DESKTOP_WINDOW=1/0` 仅影响旧配对入口。
- macOS 打包（`tools/package`）：`sous.app` 有 Dock 图标和主窗口，同时保留托盘，用和 core 相同的自签名身份「Sous Dev Signing」签名，标识 `cc.wxian.sous.desktop`；里面的 `sous` 直接复制 core 已签名的产物，保持 `cc.wxian.sous`，不改变它的隐私授权。
- Windows 打包是 zip（`sous/sous-desktop.exe` + `sous/sous.exe`），`-H windowsgui` 不弹控制台；`sous.exe` 用 core 的交叉编译产物（先 `wok run core build-windows`）。

- 标签发行的 macOS arm64 / Windows amd64 压缩包由 `.github/workflows/release.yml` 构建。Apple Silicon 包含独立沙盒运行器（不含 Microsandbox 上游运行时或镜像）；发行打包使用指定钥匙串和 `SOUS_SIGN=require`，不得降到 ad-hoc。两份 ZIP 都纳入发行 `checksums.txt`。
