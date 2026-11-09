---
title: "审批机制：策略、硬信号、审批 Agent 与复用"
description: "详解 sous 的权限模式（default / auto / open）、Node 本地底线、Shell AST 解析与硬信号、审批 Agent 取证循环以及 allow_always 复用规则。"
group: "concepts"
order: 3
sourceDoc: "docs/decisions/0019-auto-approval-layers.md"
updatedAt: "2026-11"
---

让编程 Agent 自动干活时，最大的矛盾是：**全放行怕它删库、改坏线上配置或泄露密钥；每步都问又会被几十条 `ls`、`grep`、`go test` 弹窗烦死**。

`sous` 通过「Node 本地确定性策略 + 硬信号拦截 + 审批 Agent 只读取证 + 同任务决策复用」四层机制解决这一问题（见决策记录 `0005`、`0010`、`0019`、`0024` 及 `apps/core/internal/policy`）。

## 本页目录

- [1. 权限模式与操作类别矩阵](#1-权限模式与操作类别矩阵)
- [2. 第一道锁：Node 本地底线规则（Floor）](#2-第一道锁node-本地底线规则floor)
- [3. 第二道锁：Shell 语法解析、硬信号与常规操作](#3-第二道锁shell-语法解析硬信号与常规操作)
- [4. 第三道锁：审批 Agent 与只读取证循环](#4-第三道锁审批-agent-与只读取证循环)
- [5. 用户决策复用与 `allow_always`](#5-用户决策复用与-allow_always)

---

## 1. 权限模式与操作类别矩阵

当子 Agent 通过 ACP 发起权限请求（`session/request_permission`）时，Node 首先将操作归入 **11 个风险等级递增的类别**，再按当前 Run 的权限模式查表：

| 操作类别 (`Category`) | 典型操作举例 | `default`（默认） | `auto`（自动审批） | `open`（完全开放） | 服务器 (`role: server`) | `readonly` / `verify` |
|---|---|---|---|---|---|---|
| `read` | 读工作树内文件、`git status/diff/log`、`ls`、`grep` | **直接放行** | **直接放行** | **直接放行** | **直接放行** | **直接放行** |
| `edit-worktree` | 修改工作树内文件、`git add/commit/branch`、`sed -i`（树内） | **直接放行** | **直接放行** | **直接放行** | **问用户** | 直接拒绝 |
| `exec-safe` | `go test/build/vet`、`bun test/run`（安全脚本）、`cargo test/check`、`npm ci` | **直接放行** | **直接放行** | **直接放行** | **直接放行** | `verify` 放行 / `readonly` 拒绝 |
| `exec-other` | 未知命令、自定义脚本、设置 `git remote/config` | 问用户 | **交审批 Agent** | 直接放行 | 视脚本副作用 | 直接拒绝 |
| `install` | `npm install <pkg>`、`go get`、`pip install`、`brew install` | 问用户 | **交审批 Agent** | 直接放行 | 交审批 Agent / 问用户 | 直接拒绝 |
| `outside-worktree` | 读写或 `cd` 到工作目录之外的路径、树外重定向 | 问用户 | **交审批 Agent** | 直接放行 | 交审批 Agent / 问用户 | 直接拒绝 |
| `delete` | `rm`、`git rm`、`git clean`、`git reset --hard`、`find -delete` | 问用户 | **交审批 Agent** | 直接放行 | **问用户** | 直接拒绝 |
| `git-remote` | `git push`（非 force）、`gh pr create/edit` | 问用户 | **交审批 Agent**（常规操作直接放行） | 直接放行 | 交审批 Agent | 直接拒绝 |
| `service` | `systemctl start/stop/restart`、`docker compose up/down`、`kubectl apply` | 问用户 | **交审批 Agent** | 直接放行 | **问用户** | 直接拒绝 |
| `irreversible` | `sudo`、`git push --force`、`git branch -D`、`gh release`、`terraform destroy` | 问用户 | **问用户** | 直接放行 | **问用户** | 直接拒绝 |
| `floor` | 命中底线规则（删主目录、格式化磁盘、`curl \| sh` 等） | **直接拒绝** | **直接拒绝** | **直接拒绝** | **直接拒绝** | **直接拒绝** |

> **关键保护**：
> - 在 `role: server` 的服务器上，`open` 模式会被强制降级为 `default`，且修改、删除、服务启停、不可逆操作和未知脚本**永远交由用户人工审批**。
> - `readonly`（用于项目顾问 `consult` 和主管 `machine_look`）与 `verify`（用于验收 Agent `review: agent`）由 Node 本地强制锁定，任何越界请求直接拒绝并把原因返回给 Agent，不打扰用户。

---

## 2. 第一道锁：Node 本地底线规则（Floor）

**为什么需要底线规则**：即便你在开发机上给某个实验任务开了 `open`（完全开放）模式，有些毁灭性命令也绝不能执行。

`apps/core/internal/policy/policy.go` 中的 `DefaultFloor` 内置了 13 条高危正则，在**任何模式**下命中都会被 Node 当场拒绝（`Deny`）：

1. 删除根目录或主目录：`rm -rf /`、`rm -rf ~`、`rm -rf $HOME`、`rm -rf /*`、`rm -rf ~/*`
2. `rm -rf --no-preserve-root`
3. 格式化文件系统：`mkfs` / `mkfs.*`
4. 覆写裸磁盘设备：`dd ... of=/dev/(disk|sd|nvme|hd|rdisk)` 或重定向 `> /dev/(disk|sd|nvme)`
5. macOS 抹盘：`diskutil erase|zero|secureErase|partitionDisk`
6. Windows 格式化盘符：`format C:`
7. PowerShell 递归删除盘符或主目录：`Remove-Item -Recurse C:\` 或 `~`
8. Fork 炸弹：`:(){ :|:& };:`
9. 关机或重启宿主机：`shutdown`、`reboot`、`halt`、`poweroff`
10. 全盘开放写权限：`chmod -R 777 /`
11. 下载并直接管道进 Shell 执行：`curl ... | sh`、`wget ... | bash`（含 `sudo`）

你可以在本机 `~/.sous/node.json` 的 `policy` 中追加自定义底线正则（`extra_floor`）：

```json
{
  "policy": {
    "extra_floor": ["\\bdrop\\s+database\\b", "\\btruncate\\s+table\\b"],
    "floor_disabled": false
  }
}
```

> **远程只收紧原则**：从网页远程编辑 `node.json` 时，只能往 `extra_floor` 和 `extra_hard` 里增加规则，无法删除已有规则，也无法修改 `floor` 或把 `floor_disabled` 设为 `true`。

---

## 3. 第二道锁：Shell 语法解析、硬信号与常规操作

只看命令的第一个词很容易被绕过（例如 `env rm -rf x`、`bash -c '...'`、`echo $(cat ~/.ssh/id_rsa)`）。因此 Node 在本地使用 `mvdan.cc/sh/v3` 对 Shell 命令做完整语法树（AST）分析与解包：

- **解包包装器与子命令**：自动剥离 `env`、`nohup`、`timeout`、`xargs`、`nice`、`caffeinate`、`sh -c`，递归检查管道、`&&`、`;`、`$(...)`、反引号和 `<(...)` 中的每一条真实子命令，取风险最高的类别。
- **工作目录（`cwd`）跟踪**：识别 `cd sub && ...` 后的相对路径归属，避免把在子目录里写 `../../work/tmp` 误判为越出仓库根目录。
- **内置 `sous mcp` 免审直通**：已绑定当前会话身份的默认内建 `sous mcp` 三个工具（`report_progress`、`report_result`、`ask_supervisor`）在通过参数 schema 校验后，由 Node 审计并直接放行（`allow_once`），不弹窗也不调审批模型。

### 硬信号（Hard Signals）与敏感只读（HardRead）

`policy.Assess` 会扫描命令与路径中的风险信号：

1. **硬信号（`Hard`）—— 命中后一律交由用户本人审批（除 `open` 模式外，绝不交审批 Agent，即使原类别是 `read` 也会拦下）**：
   - **写入或导出凭据/密钥**：修改 `~/.ssh`、`~/.aws`、`~/.gnupg`、`~/.kube`、`~/.netrc`、`~/.npmrc`、`/etc/shadow`、`security export/find-generic-password` 等。
   - **向外发送数据**：`curl -d/-F/-T/-X POST`、`wget --post-data`、`scp`、`sftp`、`nc`、`rsync` 到远端、`aws s3 cp` 等。
   - **远程登录**：执行 `ssh <host>` 命令（注意：`journalctl -u ssh` 这类把 `ssh` 当参数名的只读命令不会误触发）。若目标 IP/主机名是另一台已接入 `sous` 的机器，审批卡片会贴心提示「也可以让主管把这件事直接派到那台机器上做」。
   - **混淆或下载后执行**：`base64 -d | sh`、`eval`、`sh <(curl ...)`、`python -c "...exec/subprocess..."`、PowerShell `-EncodedCommand` / `iex`。
   - **写入持久化位置**：`crontab`、`schtasks`、`launchctl load/bootstrap`、`systemctl enable`、写入 `.zshrc` / `.bashrc` / `authorized_keys` / `LaunchAgents`。
   - **改动 Git 远端、钩子或全局配置**：`git remote add/set-url`、`git config --global`、修改 `.git/hooks` 或 `.git/config`。
   - **绕过安全检查**：`--no-verify`、`curl -k/--insecure`、`NODE_TLS_REJECT_UNAUTHORIZED=0`、`GIT_SSL_NO_VERIFY`。
   - **提权或特权容器**：`chown`、`chmod +s`、`docker --privileged` / `--net=host` / 挂载 `docker.sock` 或 `/`、以及 `gh pr merge` / `gh secret`。
   - **从非默认源安装依赖**：`pip install -i`、`npm install https://...`、`git+https://`。
   - **推送到受保护分支**：向 `main`、`master`、`develop`、`release/*`、`hotfix/*`、`prod`、`production`、`trunk`、`stable` 或 `--all` / `--tags` 执行 `git push`。
2. **敏感位置只读（`HardRead`）**：
   - 如读取 `~/.zshrc`、查看 `~/.ssh/config` 或列出 Keychains 目录名。在 `default` 模式下交用户；在 `auto` 模式下交给**审批 Agent** 结合受限脱敏投影判断，不再一律打断用户。

### 常规操作（Routine）在 `auto` 模式下直接放行

在 `auto` 模式下，没有命中任何硬信号的以下高频常规操作由 Node **直接放行并留痕**，无需等待审批 Agent 调用模型：
- 将非受保护的任务分支推送到远端：`git push [-u] <remote> <非受保护分支>`
- 创建或查看 PR：`gh pr create|view|edit|ready|comment|checks|diff|list|status`
- 删除工作树内的构建产物与缓存目录：`rm -rf node_modules/.next/.nuxt/.turbo/.cache/__pycache__/dist/build/out/target/coverage/tmp`

---

## 4. 第三道锁：审批 Agent 与只读取证循环

在 `auto` 模式下，既非直接放行、也未命中 `Hard` 硬信号的请求（如运行自定义脚本、`outside-worktree`、安装普通依赖）会发往 Hub，由 **审批 Agent（`approver`）** 判断。

### 自动附带脚本证据（`ApprovalEvidence`）

Node 在上报审批请求时，会自动识别命令中调用的脚本文件（包括 `sh x.sh`、`node x.mjs`、`python3 x.py` 以及 `cd`/`env`/`timeout` 包装后的脚本，最多 4 个文件、单文件 16 KiB、总计 32 KiB），计算完整原始文件的 SHA-256，并将脱敏后的脚本内容、是否在工作树内、相对于任务启动快照的状态一并附在证据中发给 Hub。

### 审批专用只读取证工具（`inspect` 与 `read_file`）

如果初始证据不够（例如脚本超过 16 KiB 或需要确认某路径状态），Hub 上的审批 Agent 拥有两个**绑定当前审批 Run 与目录**的专用只读工具：
- `inspect(command)`：在目标 Node 上以 `readonly` 档位执行受限只读检查（20 秒超时、16 KiB 上限；支持工作树内检查，以及对 `~/.zshrc` 的 PATH 投影、`~/.ssh/config` 的 Host/HostName/User/Port 投影、`Keychains` 有界列名；严禁读取私钥、`.env`、`.git`）。
- `read_file(path, offset?, max_bytes?)`：读取工作树内或命令点名的临时脚本片段。
- **取证预算**：单次审批最多调用 **4 次工具**（共 64 KiB）、最多 **6 次模型调用**（最后一次不给工具，单轮 120 秒超时），单个 Run 累计最多 40 次取证工具调用。所有取证动作均记入 `approval.checked` 事件，并在最终审批说明中附上「查过：…」。

### 决策规则与硬上限

审批 Agent 输出 `allow`、`deny` 或 `ask_user` 以及把握度 `confidence`（`high` / `medium` / `low`）：
- **默认只有 `confidence: high` 的 `allow` 才会自动放行**（可在 `hub.yaml` 的 `approval_policy.allow_medium` 中配置是否接受满足严格事实门槛的 `medium`，默认 `false`）。
- **信息不足时拒绝（`deny`）而非盲目上交**：当看不到脚本、不确定进程归属或范围过大（如全盘 `find /`）时，审批 Agent 会直接 `deny` 并把原因返回给子 Agent，让子 Agent 自己先缩小范围或确认上下文，减少对你的无谓打扰。
- **代码写死的硬上限**：
  1. 单个 Run 内审批 Agent 最多自动批准 **150 次**（可通过 `hub.yaml` 的 `approval_policy.max_ai_allows_per_run` 在 `150..300` 间调整），超过后全部交由用户审批。
  2. 同一个操作被审批 Agent 拒绝 **2 次**后，第 **3 次**重试时自动转交用户（`ask_user`）。
  3. 未配置审批模型、模型调用连续失败 3 次或返回格式非法时，自动转交用户。

---

## 5. 用户决策复用与 `allow_always`

当审批转交给你时，你可以选择：
- **允许这一次（`allow_once`）**：仅放行当前这一次工具调用，后续相同调用仍需重新评估。
- **始终允许（`allow_always`）**：在**同一个任务（Task）**内复用你的允许决定。
- **拒绝（`reject_once` / `reject_always`）**：拒绝该操作；同一任务内你明确拒绝过的相同操作，后续审批 Agent 也会遵照你的拒绝决定，不再擅自放行。

### `allow_always` 的严格身份校验（`operation_key`）

为了防止子 Agent 先让你批准一个无害的 `./run.sh` 并点了「始终允许」，随后把 `./run.sh` 改成危险内容再执行，`sous` 对 `allow_always` 的复用做了严格的**完整执行身份（`operation_key`）**绑定：
- 必须属于**同一个 Task、同一台 Node、同一个 Agent、相同的机器角色与权限模式、相同的物理工作树根目录、相同的执行 `cwd` 与当前 Git 分支、相同的命令参数与启动环境**；
- 如果命令涉及脚本文件，复用前 Node 会**重新计算脚本全文的 SHA-256 摘要**——哪怕脚本 4 KiB 之后有一字节改动，或脚本含未知动态依赖，原 `allow_always` 许可立即失效，必须重新申请审批。

下一步：阅读 [接入电脑与配置各家 Agent](/docs/connect-machine)，了解如何在电脑上配置这些策略与 Agent。
