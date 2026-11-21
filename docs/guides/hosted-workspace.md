# 独立云工作区 · 部署预览

`sous hosted-hub` 用一套独立数据目录提供一个单所有者工作区。它不依赖用户电脑、旧配对工作区、Relay 或单独部署的前端。手机可以管理对话、项目、任务和记忆。目前不接受旧机器的配对，也不会让云 Hub 直接操作任何个人电脑。可选的 `cloud_worker_token_file` 会开启独立的云工作机执行平面；它只接受专用 worker 的短时租约，不提供宿主机文件或旧 Node 通道。 这是隔离环境中的部署预览，不是已上线的 Sous SaaS，也不替代现有 0.3 配对模式。

## 准备一个独立 HTTPS 源站

1. 构建同一版本的 Core 和 WebUI（`wok build core web`；Linux amd64 使用 `wok run core build-hosted-linux`，Linux arm64 使用 `wok run core build-hosted-linux-arm64`）。将 `sous` 与 `build/web` 的构建内容部署在常在线服务器上。保持 WebUI 构建目录与持久数据目录分开；使用只读构建产物。
2. 为工作区设置**独立域名**；不要与其他应用或工作区共用同一域名的不同端口。域名是 Passkey 的身份边界。`public_url` 只填浏览器使用的 HTTPS origin，不能带路径、凭据、查询或尾随斜杠。即使重新配置，不能把已有数据库直接换到另一域名使用。
3. 示例位于 `work/infra/deploy/hosted/`：配置文件、systemd 服务以及回环后端的 HTTPS 反代。样例服务账户是专用、无特权的 `sous-hosted`，Web 和二进制位于 `/opt/sous-hosted/`，持久目录 `/var/lib/sous-hosted` 的权限为 `0700`。先创建账户、挂载持久卷并把配置样例放到 `/etc/sous-hosted/workspace.yaml`，更换示例域名后再启动。反代与 Hub 在同一宿主上，保留工作区 Host、Origin 和 WebSocket；不要把回环后端映射到内网或公网。
4. 如由 Hub 直接提供 HTTPS，在配置中设置匹配的 `tls_cert` 和 `tls_key`，由运维负责证书签发、轮换与监听权限。只在测试环境使用测试证书；公开环境需要受信任的证书。持续监控错误、连接和延迟，公开上线前需完成运营侧限流、备份恢复和真机验证。

## 首次授权（服务未运行时）

使用与服务相同的账户和配置运行，链接文件必须位于私有目录且尚不存在：

```sh
sous hosted-hub --config /etc/sous-hosted/workspace.yaml --setup-link-file /var/lib/sous-hosted/first-link
```

此命令不监听网络。生成的链接仅写入权限 `0600` 的文件，不写日志；有效 15 分钟，并且只能完成一次首次注册。启动 Hub（`sous hosted-hub --config /etc/sous-hosted/workspace.yaml`，或由 systemd 启动），从手机打开该链接并用 Passkey 注册。传递时只交给工作区所有者，注册完成后从私有目录清理链接。若链接失效且仍未注册，先停服务，再生成新链接。已注册的工作区不会因为重启、传入同样参数或误改配置而自动重置。预览暂不提供遗失全部 Passkey 后的自助恢复。

## 可选：同区 Sous ACP 主管

在服务器配置加入 `agent_config`，指向管理员控制的模型 YAML（格式见[本机工作区](local-workspace.md)）。密钥使用管理员注入的环境变量，不写进 YAML。模型由独立的 ACP 子进程调用；浏览器不会直连模型，不接收任意启动命令或密钥。工作区并非端到端加密到个人电脑：服务器持有业务数据，使用远程模型会把必要上下文发给该模型。只做手动管理就留空 `agent_config`；此时正常保存消息，但不会请求模型或生成回复。

## 持久性与升级纪律

- 一份数据目录只运行一个 Hub。SQLite、附件和适用的本机配置必须位于稳定持久存储；重启不会新建身份或丢失项目任务。停机后可对**整个数据目录**做加密备份；恢复时使用同一 HTTPS origin 并复核权限。不要移动 SQLite 单个文件以冒充完整工作区备份。
- 不用旧配对 Hub 或本机桌面的数据目录启动云 Hub，不会自动上传个人电脑的内容。正式升级及恢复演练仍需按实际部署、构建和数据规模验收。


## 可选云工作机（隔离预览）

1. 为工作区生成一个仅由该工作区 Hub 与 worker 持有的 32 字节随机小写十六进制 token，文件权限 `0600`。把路径写入云 Hub 的 `cloud_worker_token_file`，同一 token 以私有文件方式交给独立 worker；不要写进 YAML、仓库、日志或浏览器。
2. 用 Apple Silicon 上的 Core 构建和已核验的 Microsandbox v0.7.16 运行时及离线镜像启动 `sous cloud-worker --hub https://workspace.example.com --token-file ... --state ... --runtime-home ... --msb ... --firmware ... --image-archive ...`。worker 只通过专用 Bearer Header 接受 claim/heartbeat/complete，拒绝 Cookie、Origin 和重定向；不要把 token 放在普通浏览器接口。
3. 任务详情的「云工作机」入口会显示明确的脚本和输入副本。提交后 Hub 持久化意图，worker 领取并在无网络 Guest 中执行；Guest 结果先写入 worker 的 `pending.json` 检查点，再回收 Guest，最后上传幂等回执。Hub 只在完整回执后显示成功。worker 或 Hub 重启时只发布已有检查点，不自动重跑命令；无法确认 Guest 回收或回执时保留现场并显示中断。
4. 这是当前的单 worker、单并发、限 64 个文件、1 MiB 的材料预览。常驻 worker 无任务时只查询队列；也可用 `--once` 领取至多一项后退出。Guest 每个任务均独立创建和销毁。云厂商 VM/容器的启停、持久磁盘、配额、价格、跨区和公开公网部署尚未接入，不能把 worker 进程退出称作服务器资源已经归零。正式上线前仍需真实 Linux/KVM 验收，以及经过实测的厂商驱动与孤儿实例对账。云 Hub 的 SQLite 和 worker 检查点目录均需持久化；请保持原地址和运行时绑定，勿替换状态目录来跳过对账。[云执行接口与状态](../api/core-cloud-work.md)。


另有用于验收的托管容量模式：`cloud_controller_token_file` 与静态 worker 配置互斥；独立 `sous cloud-controller` 驱动持久化预留、单代注册和安全回收，配套 [容量控制契约](../api/core-cloud-capacity.md)。仓库另提供 [EC2 验收部署](ec2-worker.md) 驱动，已过本机故障与 AWS SDK 协议测试、尚未过真实云实例验收；生产仍不能把托管容量预览当作已支持的云工作机。

## 可选的外部工具入口

[外部 MCP](external-mcp.md) 可让已注册的 HTTPS 客户端读一个用户选定的项目或创建任务草稿；不需要绑定主管。它默认关闭，不与旧 Node、工具服务器管理界面或云 worker 共用凭据。
