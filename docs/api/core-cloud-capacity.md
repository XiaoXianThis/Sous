# core-cloud-capacity：托管执行容量控制

- Provider：常在线 `sous hosted-hub` 的持久队列和容量状态。
- Consumers：独立 `sous cloud-controller`、托管代次的 `sous cloud-worker`、工作台的只读状态提示。
- 管理模式须显式配置 `cloud_controller_token_file`，与 `cloud_worker_token_file` 互斥。默认空闲阈值 300 秒，`cloud_idle_seconds` 可配置为 5–3600。启用前必须结束或取消有效的静态运行；完成成果仍可读。此数据库启用托管后不能改回静态共享凭据。仍保持一个工作区一个权威 Hub；每个工作区只有一个未释放实例预留，不在本机或配对工作区启用。

## 租约与控制面

在与工作区绑定的 HTTPS origin 上，所有下列请求一律 `POST`、严格 JSON，不接受 `Origin` 或 Cookie，不允许重定向。浏览器 Passkey 不授予机器接口权限。预留标识是 ULID，`generation` 为单调递增的整数，时间字段为 Unix 毫秒。控制面使用独立的 Bearer 管理 token，不可用它调用执行接口。管理回复只含容量信息，不返回脚本、作业 ID、文件、密钥或厂商凭据。

| 路径 | 请求 | 回复 `200` 或约束 |
|---|---|---|
| `/api/v1/cloud-capacity/reconcile` | `{generation, reservation?, bootstrap_sha256?}`；后两者成对，代表下一代提案 | `{generation,phase,reservation?,queued,active,idle_since?,worker_seen?,boot_until?}`。代次过旧只返回现状，不推进。只有最新授权仍有可执行排队、且上一代已确认归零才允许写入新的预留。 |
| `/api/v1/cloud-capacity/retired` | `{generation,reservation}` | 仅在当前精确预留已进入回收、且厂商已确认资源永久不存在时调用；重试幂等，旧代次不可确认新预留。不将新队列一起删除。 |
| `/api/v1/cloud-worker/ready` | `X-Sous-Worker-Generation` + 一次性引导 Bearer + `{session_sha256}` | `{ready:true}`；引导限时 10 分钟，且只在仍有可执行任务的 `starting` 接受一次。worker 预检后先私有持久化随机会话密钥，再注册；回执丢失可用同一已登记会话重试。Hub 只存摘要。 |
| `/api/v1/cloud-worker/{claim,heartbeat,complete}` | 原有[云执行接口](core-cloud-work.md)，但使用同一代的会话 Bearer 和 `X-Sous-Worker-Generation` | 授权与领取、续约、回执在同一事务检验。会话闲置有效期为 10 分钟，正常领取或在途操作续期；引导、控制器 Bearer、旧代次或回收中代次不能用。 |

`off` → `starting` → `online` → `retiring` → `off`。草稿、聊天、未完成依赖、未通过确认、任务修改后不再匹配授权或要求指定个人电脑的任务不会启动实例。引导窗口到期或启动中任务全部撤回，可撤销尚未领取的预留；仍需厂商确认后才能回到 `off`。

正常空闲回收必须同时满足：无可执行排队、无在途租约、worker 已在保存回执并移除本地检查点后再次报告空闲、达到空闲阈值且空闲确认仍新鲜（30 秒内）。完成回执、租约失联或超时本身都不能当成空闲证明。标为 `retiring` 的事务先撤销代次权限，晚到任务留在队列等下一代，不再发给旧实例。无法确定成果或回收状态时不自动重跑。浏览器只在授权任务排队时看到 `capacity_phase`（含过期会话的 `needs_reconciliation`），不接触控制接口。

## 独立控制器与厂商驱动

`cloud-controller` 必须由运维提供受信任的绝对路径可执行驱动、私有控制凭据，以及另一份 32 字节引导种子。控制器从规范 origin、代次和随机预留标识重新推导引导密钥，重启不会改变已发起的预留。驱动只收到有时限的单代引导，不接收浏览器认证、Hub 管理 Bearer 或任务内容；厂商账户凭据由驱动自己的独立运行环境管理。驱动、启动通道以及持有引导种子的运维边界必须受到保护：短时引导仍然能登记对应代次的 worker。

驱动每次调用从 stdin 接收一个 JSON，stdout 仅返回 `{state}`，stderr 不传回。超时或响应不明确一律保留同一预留对账。请求包含 `action`（`ensure` 或 `retire`）、`scope`（规范 origin 的 SHA-256）、`generation`、`reservation`、`hub`；仅 `ensure` 带 `bootstrap` 和 `bootstrap_until`。命令行不传密钥。

- `ensure`：最多为这个预留创建一个实例；返回 `pending` 或 `present`。`present` 不是 worker 已就绪，实际 worker 仍要预检、登记。驱动必须能按 `scope`/`reservation` 查回超时请求造成的资源，不得再起第二台。
- `retire`：返回 `pending`，直到实例及其未决启动均已结清；只有外部资源确实不存在且过时的 `ensure` 已被永久封锁，才返回 `absent`。一次查询未发现实例不是足够的确认。并发、重启、丢失响应必须按身份幂等；驱动必须保护回收过的预留并能对账迟到的厂商操作。

控制器与 Hub 的状态机经过文件型厂商夹具验收。仓库附有 [独立 AWS EC2 驱动](../guides/ec2-worker.md)，以 SDK 发送实际 API 参数，经过本机签名协议和故障夹具测试；尚未用真实 AWS 资源验收。文件夹具不验证云实例电源、归零、费用、真实 KVM 或云厂商的最终一致性。
