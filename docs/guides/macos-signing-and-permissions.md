# macOS：固定签名身份和文件访问权限

这份指南讲两件事：怎样让 macOS 不再在每次重建 `sous` / `sous-hub` 之后重新要文件访问权限，以及怎样给 `sous` 开「完全磁盘访问权限」。只适用于 macOS。

## 为什么会反复弹窗

- Go 链接器给 darwin 二进制的是 **ad-hoc 签名**：`codesign -dv` 显示 `Identifier=a.out`、`Signature=adhoc`，`codesign -d -r-` 显示 `designated => cdhash H"…"`。
- macOS 的隐私授权（TCC，「系统设置 → 隐私与安全性」里的那些开关）记的就是这条 designated requirement（DR）。DR 绑定 cdhash，而每次编译 cdhash 都会变，系统就把新二进制当成另一个程序，之前点的「允许」作废，再弹一次。
- `go run ./cmd/sous-hub` 每次在临时目录里编出一个新的 `sous-hub`，所以「文件与文件夹」里会攒下好几个同名的 `sous-hub` 条目。
- 项目在 `~/Documents` 这类受保护目录下。由 LaunchAgent 启动的 `sous` 没有终端的「完全磁盘访问权限」，它和它启动的 agent 一碰这些目录就要授权；没人点「允许」时任务就停在那里。

办法是用一个**固定的自签名证书**给二进制签名。这样 DR 变成 `identifier "cc.wxian.sous" and certificate leaf = H"<证书指纹>"`（个别系统版本写成 `certificate root = H"…"`），只要证书不换，重建前后 DR 不变，授权一直有效。

发布到 GitHub Releases 的二进制不签名（见 `docs/decisions/0008-manual-update-unsigned.md`），仍然是 ad-hoc。这里的证书只给本机用。

## 1. 创建签名身份（每台 Mac 一次）

在仓库里执行：

```
wok run core setup-signing
```

不用 wok 时，在 `apps/core/` 里执行 `sh scripts/setup-macos-signing.sh`。

它会：

1. 新建专用钥匙串 `~/Library/Keychains/sous-signing.keychain-db`，口令是随机生成的，不打印，存在登录钥匙串的「sous-signing-keychain」项里（只允许 `/usr/bin/security` 读取），以后签名脚本靠它在重启后解锁。专用钥匙串不会因为睡眠或超时自动上锁。
2. 用系统自带的 `/usr/bin/openssl` 在临时目录生成 RSA 2048 私钥和自签名证书「Sous Dev Signing」：有效期 20 年，`CA:FALSE`，keyUsage 是 `digitalSignature`，extendedKeyUsage 是 `codeSigning`。
3. 把私钥和证书导入专用钥匙串，只允许 `/usr/bin/codesign` 使用私钥（并设置 partition list，让 codesign 签名时不弹窗），然后删掉临时文件。私钥不会留在磁盘的其他地方，也不会进仓库。

成功时输出类似：

```
created "Sous Dev Signing" in /Users/<你>/Library/Keychains/sous-signing.keychain-db
keychain: /Users/<你>/Library/Keychains/sous-signing.keychain-db
identity: "Sous Dev Signing" sha1=<40 位十六进制>
  subject= /CN=Sous Dev Signing
  notBefore=…
  notAfter=…（20 年后）
  extended key usage: Code Signing
```

重复执行不会出错，只会显示已有的身份。`sh scripts/setup-macos-signing.sh status` 只查看不修改。

### 备份和重装机器

重新生成的证书指纹不同，等于换了身份，所有授权要再点一次。想在重装后保留身份，可以在「钥匙串访问」里打开 `sous-signing` 钥匙串，右键「Sous Dev Signing」→「导出」，存成 `.p12` 并设置口令。把它放进密码管理器之类的地方，**不要放进仓库或任何同步到仓库的目录**。

重装后导入（系统会弹窗要 `.p12` 的口令）：

```
sh scripts/setup-macos-signing.sh import <备份的 .p12 路径>
```

## 2. 构建时自动签名

身份建好以后，`apps/core` 的这些任务在 macOS 上会自动签名（`scripts/sign-darwin.sh`）：

| 任务 | 签什么 | 缺身份时 |
|---|---|---|
| `wok build core` | `build/core/sous`、`build/core/sous-hub` | 报错退出（二进制已经编出来，但没签名） |
| `wok dev core` | 先编到 `apps/core/bin/sous-hub`，签名后运行（不再用 `go run`） | 只警告，照常运行 |
| `wok run core release-snapshot` | goreleaser 的 darwin 产物（build hook） | 报错退出 |

identifier 是 `cc.wxian.<文件名>`（`cc.wxian.sous`、`cc.wxian.sous-hub`）。签名是先签一个副本再改名替换，正在运行的进程不受影响。

专用钥匙串平时不在用户的钥匙串搜索列表里（`security list-keychains -d user` 只有登录钥匙串），而 `codesign` 只在搜索列表里找身份，只加 `--keychain` 会报 `no identity found`。所以签名脚本在签名期间把专用钥匙串加到列表末尾，结束后（包括失败和中断）恢复原来的列表。它只改搜索列表，不改钥匙串内容和信任设置。证书不需要设成「始终信任」：`security find-identity` 显示 `CSSMERR_TP_NOT_TRUSTED` 也能签。

用环境变量 `SOUS_SIGN` 调整：`require`（默认）缺身份时报错；`auto` 只警告，保留 ad-hoc 签名；`off` 不签。设置了 `CI`（GitHub Actions 会设）时默认是 `off`，所以 CI 的 macOS 构建和 Linux 上的发布构建都不受影响。`SOUS_SIGN_DRY_RUN=1` 只打印要执行的 `codesign` 命令。

## 3. 给已经安装的 sous 重签

`install-sous.sh`、`sous update` 和网页上的「更新到 vX.Y.Z」装的是 Release 里的二进制，仍然是 ad-hoc 签名。每次安装或更新之后，在 `apps/core/` 里重签，再重启服务：

```
sh scripts/sign-darwin.sh ~/.local/bin/sous
sous service restart
```

`sous` 不在 `~/.local/bin` 时用 `command -v sous` 查它的位置。重启之前，正在运行的 daemon 还是旧的签名。

## 4. 验证签名是否稳定

```
codesign -dv --verbose=4 build/core/sous
codesign -d -r- build/core/sous
```

应该看到：

- `Identifier=cc.wxian.sous`，`Authority=Sous Dev Signing`，`Signature size=…`（不再是 `Signature=adhoc`），`TeamIdentifier=not set`；
- `designated => identifier "cc.wxian.sous" and certificate leaf = H"…"`，**没有** `cdhash H"…"`。

再确认重建之后 DR 不变：把第一次的 `codesign -d -r- build/core/sous` 输出记下来，执行 `wok build core`，再看一次。两次的 `designated =>` 那一行应该一字不差；`codesign -dv` 里的 `CDHash` 会变，这是正常的。

## 5. 给 sous 开「完全磁盘访问权限」

签名之后做一次（以后重建、重签都不用再做，除非换了证书）：

1. 打开「系统设置 → 隐私与安全性 → 完全磁盘访问权限」。
2. 如果列表里已经有旧的 `sous`（ad-hoc 签名时加的），选中它，点「−」删掉；它对应的是旧签名，对新二进制无效。
3. 点列表下面的「+」，按提示输入登录密码或用 Touch ID。
4. 在文件选择窗口按 `⌘⇧G`，输入 `~/.local/bin/sous`（或 `command -v sous` 显示的路径），回车，点「打开」。
5. 确认 `sous` 旁边的开关是打开的。
6. 重启服务：`sous service restart`。

daemon 启动的 agent（claude、codex 等）由 `sous` 负责，访问受保护目录时按 `sous` 的授权算，所以给 `sous` 开了就够了，不用给每个 agent 开。

本地开发跑的 `sous-hub`（`wok dev core`）一般不需要完全磁盘访问权限：第一次访问 `~/Documents` 时点一次「允许」，签名固定后不会再问。

## 6. 清理「文件与文件夹」里重复的 sous-hub

这些条目各自对应一个已经删掉的临时二进制（旧的 cdhash），不会授予新二进制任何权限，留着没有害处。

- 「文件与文件夹」列表没有「−」按钮，不能单独删掉一条。可以把每个旧条目下面的开关关掉，让列表看起来不那么乱。
- 想彻底清掉，只能用系统自带的 `tccutil` 按类别重置。下面的命令会清掉**所有应用**在这几个位置上的授权，之后各个应用第一次访问时会再问一次：

  ```
  tccutil reset SystemPolicyDocumentsFolder
  tccutil reset SystemPolicyDesktopFolder
  tccutil reset SystemPolicyDownloadsFolder
  ```

  只执行出现了 `sous-hub` 的那几类就行。不要直接修改 `TCC.db`，也不要为此关闭 SIP。

清理以后，用签过名的 `sous-hub` 再跑一次，点一次「允许」，列表里就只有一个 `sous-hub`，之后重建也不会再多出新的。

## 故障排查

- **构建报 `cannot sign with "Sous Dev Signing"`**：还没执行第 1 步，或者 `~/Library/Keychains/sous-signing.keychain-db` 被删了。照第 1 步重新创建（证书会变，权限要再授一次）；只想先编出来就加 `SOUS_SIGN=auto`。
- **签名时弹窗「codesign 想要使用钥匙串中的密钥」**：partition list 没设好。重新执行第 1 步的脚本不会修复已有的身份，可以在「钥匙串访问」里打开 `sous-signing` 钥匙串，双击「Sous Dev Signing」的私钥，在「访问控制」里加上 `/usr/bin/codesign`；或者在弹窗里输入专用钥匙串的口令后点「始终允许」，口令用 `security find-generic-password -s sous-signing-keychain -w` 查。
- **codesign 报证书不受信任**：在「钥匙串访问」里双击证书「Sous Dev Signing」，在「信任 → 代码签名」里选「始终信任」（会要求输入登录密码）。
- **升级后又开始弹窗**：Release 里的二进制是 ad-hoc 签名，照第 3 步重签并重启。
