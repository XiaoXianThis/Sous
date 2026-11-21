# 按需云工作机：AWS EC2 验收部署

这是独立云 Hub 的 EC2 驱动，不改变本机沙盒、主管或旧配对。驱动已通过本机签名请求和故障夹具测试，**尚未用真实 AWS 实例和 Linux/KVM 完成发布验收**。不要在生产用户工作区开启。实例、根盘、镜像快照及网络服务可能计费；启动付费验收前应明确批准测试账户、区域、预算、验收时间及清理人。

## 准备受控环境

1. **固定部署身份和网络。** 使用隔离 AWS 账户、商用区域、固定子网及无入站安全组。选该可用区实际提供的 Intel C8i、M8i 或 R8i `large` / `xlarge`。应用明确开启 EC2 嵌套虚拟化，不给实例公网 IPv4；子网必须不自动分配 IPv6、不是 IPv6-only。让 worker 宿主经授权的 VPC 路由到 Hub HTTPS，限制宿主出站范围；Microsandbox 内层 Guest 保持无网络。驱动本身使用固定区域的 AWS API。不要给 worker 配 SSH key 或 AWS 实例角色。
2. **预构建自有 Linux x86_64 AMI**，只含一块 EBS 根盘，尺寸不超过驱动配置的 12–64 GiB。镜像须含支持嵌套虚拟化的 Linux 内核、cloud-init、systemd、`sous-worker` 系统用户/组，以及可用的 `kvm` 组。放入 `build-cloud-worker` 的 Linux **amd64** `sous`、经核验的 Microsandbox `msb` 与固件、`sous cloud-worker prepare-image` 生成的 amd64 OCI 归档。路径与 `work/infra/deploy/hosted/sous-ec2-worker.service` 一致。镜像不带工作区数据库、旧会话、引导、日志或其他测试工作资料；清理 cloud-init 的旧实例状态再封装。AMI 本身不能证明嵌套 KVM 已可用，须在下述验收实例上实测。
3. **隔离控制器角色。** 只有运行 `sous-ec2-driver` 的服务账户获得 AWS 身份。所需 API 为 `GetCallerIdentity`、`DescribeImages`、`DescribeSubnets`、`DescribeSecurityGroups`、`DescribeInstanceTypeOfferings`、`DescribeInstances`、`DescribeVolumes`、`RunInstances`（含按需资源打标签）和 `TerminateInstances`。AWS 写权限应由账户管理员按固定 AMI/网络、实例类型及 `sous:managed`、`sous:scope` 约束；不要授予通用 EC2 管理、`PassRole`、启动模板修改或删除任意卷的权限。实例的根盘加密，实例与盘都带 `sous:managed=worker-v1`、工作域和预留代次标签。

参考 AWS 的 [EC2 嵌套虚拟化](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html) 和 [EC2 API 幂等语义](https://docs.aws.amazon.com/ec2/latest/devguide/ec2-api-idempotency.html)。

## 启动这一套验收部署

1. 构建 `wok run core build-ec2-driver` 得到 Linux amd64 驱动。用 `work/infra/deploy/hosted/ec2-driver.example.json` 作起点，填写实际 Hub HTTPS origin、账户、区域、AMI、子网和安全组。创建专有 `0700` 持久 ledger 目录并让控制器服务账户独占；它不能是临时盘、符号链接或多份复制的目录，也不能与不同工作区共用。
2. 在控制器环境中用 `SOUS_EC2_DRIVER_CONFIG` 指向配置文件。先运行 `sous-ec2-driver verify`：它用实际 AWS 身份只读核验账户、AMI、网络和实例类型供应，**不创建资源**。新部署在确认工作域没有既有实例或未决启动后，运行 `sous-ec2-driver init-ledger` 一次。`sous-ec2-driver status` 只读本机 ledger，不连接 AWS。**既有部署不能因 ledger 丢失而重新初始化**：先找回原持久目录或停止对账，不能假定没有创建请求。
3. 云 Hub 与 `sous cloud-controller` 按 [容量控制契约](../api/core-cloud-capacity.md) 配置。控制器的 `--provider` 指向驱动的绝对路径；管理 Bearer、独立引导种子和 AWS 身份互不替代。传给 EC2 cloud-init 的数据仅含 Hub origin、代次和一次性引导。引导暂存于实例 `/run`，cloud-init 或 AWS 控制面可能保留一份副本，因此仍应限制 userdata 读取；Hub 在注册后立即撤销引导。重启时若 `/run` 已清空，worker 只能用同一代受保护的持久会话继续，不能借此登记新身份。
4. 允许首个测试授权前检查预算、健康状态和标签。首次用手机测试 [Linux/KVM 发布门槛](../../work/tests/integration/README.md#linuxkvm-worker-发布验收)。确认个人设备均关闭、Hub 仍在线，任务触发一次 EC2 启动，Guest 实际在 `/dev/kvm` 执行、无网络，产物保存后进程确认空闲。用 AWS 查询确认精确实例到 `terminated` 且根卷已删除，等待下一任务启动下一代并恢复旧成果。记录首次启动、保存、回收到下一次可用的耗时，分开说明公网与同区环境。不要以系统页面显示 `off` 替代提供商证据，也不要把资源状态代替账单核对。

## 对账与停止

- 每个预留只有一个固定子网和稳定 `ClientToken`；发送前本地记录已同步。只在仍有启动授权时用完全相同的参数有限重试。取消后先持久化栅栏，再查询或终止，不为确认状态另开实例。发现同一工作域的陌生实例、多块卷或变更的根卷时不自动终止。
- AWS 返回 `shutting-down` 或查询暂时找不到实例，**不是**终止证明。已发送但尚无确切结果的请求会保留；已观察 `terminated` 后还查找被标记的卷及记住的根卷。遗留卷由运维核对和保全，不自动删除；不能跳过以解除下一代的限制。
- 若一直无法确认，检查原 `status` 记录、AWS 的预留/代次标签、同区域 API 审计和卷状态。不要移除栅栏、删除 ledger、更换子网重试或手改结论。计划下线时先停止新授权，保留控制器直到 AWS 和 Hub 均确认回收，再关闭托管模式。
