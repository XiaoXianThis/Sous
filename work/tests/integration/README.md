# integration

Integration tests across apps: apps interacting correctly over the contracts in `docs/api/`.

## Running

### Microsandbox 本机能力探测

在 `microsandbox/` 下运行 `go test -v ./...`。默认跳过真机测试；开启前准备同版本官方 `msb` 和 `libkrunfw`（当前测试固定 SDK v0.7.16），设置指向这两个文件绝对路径的 `MSB_PATH`、`MSB_LIBKRUNFW_PATH`，并设 `SOUS_TEST_MICROSANDBOX=1`。需要该运行时支持的本机硬件虚拟化；首次启动需宿主能获取 `alpine:3.22` OCI 镜像，不需要 Docker daemon 或云账号。测试强制拒绝云 backend、使用独立临时运行时目录、不挂载用户文件，并测量导入、无网络执行和销毁。此探测不代替 Sous 的目录授权和端到端测试。

macOS 如遇运行时套接字路径长度限制，显式设置 `SOUS_TEST_SHORT_TMP` 为短的可写临时目录。测试会在该目录下创建并清理独立测试目录，不接触现有运行时数据。

### Microsandbox 本机授权运行器实测

运行 `wok run core build-sandbox`，在 `apps/core/` 中运行 `go test -v -count=1 ./internal/localrun`。默认只跑本机文件筛选、阻断链接/特殊文件与审阅清单测试。Apple Silicon 真机上按前一节设置 `SOUS_TEST_MICROSANDBOX=1`、`MSB_PATH`、`MSB_LIBKRUNFW_PATH`；再将 `SOUS_TEST_SANDBOX_BIN` 指向构建出的 `sous-sandbox` 的绝对路径以开启 CLI 强制中断与重启对账和临时 Git worktree 审阅/写回测试。可将 `SOUS_TEST_MSBOX_IMAGE_ARCHIVE` 指向 `sous-sandbox download-image` 生成的镜像归档，真机测试就不会依赖临时联网拉取。测试只读写合成项目。运行器试用说明见 [本机沙盒](../../../docs/guides/local-sandbox.md)。

### Linux arm64 云 Hub 启动冒烟

先运行 `wok build web`、`wok run core build-hosted-linux-arm64` 和 `wok run core build-sandbox`。在 Apple Silicon 上准备上面的已核验运行时、固件和离线 OCI 归档，然后在 `hosted-linux/` 运行 `bun run smoke.ts`。测试把 Linux arm64 ELF 分片复制进真实无网络 Guest，验证启动、一次性初始化、认证前的路由边界、停止和重启。直接向 Guest 的回环后端发请求，模拟反代传递 Host；它不是公网 HTTPS、`systemd` 服务或云工作机生命周期验收。

### Linux/KVM worker 发布验收

在隔离的 Linux amd64 或 arm64 宿主，以实际 worker 服务账户运行（不要访问真实工作区、不要用 root 绕过设备授权）。需要 Linux C 编译器、glibc 兼容系统和该账户可读写的真实 `/dev/kvm`。`wok run core build-cloud-worker` 生成单独的 `build/core/linux-worker/sous`；普通 Linux Hub 不含执行 SDK。按架构准备 [Microsandbox v0.7.16 官方发布](https://github.com/superradcompany/microsandbox/releases/tag/v0.7.16)的 `msb` 和 `libkrunfw`，核对发布摘要，并设置 `MSB_PATH`、`MSB_LIBKRUNFW_PATH`。执行 `sous cloud-worker prepare-image --output <新归档的绝对路径>` 显式下载并校验对应架构的 Alpine OCI；把归档指定给 `SOUS_TEST_MSBOX_IMAGE_ARCHIVE`。运行时离线导入，不需要 Docker。

在 `apps/core/` 运行 `SOUS_TEST_LINUX_KVM=1 CGO_ENABLED=1 go test -count=1 -v -tags sous_linux_worker ./internal/localrun -run 'Test(KVMProbe|LiveHostedKVM)'`。负例检查非设备、符号链接与错误设备；正例必须成功启动实际的无网络 Guest，Guest 不可见的宿主合成文件不变，项目副本结果在 Guest 销毁前留下检查点。然后在同一宿主跑 [HTTPS 托管执行 E2E](../e2e/local-workspace/README.md)，复验浏览器授权、丢回执、重启、恢复和取消。仅交叉编译、无 KVM 拒绝或默认跳过实机用例均不算通过。

### EC2 驱动协议与真实实例验收

在 `apps/core/` 运行 `go test -count=1 -v ./internal/ec2driver`。套件用合成 AWS 签名凭据、回环 Query 协议 fixture 和本机 durable ledger，覆盖固定的私网/加密/嵌套虚拟化请求、启动结果未知、重复令牌不变、取消后的创建栅栏、观察到真实终态形状之前不释放、已知根卷残留及陌生实例保护。不读用户的 AWS 密钥，不联系 AWS。`verify` 可以在获准的隔离 AWS 测试角色下检查配置而不创建实例；完整付费环境与空闲归零验收见 [EC2 运行手册](../../../docs/guides/ec2-worker.md)。
