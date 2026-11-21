# 本机工作区浏览器验收

先在仓库根目录运行 `wok build core web`。在本目录运行 `bun install`，准备 Chromium 后执行 `bun run test`。可用 `SOUS_TEST_CHROMIUM` 指定 Chromium 可执行文件；默认使用 Playwright 的 Chromium（可在本目录运行 `bunx playwright-core install chromium`）。

套件自行启动 `build/core/sous` 和 `build/web`，不使用真实工作区。`SOUS_CORE_BIN`、`SOUS_WEB_BUILD` 可覆盖构建产物。真实浏览器用虚拟认证器执行 WebAuthn，不访问 Passkey 系统存储；测试退出时停止 Hub、回收临时目录。此测试不要求云账号、Relay、模型或沙盒运行时。

默认验收无主管装配。设置 `SOUS_TEST_LEGACY=1` 可以用本地 mock 模型复验兼容主管和流式回复；该模式也不请求外部模型服务。

设置 `SOUS_TEST_ACP=1` 走第一方 ACP 主管。测试启动独立的本机 SSE 模型夹具（不是生产推理），但实际连到 ACP 子进程与 MCP stdio 工具桥，验证流式回复、工具作用域、草稿落库和去重、取消、切回无主管后历史仍可读。

可用 `SOUS_TEST_ACP_MODEL_CONFIG` 指向一份测试专用的 ACP 模型 YAML，替换 SSE 模型夹具，验证实际推理、流式响应、草稿和重启保留。应选择本机已启动的模型服务；测试仅发送合成内容。故意越权和取消模拟仍以离线夹具模式为准。

## 真 Microsandbox 派发

在 Apple Silicon 上先构建 Core、Web 和 `wok run core build-sandbox`。设置 `MSB_PATH`、`MSB_LIBKRUNFW_PATH`、`SOUS_TEST_MSBOX_IMAGE_ARCHIVE` 分别指向经核验的运行时、固件和 `sous-sandbox download-image` 产生的离线归档。运行 `bun run test:sandbox`。它在真实 Chromium 中排队，在测试进程模拟 Desktop 接管私有管道，再实际运行及取消离线 Guest。它不是对原生窗口的验收。

## 打包 Desktop + Chromium 半自动验收

1. 运行 `wok run desktop build-sandbox`。只对本机隔离测试使用该开发包。启动包内 Desktop 进程前设置 `SOUS_HOME` 为新空测试目录，并用 `SOUS_SANDBOX_MSB`、`SOUS_SANDBOX_FIRMWARE`、`SOUS_SANDBOX_IMAGE_ARCHIVE` 设置已核验的同一组夹具。不连接生产工作区。
2. 这个兼容性用例刻意保留独立的 Chromium 控制端：在启动 Desktop 前先用包内 `sous hub --home <同一测试目录> --web-dir <包内 WebUI>` 初始化一次，把终端的一次性同源注册链接仅存进权限为 `0600` 的临时文件，然后退出 CLI Hub。正常启动 Desktop（不加 `--debug-login`），由它启动同一隔离工作区。准备只含 `note.txt`（内容为 `untouched original` 加换行）的合成项目。内置登录另用真实桌面窗口验收。
3. 用 `SOUS_TEST_DESKTOP_SETUP_FILE` 和 `SOUS_TEST_DESKTOP_PROJECT` 指向这两个测试夹具，运行 `bun run test:desktop-sandbox`。跟随输出操作原生选择、运行确认、内容审阅及写回，再发起第二轮并在浏览器撤回。测试会等待取消请求真正落库后再检查租约心跳，避免 UI 点击异步提交造成假失败。浏览器使用临时虚拟 Passkey；驱动不会启动 Hub、沙盒运行器或调用任何私有管道，不替代人工确认。结束后退出该测试 Desktop、关闭设置链接并清理合成项目和凭据。

## 浏览器内切换本机主管

运行 `bun run test:binding`，需要同一组 Core/Web 和 Chromium 构建。测试启动 `--desktop-agent-switch` 管理的空工作区与两个回环 SSE 模型夹具；验证探测失败保留原绑定、活跃期间不交接、取消、配置不因文件编辑而重定向、重启失配保护及重新确认、项目/任务/记忆/历史保留。可额外设置 `SOUS_TEST_BIND_LIVE_PROFILE` 指向测试专用本机模型 YAML，增加真实模型的探测、回复增量和持久化验证。

## 独立云工作区 HTTPS 验收

准备同一套 Core/Web 构建、Chromium 和 `openssl`，运行 `bun run test:hosted`。生成隔离证书和临时数据库，以实际 `sous hosted-hub` 独立监听；Chromium 仅信任测试证书的公钥。使用手机尺寸和触控、真实 HTTPS/WSS 与虚拟 Passkey，验证两项并发首次注册只允许一个、工作台保存、重启恢复、掉线提示和登出撤销。套件不需要云帐号、真实公网、用户个人电脑的 daemon 或 Relay；不作为云工作机或真实手机公网 SLA 的验收。

设置 `SOUS_TEST_HOSTED_ACP=1` 会改为带同机第一方 ACP 主管的云装配：模型由回环 SSE 夹具模拟，ACP 子进程、MCP stdio、浏览器、TLS 和持久化路径是真实路径。设置 `SOUS_TEST_HOSTED_LATENCY=1`（无主管）可记录追加延迟 0/80/180 ms 的热连接、每组 30 个样本；脚本用浏览器提交到 202 为确认时间，并单列 `Server-Timing: hub_persist`。这是客户端模拟，不是对公网拓扑、模型首字或跨区流式性能的 SLA。

设置 `SOUS_TEST_CLOUD_WORKER=1` 则额外测试托管执行：在 Apple Silicon 使用默认 Core worker；在 Linux amd64/arm64 KVM 验收宿主上，用 `SOUS_CLOUD_WORKER_BIN` 指向 `build/core/linux-worker/sous`，Hub 仍用独立的普通构建。两者都需要与执行宿主架构一致且已核验的 `MSB_PATH`、`MSB_LIBKRUNFW_PATH`、`SOUS_TEST_MSBOX_IMAGE_ARCHIVE`。Linux 运行方法见[集成测试](../../integration/README.md)。驱动从手机布局实际上传材料并授权，确认镜像预检失败不消耗授权，再启动独立 worker 和真实 Guest，模拟已持久化但回执丢失、强杀 worker 和只重放回执；Hub 重启后再次选择前一结果启动，并验证浏览器停止长任务。无公共云帐户、无实体手机、无需对任何真实工作区授权。

设置 `SOUS_TEST_MANAGED_WORKER=1` 代替静态 worker 模式，使用同样的真实 Guest 夹具运行 `bun run test:hosted`。测试建立独立 HTTPS Hub，执行真实 `sous cloud-controller` 及托管代次 worker；厂商接口用 `capacity-driver-fixture.ts` 的临时文件模拟，不联系云 API。覆盖：普通工作台操作不启动、手机授权预留一次、开启后未确认的自动对账、浏览器显示启动或回收、worker 回执幂等、达到空闲阈值后旧代次不能领取、回收未确认时不启动第二台、厂商回收响应丢失后对账、下一代真实 Guest 恢复前一结果。结果只证明容量协议与 Guest 流程，不证明厂商资源或计费归零。

设置 `SOUS_TEST_EXTERNAL_MCP=1` 后运行 `bun run test:hosted`：在同一合成托管工作区注册一个固定 HTTPS 回调，使用手机视口、虚拟 Passkey 和真实 HTTPS 完成授权。回调在 Playwright 中隔离拦截；独立的非浏览器 HTTP 客户端换码、发现工具、创建草稿；回到工作台核对同一草稿并从「外部连接」撤销。默认模式验证 `/mcp` 不暴露。可与 `SOUS_TEST_MANAGED_WORKER=1` 合跑，覆盖不同机器身份不依赖同一令牌。官方 MCP Go SDK 客户端另由 `apps/core/internal/externalmcp` 用 TLS 验证；不访问真实外部厂商账号。
