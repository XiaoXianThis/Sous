# 任务：0.3.0-alpha2 提速、本机解锁、桌面版

0.3.0-alpha1 上线后：页面加载远慢于 0.2.x；Windows 不能用 PIN、Mac 也要手机扫码才能解锁。原因和结论见决策 0033、`docs/architecture/trust.md`、`apps/core/AGENTS.md`。

## 提速

- [x] 快照按内容切块、只传变化的块、每块压缩，中继只保留最新两份清单的块（`blob.keep`）
- [x] 快照在独立 goroutine 里上传，不挡租约续期；写帧超时按大小放宽，取消的请求不拆掉共用连接
- [x] 大的 RPC 回复 gzip（`X-Sous-Accept-Encoding`）
- [x] 打开对话只取 Run 摘要，展开时才取执行过程
- [x] 修 daemon 空转：`linkConn.watch` 把租约信号塞回自己读的通道，占满一个 CPU 核
- [ ] 在慢上行（约 100 KB/s）下实测打开对话的耗时，对比 alpha1

## 本机解锁（决策 0033）

- [x] 每台设备一把本机 Passkey，解锁和关键操作只请求它
- [x] 没有 PRF 的设备（Windows Hello）用设备密钥 + 中继 `devwrap`
- [x] 信任页“让这台设备直接解锁”，解锁按钮按平台写验证方式
- [ ] 真机验收：iPhone Face ID、Android 指纹、Mac 触控 ID / 密码、Windows PIN（含不支持 PRF 的 Windows）

## 桌面版

- [x] Wails v3 托盘程序，带 `sous`，没有 daemon 时自动启动；可安装为后台服务
- [x] IPC `status` 加 `relay_url`、`page_url`、`brain`
- [x] macOS `.app`（自签名）和 Windows zip 的打包工具
- [ ] Windows 真机验收：WebView2 里用 Windows Hello 解锁
- [ ] 桌面版里配对（现在仍要在终端运行配对命令）
- [ ] 发布流程里自动构建桌面版（现在是作者本机构建后上传）

## 发布

- [x] `v0.3.0-alpha2`：tag、CI 构建、离线签名、上传桌面版（macOS arm64、Windows amd64）
- [x] 部署：Vercel 页面、hkvps 中继、本机作为大脑（第一次全量快照 170 秒，之后每次约 0.3 MB）
- [x] CI 的 Windows 审批测试（`unsupported shell`）修好；顺带修了不解析时自定义高风险规则漏掉 argv 命令
- [x] `v0.3.0-alpha3`：带上面的修复，tag、CI 构建、离线签名、上传桌面版
- [ ] 部署 alpha3：hkvps 中继、本机 sous 和桌面版（页面不变，不用重新部署）
