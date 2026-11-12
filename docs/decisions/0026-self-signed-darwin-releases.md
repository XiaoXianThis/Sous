# 0026 发布的 macOS 二进制用自签名证书签名

- Status: Accepted（发布的信任根被 0030 修改：Node 只认离线发布密钥的签名，Apple 证书只用于 TCC）
- Date: 2026-11-09

## Context

0008 决定不做代码签名，发布产物保持 Go 链接器的 ad-hoc 签名。实际用下来，这让 macOS 反复弹文件访问的授权窗：隐私授权（TCC）记的是二进制的 designated requirement，ad-hoc 签名的 DR 是 cdhash，每个新版本的 cdhash 都不同，所以每次 `sous update` 或网页上的「更新」之后，之前点过的「允许」和「完全磁盘访问权限」全部作废。由 LaunchAgent 启动的 daemon 碰到受保护目录时没人点「允许」，任务就停在那里。

本地构建已经用固定的自签名证书「Sous Dev Signing」重签（`apps/core/scripts/sign-darwin.sh`），DR 变成 `identifier "cc.wxian.sous" and certificate leaf = H"…"`，重建前后不变。但发布产物还是 ad-hoc，自更新会把它装回来。

作者仍然没有 Apple 开发者账号。

## Decision

- 发布 workflow 改在 macOS runner 上运行，用同一张「Sous Dev Signing」证书给 darwin 的 `sous` 签名，签名在打包和计算 `checksums.txt` 之前完成。Linux 和 Windows 产物照旧交叉编译，不受影响。
- 证书和私钥以 `.p12` 的形式放在两个仓库 secret 里（`SOUS_SIGN_CERT_P12`、`SOUS_SIGN_CERT_P12_PASSWORD`），由作者在自己的 Mac 上用 `apps/core/scripts/export-signing-cert.sh` 导出后手动填写。workflow 只把它导入运行期间的临时钥匙串。
- workflow 写死证书的 SHA-1，导入后核对；secret 缺失、口令不对或证书不是这一张时发布直接失败，不退回 ad-hoc 签名。
- 目的只是让本地构建、自更新和全新安装的签名身份一致，TCC 授权一次有效。**不是**为了通过 Gatekeeper 或公证：证书不受任何系统信任，别的机器下载后仍会提示「无法验证开发者」，首次安装的做法和 0008 一样。

0008 的「不做代码签名」由这条取代；手动更新、不做公证、不做自动更新的部分不变。

## Alternatives

- 继续 ad-hoc，每次更新后手动重签：容易忘，忘了就会卡任务；全新安装也要重签一次。
- Apple Developer ID 签名加公证：能过 Gatekeeper，但需要付费账号，对目前的使用范围没有必要。以后真要给别人用时再评估。
- 让 `internal/update` 在替换后自己重签：更新流程要依赖本机钥匙串和 `codesign`，而且全新安装的那一份仍然是 ad-hoc。
- 在 Linux runner 上用第三方工具（如 rcodesign）签名：少一次换 runner，但多一个依赖，和本地用的 `codesign` 也不是同一套实现，DR 一致性要另外核对。

## Consequences

- 每个发布的 darwin 二进制都带同一个 DR，授权在更新后仍然有效。换证书等于换身份，要重新导出 secret、改 workflow 里的指纹，所有机器的授权也要重新点一次。
- 私钥离开了本机，存在 GitHub 的仓库 secret 里。拿到它的人可以签出 DR 相同的二进制，骗过已经给 sous 的授权；所以 secret 只给发布 workflow 用，不要在别的 workflow 或 fork 的 PR 里暴露。
- `internal/update` 仍然只核对 `checksums.txt`，不检查签名；0015 里说的更新包供应链问题没有因为这条而解决。
- macOS runner 比 Linux runner 慢，计费分钟数也更高，对每次打 tag 才跑一次的发布可以接受。
