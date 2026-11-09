---
title: "常见问题与排错（FAQ）"
description: "按「现象 → 原因 → 处理步骤」快速排查 Agent 未登录、额度不足、Worktree 准备失败、macOS TCC 弹窗、504 更新超时与审批卡住等问题。"
group: "faq"
order: 1
updatedAt: "2026-11-09"
---

## 如何使用本页

当你在接入电脑、派发任务或升级版本时遇到报错，可以对照下面的**现象 → 原因 → 处理步骤**直接执行修复命令。

---

## 1. Agent 未登录或体检失败（`auth` / 找不到 `npx`）

### 现象
- 在网页「电脑」面板运行 Agent 自检时提示未登录，或任务启动后立刻失败，错误分类为 `auth`（登录过期）或 `launch`（启动失败，提示 `npx: command not found`）。
- Windows 电脑上你明明在 PowerShell 里登录过 `claude` 或 `codex`，但 Sous 派发任务时依然报未登录。

### 原因
1. `claude` 与 `codex` 通过 ACP 桥接包（`@agentclientprotocol/claude-agent-acp` 与 `@zed-industries/codex-acp`）启动，依赖本机 `PATH` 中存在 Node.js 的 `npx`，且对应 CLI 已完成交互式登录。
2. 在 Windows 上，若将 `sous` 注册为 `LocalSystem` 系统服务，服务进程读取的是系统账户目录而非你的个人用户目录（`%USERPROFILE%`），因此读不到你的登录凭证。

### 处理步骤
1. 在目标电脑终端直接运行一次该 Agent CLI，确认交互对话正常：
   ```bash
   # 验证 Claude Code
   claude
   # 或验证 Codex
   codex
   ```
2. 确认 `npx --version` 可正常输出（建议 Node.js 18+）。如果 Node.js 安装在 `nvm` / `fnm` / `volta` 等非标准路径下，重启一次守护服务以刷新环境，或在 `~/.sous/node.json` 的 `agent_env` 中显式补充 `PATH`：
   ```bash
   sous service restart
   sous status
   ```
3. **Windows 特别说明**：Windows 上 `sous service install` 注册的是 `SYSTEM` 账户系统服务，读不到当前用户目录下的 Agent 登录态。请卸载系统服务（`sous service uninstall`），改用 PowerShell 注册当前用户登录时启动的计划任务（详见 [接入电脑与配置各家 Agent](/docs/connect-machine#5-注册后台常驻服务与终端-sous-chat)）。

---

## 2. 模型额度不足（`agent_quota`）或空跑无产出（`no_action`）

### 现象
- 任务卡片变为红色失败，显示「额度耗尽（`agent_quota`）」或「Agent 未执行有效操作即退出（`no_action`）」。

### 原因
- **`agent_quota`**：子 Agent 绑定的订阅账号（如 Claude Pro/Max、OpenAI Plus 或 API Key）达到了速率限制（Rate Limit / 429）或月度额度上限。
- **`no_action`**：子 Agent 进程虽然返回了退出码 `0`，但既没有调用任何工具、没有产生任何文件改动，也没有输出有效回复（常见于部分 CLI 在后台静默报错或模型拒绝响应）。Sous 的诚实结果判定器（ADR 0024）会将其拦截并标记为 `no_action`，绝不伪造「已完成」。

### 处理步骤
1. 点开任务详情的 **Run 历史**，展开最后一次 Run 的原始日志查看具体报错信息。
2. 如果是单个 Agent 额度用尽，可以在网页「电脑」面板将 **默认 Agent（`default_agent`）** 切换到另一款已登录的 Agent（例如从 `claude` 切到 `codex` 或 `cursor`），或者在重试任务时换一个绑定了其他模型的预设（Preset）。
3. 确认恢复后，在任务卡片上点击 **重试（Retry）**。

---

## 3. Worktree 准备失败（`git worktree add` 报错）

### 现象
- 任务刚派发到节点就报错退出，提示无法创建 `worktree`、分支已存在、`index.lock` 冲突或找不到基准分支。

### 原因
1. 目标仓库中残留了之前异常中断留下的 `.git/index.lock` 或失效的 worktree 登记记录。
2. 仓库是刚初始化的空仓库（没有任何 Commit，`HEAD` 不存在），导致 `git worktree add` 无法切出新分支。
3. 项目的默认分支配置与本地实际分支名不一致。

### 处理步骤
1. 进入目标电脑上的主仓库目录，清理失效的 Worktree 记录与锁文件：
   ```bash
   cd ~/code/your-repo
   git worktree prune
   rm -f .git/index.lock
   ```
2. 如果是一个尚未有过任何提交的全新目录，请先提交一个初始 Commit，或者在派发任务时将任务执行位置改为 **原地修改（`in_place`）**。
3. 修复后在工作台点击任务卡片的 **重试**。

---

## 4. macOS 反复弹出权限窗口（TCC）或后台任务静默卡住

### 现象
- macOS 不断弹出「`sous` 想要访问你桌面/文稿文件夹中的文件」，或者任务在后台运行到读写文件那一步就一直停住不动。

### 原因
- 工作区目录放在受 macOS TCC 保护的路径下（如 `~/Desktop`、`~/Documents`、`~/Downloads`），或本地自行编译的 `sous` 二进制未使用固定的 `Sous Dev Signing` 证书签名，导致每次更新二进制后 macOS 都将其视为新程序重新拦截。

### 处理步骤
1. **推荐做法**：将代码工作区放在非 TCC 保护的家目录路径下（如 `~/code` 或 `~/projects`）。
2. 如果你是从源码本地编译 `sous`，请先初始化固定自签名证书并重新编译安装：
   ```bash
   wok run core setup-signing
   wok build core
   sous service restart
   ```
3. 如果权限记录已经混乱，重置 `cc.wxian.sous` 的 TCC 记录，或在「系统设置 → 隐私与安全性 → 完全磁盘访问权限」中勾选 `~/.local/bin/sous`：
   ```bash
   tccutil reset SystemPolicyAllFiles cc.wxian.sous
   sous service restart
   ```

---

## 5. 网页触发更新或管理操作报 `504 Gateway Timeout`

### 现象
- 在网页点击「立即升级」或执行耗时操作时，浏览器弹出 `504 Gateway Timeout` 或连接断开提示。

### 原因
1. `sous-hub` 触发自升级时会下载二进制并重启 `sous-hub.service`，在服务重启瞬间反向代理（如 Nginx / Caddy / Cloudflare Tunnel）会短暂失去上游连接。
2. 反向代理的 `proxy_read_timeout` 设置过短，或未正确透传 WebSocket 升级头（`Upgrade` / `Connection`）。

### 处理步骤
1. 等待 5–10 秒后刷新网页，检查左下角或「设置 → 版本更新」中的 Hub 版本号是否已经变为最新版本。
2. 在节点电脑上可直接通过命令行完成校验升级与重启：
   ```bash
   sous update
   ```
   若在网页远程执行 Agent 安装或登录时超时（504），也可在节点终端直接运行 `sous agents` 查看状态并手动执行对应的 `login` 命令。
3. 检查 Nginx / Caddy 反代配置，确保 `/ws`、`/node` 与 `/api/v1/terminal` 均已开启 WebSocket 支持，且长连接超时时间不少于 `3600s`。

---

## 6. 审批卡住或觉得审批弹窗太频繁

### 现象
- 任务停在「等待审批（Waiting for approval）」，或者每次子 Agent 跑测试、读文件都要人工点允许。

### 原因
1. 节点的审批档位被设为了最严格的 `ask_all`（每步都问），或者在网页设置中关闭了 AI 审批员（`allow_medium: false`）。
2. 单个 Run 中 AI 自动放行次数达到了 `max_ai_allows_per_run` 上限（默认 `150` 次），后续 `Medium` 请求自动转为人工审批。
3. 子 Agent 请求的操作命中了**硬信号（`Hard`）**（如 `git push`、`sudo`、写工作区以外的路径），按安全红线必须由人工拍板。

### 处理步骤
1. **处理当前卡住的审批**：在聊天流内嵌的黄色审批条或右侧「待审批」标签页中，查看命令详情与 AI 初审备注，点击 **允许一次**、**以后这类都行（`allow_always`）** 或 **拒绝并附理由**。
2. **减少日常打断**：
   - 确认节点 `node.json` 的 `policy.mode` 为 `"auto_low"`（默认）或 `"auto_Safe"`。
   - 在网页「设置 → 审批策略」中确认已开启 **允许审批 Agent 自动处理中风险操作（`allow_medium`）**，并可按需将单次上限调高至 `300`。
   - 对项目内常用的构建/测试脚本点击一次「以后这类都行」，Sous 会绑定该脚本的 SHA-256 哈希自动放行后续调用，直到脚本内容被修改。

---

## 7. 任务被自动暂停（提示 `stuck` / 巡检器介入）

### 现象
- 任务状态变为已暂停或失败，备注提示巡检器（`inspector`）检测到子 Agent 长时间无进展或陷入重复循环。

### 原因
- 子 Agent 在执行某个交互式命令（如等待用户输入 `y/N` 的 CLI）而挂起，或者在同一处编译错误上反复重试未取得进展。

### 处理步骤
1. 打开任务的 **Run 历史** 查看最后几条命令输出，定位卡住的具体命令。
2. 点击 **重试（Retry）** 并附上补充说明（例如：「运行脚手架时请加上 `--yes` 参数避免交互等待」），子 Agent 会续接原会话或按新指令继续完成。

---

## 常见疑问（FAQ）

### Q：关掉浏览器网页后，正在跑的任务会停吗？
**不会。** 主管循环运行在服务器的 `sous-hub` 上，子 Agent 运行在目标电脑后台的 `sous daemon` 中。即使你关掉所有浏览器窗口，任务派发、AI 自动审批、进展合批汇报都会照常推进；下次打开网页即可看到最新结果。

### Q：我的开发机如果在家里内网、没有公网 IP，能接入云上的 Hub 吗？
**完全可以，且无需配置内网穿透（frp / Tailscale 非必需）。** 因为 `sous daemon` 始终是从你的电脑主动向 `sous-hub` 发起出站 WebSocket 连接（`/node`），只要你的电脑能访问 Hub 的 URL 即可双向通信。

### Q：可以在同一台电脑上同时运行 `sous-hub` 和 `sous daemon` 吗？
**可以。** 如果你只有一台主力 Mac 或 Linux 工作站，可以直接在本机启动 `sous-hub`，然后在本机运行 `sous pair http://localhost:8787 <配对码>` 将自己作为节点接入。
