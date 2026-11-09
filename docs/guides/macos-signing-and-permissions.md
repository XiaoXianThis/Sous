# macOS：固定签名身份和文件访问权限

这份指南讲三件事：怎样让 macOS 不再在每次重建或更新 `sous` / `sous-hub` 之后重新要文件访问权限，怎样让发布产物也用同一个签名身份，以及怎样给 `sous` 开「完全磁盘访问权限」。只适用于 macOS。

## 为什么会反复弹窗

- Go 链接器给 darwin 二进制的是 **ad-hoc 签名**：`codesign -dv` 显示 `Identifier=a.out`、`Signature=adhoc`，`codesign -d -r-` 显示 `designated => cdhash H"…"`。
- macOS 的隐私授权（TCC，「系统设置 → 隐私与安全性」里的那些开关）记的就是这条 designated requirement（DR）。DR 绑定 cdhash，而每次编译 cdhash 都会变，系统就把新二进制当成另一个程序，之前点的「允许」作废，再弹一次。
- `go run ./cmd/sous-hub` 每次在临时目录里编出一个新的 `sous-hub`，所以「文件与文件夹」里会攒下好几个同名的 `sous-hub` 条目。
- 项目在 `~/Documents` 这类受保护目录下。由 LaunchAgent 启动的 `sous` 没有终端的「完全磁盘访问权限」，它和它启动的 agent 一碰这些目录就要授权；没人点「允许」时任务就停在那里。

办法是用一个**固定的自签名证书**给二进制签名。这样 DR 变成 `identifier "cc.wxian.sous" and certificate leaf = H"<证书指纹>"`（个别系统版本写成 `certificate root = H"…"`），只要证书不换，重建前后 DR 不变，授权一直有效。

发布到 GitHub Releases 的 darwin 二进制也用同一张证书签名（第 6 节，决策 0026），所以本地构建、`sous update` 和全新安装的签名身份完全一样。这张证书是自签名的，只为了让授权稳定，**不能**通过 Gatekeeper 或公证：别的机器下载后仍会提示「无法验证开发者」。

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

用环境变量 `SOUS_SIGN` 调整：`require`（默认）缺身份时报错；`auto` 只警告，保留 ad-hoc 签名；`off` 不签。设置了 `CI`（GitHub Actions 会设）时默认是 `off`；发布 workflow 明确设了 `SOUS_SIGN=require`（第 6 节）。`SOUS_SIGN_DRY_RUN=1` 只打印要执行的 `codesign` 命令。

## 3. 给已经安装的 sous 重签

`install-sous.sh`、`sous update` 和网页上的「更新到 vX.Y.Z」装的是 Release 里的二进制。从发布产物开始签名的那个版本起（第 6 节），它们已经带着「Sous Dev Signing」的签名，不用再重签。

更早的版本是 ad-hoc 签名。装的是这类版本时，在 `apps/core/` 里重签，再重启服务：

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

## 6. 发布产物签名

打 `v*` tag 后，`.github/workflows/release.yml` 在 macOS runner 上运行，用同一张「Sous Dev Signing」给 darwin 的 `sous` 签名，再打包、算 `checksums.txt`、发布：

1. 第一步「Import the macOS signing identity」从两个仓库 secret 取出证书，导入 `$RUNNER_TEMP` 里的临时钥匙串（随机口令、设置 partition list、加进搜索列表），并核对证书的 SHA-1 等于 workflow 里写的 `SOUS_SIGN_CERT_SHA1`（`970B643A535B0E5B736FD011DFC11AF8896BCE72`，就是本机这张证书的指纹，公开信息）。secret 缺失、口令不对、证书不是这一张，都会直接让发布失败并说明原因，**不会**退回 ad-hoc 签名。
2. goreleaser 那一步设 `SOUS_SIGN=require`，`SOUS_SIGN_KEYCHAIN` 指向临时钥匙串；build hook（`scripts/sign-darwin.sh`）在打包之前签名，所以 `checksums.txt` 算的是签过名的压缩包。
3. 「Check the darwin signatures」确认两个 darwin 二进制的 designated requirement 是 `identifier "cc.wxian.sous" and certificate leaf = H"<上面的指纹>"`。
4. 最后删掉临时钥匙串。

### 需要的两个 secret

| secret | 内容 |
|---|---|
| `SOUS_SIGN_CERT_P12` | 「Sous Dev Signing」的证书和私钥，`.p12` 格式，base64 编码成一行 |
| `SOUS_SIGN_CERT_P12_PASSWORD` | 这个 `.p12` 的口令 |

在建过身份的那台 Mac 上，进 `apps/core/`，用 `scripts/export-signing-cert.sh` 导出。口令从环境变量读，脚本不会打印它；`.p12` 只在 `$TMPDIR` 下的私有临时目录里存在，退出时删除，不会进仓库。标准输出只有一行 base64，提示和证书指纹打在标准错误上。

用 GitHub CLI（`gh auth login` 过、当前目录在这个仓库里）：

```
export SOUS_SIGN_CERT_P12_PASSWORD="$(openssl rand -hex 24)"
sh scripts/export-signing-cert.sh | gh secret set SOUS_SIGN_CERT_P12
printf '%s' "$SOUS_SIGN_CERT_P12_PASSWORD" | gh secret set SOUS_SIGN_CERT_P12_PASSWORD
```

不用 CLI 时：在同一个终端里执行 `sh scripts/export-signing-cert.sh | pbcopy`，在 GitHub 仓库的「Settings → Secrets and variables → Actions → New repository secret」里新建 `SOUS_SIGN_CERT_P12`，粘贴；再执行 `printf '%s' "$SOUS_SIGN_CERT_P12_PASSWORD" | pbcopy`，新建 `SOUS_SIGN_CERT_P12_PASSWORD`，粘贴。两个 secret 必须对应同一个口令。

导出私钥时 macOS 可能弹窗要「sous-signing」钥匙串的口令，用 `security find-generic-password -s sous-signing-keychain -w` 查。脚本会核对导出的文件只含一张名为「Sous Dev Signing」的证书，并在标准错误上打印它的 SHA-1，应该和 workflow 里的 `SOUS_SIGN_CERT_SHA1` 一致。

换了证书（重新执行第 1 步生成了新身份）时，要重新导出、更新两个 secret，并把 workflow 里的 `SOUS_SIGN_CERT_SHA1` 改成新指纹；所有机器上的授权也要再点一次。

### 对其他系统没有影响

签名只发生在 darwin 二进制上：`sign-darwin.sh` 跳过不是 Mach-O 的文件，Linux 和 Windows 的 `sous`、`sous-hub` 仍然是 `CGO_ENABLED=0` 交叉编译出来的，和在 Linux runner 上编出来的一样（同一提交下，开不开签名，这些二进制逐字节相同）。压缩包的名字、格式和 `checksums.txt` 的写法都没变，`internal/update` 照常按名字找安装包、核对校验和。

### 发版后验证

```
gh release download vX.Y.Z -p 'sous_*_darwin_arm64.tar.gz' -p checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
tar -xzf sous_*_darwin_arm64.tar.gz
codesign -d -r- sous
```

应该看到 `designated => identifier "cc.wxian.sous" and certificate leaf = H"970b643a535b0e5b736fd011dfc11af8896bce72"`。再在 Mac 上 `sous update`（或网页上的「更新」）到这个版本，重启服务后不应再弹文件访问的窗口。

## 7. 清理「文件与文件夹」里重复的 sous-hub

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
- **升级后又开始弹窗**：先用 `codesign -d -r- "$(command -v sous)"` 看签名。是 `cdhash H"…"` 就说明装的是还没签名的旧版本，照第 3 步重签并重启；是证书身份但指纹和本机的不同，说明 secret 里的证书换过，按第 6 节重新导出。
- **发布失败，提示 `SOUS_SIGN_CERT_P12` 或 `SOUS_SIGN_CERT_P12_PASSWORD` 缺失、导入失败或证书不对**：按第 6 节重新导出并更新两个 secret，再删掉 tag 重新推送，或者打一个新 tag。
