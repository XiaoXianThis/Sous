# core-cloud-work：云工作区执行预览

- Provider：`sous hosted-hub`，须由运维显式配置 `cloud_worker_token_file`（静态参考 worker）或 `cloud_controller_token_file`（[托管容量控制](core-cloud-capacity.md)）。无配置则既无路由，也无前端入口；本机和配对工作区不启用。
- Consumers：WebUI 的任务详情、独立的 `sous cloud-worker`。它与旧 Node、桌面本机沙盒和 ACP 主管的工具桥无关。

## 所有者接口

同源 HTTPS Passkey 会话；写请求需要 CSRF，提交还需要近期 Passkey 验证。

| 方法与路径 | 说明 |
|---|---|
| `GET /api/v1/tasks/{id}/cloud-work` | 返回 `{ "job": Job 或 null }`；托管模式排队时可含 `capacity_phase`。不含输入、脚本、输出或凭据 |
| `POST /api/v1/tasks/{id}/cloud-work` | 审核授权并持久化，返回 `202 { "job": Job }`；不会立即调用或等待 Worker |
| `POST /api/v1/cloud-work/{id}/cancel` | `{}`；返回 `{ "job": Job }`。待领取作业可立即取消；已领取者必须确认回收 |
| `GET /api/v1/projects/{id}/cloud-work-results` | 返回 `{ "results": Job[] }`；该项目可复用的成功快照元数据 |
| `GET /api/v1/cloud-work/{id}/results` | 返回 `{ "files": Bundle 或 null }`；只有已确认完结的结果可读 |

提交严格接受 `{request_id, task_revision, script, files? , base_job_id?}`。`request_id` 为调用者生成的 UUID，同一键与相同意图重复提交时返回原记录，不可改变内容。`task_revision` 是本次审核所用的任务 `updated_at`。必须是归属活动项目、待分配或待办、没有未完成依赖及没有指定旧机器的任务。输入为明确上传的 `files` 或同项目曾成功的 `base_job_id`，二选一。`Bundle` 是相对路径到**标准 Base64** 文件字节的映射，最多 64 个文件、1 MiB 合计；路径不能指向隐私文件、链接或外部目录，不接受系统绝对路径。命令最多 8 KiB，只进入无网络 Guest，不触发宿主执行。单工作区最多 8 条待领取、1 活跃租约、64 条执行记录。预览不提供保留期配置或自动清理已有成果。

`Job` 包含 `id`、`task_id`、`status`、可选 `base_job_id`、`files`（成果数量）、`exit_code`（未知为 `-1`）及 `created_at`、`updated_at`、`expires_at`（毫秒）。状态：`queued`、`running`、`cancel_requested`、`cancelled`、`succeeded`、`failed`、`expired`、`stale`、`interrupted`。待领取授权 30 分钟内有效，变更后会失效；已领取后变更将请求撤回。执行回执独立于任务工作流，不会自动把任务标为完成。已完成的有效快照可以与较晚到达的取消请求竞争：确认结果已保存并已回收时，保留结果而不是虚报取消。

## Worker 专用接口

独立 HTTPS 客户端携带 `Authorization: Bearer <32 字节随机密钥的小写 hex>`，禁止 Cookie、`Origin` 和重定向。此密钥不赋予工作区所有者权限；Owner 会话也不能领取。当前是运维给专用 worker 进程分发的密钥，不是公有云实例身份或自助注册功能。

| `POST /api/v1/cloud-worker/…` | 请求 | 成功返回 |
|---|---|---|
| `claim` | `{workload:"microsandbox-shell-v1"}` | `{claim: null 或 {job_id,workload,lease,script,files}}` |
| `heartbeat` | `{job_id,lease}` | `{stop: boolean}` |
| `complete` | `{job_id,lease,outcome,exit_code,files?}` | 直接返回 `Job`。相同完成回执幂等，不能覆盖终态 |

单个执行租约 45 秒，由实际执行者持续心跳。失联、过期或未知结果不会自动重跑。进程从零领取任务后，启动一次性 Guest，领取前预检固定运行时、离线镜像，Linux 验收版还检查 `/dev/kvm`。按授权只把快照字节导入 Guest，不创建宿主项目目录、不挂载宿主文件，也不回退到宿主执行。正常完成时，先把完整结果与租约写入独立、私有且持久的 worker 检查点并同步，再销毁 Guest，最后向 Hub 提交完成，Hub 同步写入 SQLite 后确认。只有看到匹配回执，worker 才删除检查点。重启后，检查点和运行时必须仍属于同一云工作区；确认销毁后只补交回执，不重新执行。如果检查点写入中断或内容无效、发现未经对账的 Guest，或无法确认回收，不自动销毁旧 Guest，也不领取新任务；未被 Hub 接收且租约已过期的检查点由运维对账，不冒充成功结果。

目前只有 Apple Silicon 参考 worker 通过真实 Guest 托管执行 E2E。Linux amd64/arm64 的验收构建需要显式标签，不是发布支持。Guest 按任务释放，但该宿主不会因作业结束由云厂商关机。
