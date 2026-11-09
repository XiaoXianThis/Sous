# 审批摩擦分析：数据、机制与改造清单

调查日期 2026-11-09。只读调查，没有改产品代码。代码行号对应提交 `13ff1aa`。

---

## 0. 结论与建议优先级（只读这一页就能做决定）

**数据的限制先说清楚**：hub 不在这台 Mac 上（`~/.sous/node.json` 的 `hub_url` 指向远端 `https://agent.wxian.cc`），`~/.sous` 下没有任何 `.db`。去远端取数的尝试（DNS 查询）被审批拒绝了，按约束没有换写法再试。所以第一部分的全部数字只来自本机发件箱 `~/.sous/outbox.jsonl`：**3 个 Run 发出的 41 条审批请求、5 条有理由的拒绝，时间是 2026-11-09 上午约 35 分钟**。样本小，只能看出“是什么类型的问题”，算不出全局比例。要拿全局数字，在 hub 服务器上跑附录 A 的只读 SQL。

另外，我把 `policy` 包原样拷到临时目录，用真实命令跑了 `Classify → Assess → Judge`（共 80 多条），所以第二、三部分关于“为什么被拦”的结论都**实测过**，不是读代码推出来的。

### 根因（按影响排）

1. **分类器不认引号和 heredoc。** `splitter`（`policy.go:355`）遇到 `|`、`;`、`&&` 或换行就切，引号里的也切；硬信号（`assess.go:141`）对整段命令文本做正则匹配，heredoc 里的正文和 commit message 也算。结果是：
   - `grep -E '^(Port|PermitRootLogin)' /etc/ssh/sshd_config`、`ps aux | grep -E 'frps|nginx'`、`grep 'a\|b'` 这类命令，被切出 `PermitRootLogin`、`nginx'` 这样的“命令”，判成 exec-other。**用户在服务器上看到的 machine_look 被拒，主要就是这个原因**（见 3.6）。
   - `python3 - <<'EOF' …文档正文里写着 security find-identity / gh secret…` 被判成“读写凭据”“改 GitHub 密钥”，直接交给用户。样本里 7 次交给用户的审批，**有 4 次是这种误报**，另有 1 次是 `grep -i version` 里的 `-i` 被当成 pip 的 `-i <源>`。
2. **审批 agent 没有取证能力。** 它只做一次模型调用（`task/advance.go:562 askJSON`，没有工具），能看到的只有标题、detail 和任务说明。`scriptPreview`（`node/outcome.go:156`）只在命令**第一个词**是 `sh`/`bash`/`./x.sh` 时才附上脚本内容，而 agent 实际写的是 `cd … && bash x.sh`、`T=…; sh x.sh`，所以脚本内容一次都没附上。样本里 5 条拒绝，**3 条的理由原文就是“看不到脚本全文”**，被拒的都是 agent 自己刚写进 worktree 的脚本。另外 2 条拒绝里，有 1 条基于错误的事实（审批 agent 以为 hub.db 在本机），它没办法自己核实。
3. **machine_look 用的是 `readonly` 档位**：`readOnly` 集合以外的命令一律拒绝，没有审批 agent 兜底。服务器上常用的只读命令，比如 `journalctl`、`iptables -S`、`hostname`、`ip addr`、`who`、`last`、`nginx -t`、`ufw status`、`systemctl list-timers`，都不在集合里。
4. **一些硬信号的正则太宽**：`journalctl -u ssh` 会命中“远程登录”，`cat /etc/crontab`、`ls /etc/cron.d`、`crontab -l` 会命中“持久化”，`cat ~/.zshrc | grep PATH` 也命中“持久化”，`ls ~/Library/Keychains | grep sous` 命中“读写凭据”。这些都只是读，却和写同一档，交给用户。
5. **sous 自己的 MCP 工具也要审批。** `mcp__sous__report_progress` / `report_result` 被判成“未知类型的操作”（exec-other），每次都要过审批 agent（样本 41 次里有 2 次）。

### 建议优先级

| # | 改什么 | 预计效果（样本内） | 改动量 | 风险 |
|---|---|---|---|---|
| P1 | 分类器认识引号、heredoc；硬信号只匹配“会执行的部分” | 交给用户的 7 次里少 4–5 次；machine_look 的误拒基本消失 | 中（policy 包，要补大量测试） | 低到中：改的是解析器，要用 `TestClassifyBypasses` 守住 |
| P2 | **审批 agent 取证**：先由 node 附上证据包（脚本全文、命令里提到的文件头部、路径是否在 worktree、文件是否刚由本任务改过），再给审批 agent 一个只读的 `inspect` 工具（走 machine.look 的通道和规则） | 5 次拒绝里 3 次直接能批；以后“先 cat 一下再来”这类来回会少很多 | 中（第一步小，第二步中） | 低：只读，并且仍然受 readonly 规则和 secretDump 限制 |
| P3 | `scriptPreview` 先剥掉开头的 `cd X &&`、`VAR=…;`、`export …;` 再找脚本；读文件时按 `cd` 之后的目录解析 | 同上 3 次（属于 P2 的第一步，可以先单独做） | 小 | 低 |
| P4 | 硬信号分成“只读”和“写/外发”两档，只读那档交给审批 agent | 样本里还剩下的 1–2 次（`ls ~/Library/Keychains`、`grep Host ~/.ssh/config`） | 小到中 | 中：见 4.4 的论证和保留项 |
| P5 | `classifyCommand` 记住 `cd`：`cd apps/core && … > ../../work/tmp/x` 不再判成 outside-worktree | 13 次 outside-worktree 里 3 次 | 小 | 低 |
| P6 | sous 自己的 MCP 工具（`mcp__sous__*`）直接放行 | 41 次审批 agent 调用里少 2 次 | 很小 | 很低 |
| P7 | 扩大 `readOnly`（服务器查看类命令） | machine_look 在服务器上的大部分误拒 | 小 | 低 |
| P8 | 审批 agent 在低风险类别（exec-other、outside-worktree 只读、install 默认源）里 `medium` 也能放行 | 数据不够，估不出来（要用附录 A 的 SQL 看“把握不够”占多少） | 很小 | 中 |
| P9 | 用户在同一任务里选过 `allow_always` 的操作，之后自动放行 | 数据不够 | 小 | 低到中 |
| P10 | `maxAIAllowsPerRun` 按类别计数，或者提高到 300 | 数据不够；样本里最多的一个 Run 只有 26 次 | 很小 | 低 |

**建议先做 P3 + P6 + P7 + P5（都很小，风险低），再做 P1 和 P2（收益最大）。P4、P8、P9 等附录 A 的数字出来以后再决定。**

**不建议动**：底线规则；`irreversible` / `floor` 不交给审批 agent；服务器角色上的删除、服务、不可逆操作仍交用户；拒绝记录和 `recordPolicy` 审计。理由见第 5 节。

---

## 1. 数据采集

### 1.1 数据来源与样本量

| 来源 | 结果 |
|---|---|
| `~/.sous/hub.db` | **不存在**。`~/.sous` 下只有 `node.json`、`outbox.jsonl`、`gitops.jsonl`、`agent-options.json`、`runs/`（2 个 Run 的 spec JSON）、`scratch/`、`worktrees/`、`shadow/`。 |
| hub 的位置 | `node.json` 的 `hub_url` = `https://agent.wxian.cc`（远端）。代码里的路径是 `<data_dir>/hub.db`（`apps/core/internal/hub/hub.go:62`）。 |
| 远端取数 | `dig agent.wxian.cc` 被审批 agent 拒绝（理由是“对外查询不在任务范围内”），按约束没有换写法重试。**因策略被拦，未验证。** |
| `~/.sous/outbox.jsonl` | 448 行，是 node 发给 hub 的帧的滚动窗口（`f` 发出、`a` 已确认）。其中 `approval.request` 41 条，`run.update` 215 条，`run.started` 5 条，`run.ended` 3 条。帧 id 从 `01M6Z1EHG4…` 到 `01M6Z2H5Z9…`，大约是 2026-11-09 10:00–10:35。 |
| `~/.sous/runs/*.log` | 不存在。`runs/` 下只有 2 个 `.json`（Run 的 spec，`permission_mode` 都是 `auto`）。 |

**审批的结论（批准、拒绝、谁决定的）不在 outbox 里**：它们是 hub 发给 node 的，本机没有存。能拿到的只有两样：一是 `run.ended.refused`（每个 Run 被拒操作的理由原文，共 5 条）；二是工具调用的结果里出现的 `User refused permission to run tool`（共 4 次）。

### 1.2 能给出的数字（n = 41 条审批请求，3 个 Run）

Run：`…EC23`（CI：macOS 发布产物签名，Claude/opus，auto）26 条；`…SK0E`（本任务的上一轮）11 条；`…8B6E`（EC23 的后续）4 条。

**按 category**

| category | 条数 | 占比 |
|---|---|---|
| exec-other | 23 | 56% |
| outside-worktree | 13 | 32% |
| install | 2 | 5% |
| delete | 2 | 5% |
| irreversible | 1 | 2% |

**按 review**：ai 34（83%），user 7（17%）。

**category × review**

| category | ai | user |
|---|---|---|
| exec-other | 20 | 3 |
| outside-worktree | 11 | 2 |
| install | 1 | 1 |
| delete | 2 | 0 |
| irreversible | 0 | 1 |

**7 条 user 的原因**：全部是 node 端的硬信号（`Judge` 在 `assess.go:322` 把它们转成 AskUser）。**没有一条是审批 agent 转上来的**（outbox 里看不到 hub 端的 escalation；`escalation` 字段的分组只能用附录 A 在 hub 上统计）。

| 硬信号 | 次数（一条可以命中多个） |
|---|---|
| 读写凭据或密钥 | 5 |
| 持久化（定时任务、启动项、shell 配置） | 2 |
| 改动 GitHub 上的权限或密钥，或合并 PR | 1 |
| 从非默认来源安装 | 1 |

**这 7 条里实际有风险的**：经逐条判断（见第 2 节），5 条是误报（4 条是 heredoc 或字符串里的文字，1 条是 `-i` 误认），2 条是对家目录敏感位置的**只读**访问（`~/.zshrc`、`~/.ssh/config`），合理但可以交给审批 agent。

**node 端自己放行、不经 hub 的**（`run.update kind=permission status=allow`）：read 8、edit-worktree 6。

**拒绝（5 条，来自 `run.ended.refused`）**，按理由聚类：

| 理由 | 条数 |
|---|---|
| 看不到要运行的脚本的全文（`e2e.sh`、`export-signing-cert.sh`） | 3 |
| 范围超出（`find /` 全盘搜索，和读 node.json、outbox 放在同一条命令里） | 1 |
| 对外网络查询（`dig`） | 1 |

**工具调用失败**（`run.update status=failed`，n = 7）：`User refused permission to run tool` 4 次，命令非零退出 1 次，`text 不能为空`（一次空的 MCP 调用）1 次，其他 1 次。

### 1.3 要求里有、但这次拿不到的数字

- approvals 的总数、`decided_by` 分组、`escalation` 原因分组、按 category 统计“转人工率”、拒绝原因 TOP N：都需要 hub.db。SQL 写在附录 A。
- runs 的失败分类（额度不足、超时、被拒绝、准备目录失败）：本机只有 3 个 `run.ended`（succeeded 1、cancelled/用户取消 2），**没有失败样本**。SQL 写在附录 A。

---

## 2. 被拒绝或转人工的典型样本（全部是真实请求原文）

“实测”一列是用拷出来的 policy 包跑的结果，和 outbox 里记录的 category 一致。

| # | 命令（节选） | category | 触发 | 结论 | 实际有害吗 |
|---|---|---|---|---|---|
| 1 | `cat apps/core/AGENTS.md; …; ls ~/Library/Keychains/ \| grep sous; grep -rn "0008…"` | exec-other | 硬信号“读写凭据或密钥”（`assess.go:69`，片段 `Library/Keychains`） | 交给用户 | **无害**。只列出钥匙串的文件名，不读内容，任务本身就是钥匙串签名。 |
| 2 | `goreleaser --version 2>&1 \| grep -i version; go version; … go install github.com/rhysd/actionlint/cmd/actionlint@latest` | install | `installSource`（`assess.go:96`）：`grep -i version` 里的 `-i version` 被正则 `(^\|\s)-i\s+\S` 当成了 pip 的“自定义源” | 交给用户 | **无害**，属于误报。同一条 `go install` 去掉 `grep -i` 后再发，就走了审批 agent（样本第 4 条）。 |
| 3 | `ls /usr/local/go/bin … ; cat ~/.zshrc 2>/dev/null \| grep -i -n "go\|path" \| head` | exec-other | “持久化”（`assess.go:82`，片段 `.zshrc`） | 交给用户 | **低风险**。只读，用来找 Go 的 PATH；不过 `.zshrc` 里可能 export 了令牌，`grep path` 可能把它们带出来。可以交给审批 agent。 |
| 4 | `cd …xqgxwnf1yg && python3 - <<'EOF' … docs/guides/macos-signing…md … EOF` | **irreversible** | “读写凭据”+“改 GitHub 密钥”：heredoc 正文（写进文档的文字）里出现了 `security find-identity`、`gh secret` | 交给用户 | **无害**，属于误报。只是往 worktree 里的 md 文件写字。 |
| 5 | `python3 - <<'EOF' def rep(p, old, new): … rep('apps/core/.goreleaser.yaml', …"~/Library/Keychains/sous-signing…"…)` | outside-worktree | “读写凭据”（文字里有 `Library/Keychains`）；outside 是因为 heredoc 的行被当成命令 | 交给用户 | **无害**，属于误报。只改 worktree 里的 yaml 注释。 |
| 6 | `grep -i '^\s*host\b' ~/.ssh/config; grep -rn 'hub.db…' apps/core/` | exec-other | “读写凭据”（`~/.ssh`） | 交给用户 → 用户拒绝 | **低风险**。只读 Host 别名，不读私钥；但会暴露基础设施，交给用户可以理解。只读档可以交审批 agent，由它判断“任务需不需要”。 |
| 7 | `mkdir -p work/tmp/…/probe; cp …; cat > main.go <<'EOF' … EOF; printf … 'ls ~/.ssh' 'cat ~/.bashrc' … \| ./probe` | outside-worktree | “读写凭据”+“持久化”：printf 的参数（测试数据）里有 `~/.ssh`、`.bashrc` | 交给用户 | **无害**，属于误报。只把字符串当数据传进程序。 |
| 8 | `cd …xqgxwnf1yg && timeout 180 bash work/tmp/ci-macos-signing/e2e.sh 2>&1; …; security list-keychains -d user` | exec-other | 无硬信号 | **审批 agent 拒绝**：“待运行的 e2e.sh 内容看不到 … 请先 cat 出全文” | **无害**。脚本是 agent 在本任务里刚写的，只建并清理一个临时钥匙串。拒绝的根因是 `scriptPreview` 没附上内容（命令以 `cd` 开头）。 |
| 9 | `T=$PWD/work/tmp/ci-macos-signing; env -u SOUS_SIGN_CERT_P12_PASSWORD sh apps/core/scripts/export-signing-cert.sh; …` | exec-other | 无 | **审批 agent 拒绝**（2 次）：“看不到 export-signing-cert.sh 全文，无法确认密码缺失时会先退出” | **这次是谨慎得对的**：它要确认脚本不会真的把私钥导出。但它需要的只是“读一下 worktree 里的那个脚本”，有取证能力就能自己确认，不用来回两轮。 |
| 10 | `cd ~/.sous; grep -o '"[a-z_]*":' node.json …; head -c 1500 outbox.jsonl; find / -name "hub.db"` | outside-worktree | 无 | **审批 agent 拒绝**：find / 范围太大 | **合理的拒绝**。全盘 find 确实超出范围，理由也给了替代办法。 |
| 11 | `dig +short agent.wxian.cc \| head` | exec-other | 无 | **审批 agent 拒绝**：说“数据全在本地 ~/.sous/hub.db” | **无害**（DNS 查询）。拒绝的前提是错的（hub.db 不在本地），审批 agent 没办法自己核实。 |
| 12 | `mcp__sous__report_progress` / `mcp__sous__report_result` | exec-other（“未知类型的操作”，`policy.go:255`） | 无 | 交给审批 agent | **无害**。这是 sous 自己发给子 agent 的工具，不该审批。 |
| 13 | `export PATH=…; cd apps/core && SOUS_SIGN=require goreleaser release --snapshot --clean > ../../work/tmp/…/snapshot.log` | outside-worktree | `redirectCategory`（`policy.go:448`）：没有记住 `cd apps/core`，把 `../../work/tmp` 按 worktree 根算，结果在外面 | 交给审批 agent | **无害**，属于误报。写的是 worktree 里的 `work/tmp`。 |
| 14 | `git add -A && git commit -q -m "CI, Mac: …多行说明…"` | outside-worktree | 多行 commit message 按换行被切成“命令”，其中一行被判成 outside | 交给审批 agent | **无害**，属于误报。 |
| 15 | （machine_look，服务器，实测）`grep -E '^(Port\|PermitRootLogin\|PasswordAuthentication)' /etc/ssh/sshd_config` | exec-other | 引号里的 `\|` 被切开，`PermitRootLogin` 成了一条“命令” | readonly 档：**直接拒绝** | **无害**，只读。 |
| 16 | （machine_look，实测）`journalctl -u ssh --since today \| grep -c Failed` | exec-other + 硬信号“远程登录” | `journalctl` 不在 `readOnly` 里；`-u ssh --since` 命中 `assess.go:77` 的 `ssh\s+\S` | 拒绝 | **无害**，只读日志。 |

---

## 3. 代码面逐条标注

### 3.1 `policy/policy.go`

- **模式常量**（`policy.go:51-60`）：`ModeDefault="default"`、`ModeAuto="auto"`、`ModeOpen="open"`，另有决策 0022 的 `ModeReadOnly="readonly"`、`ModeVerify="verify"`。`Restricted`（`:64`）对后两个返回 true。
- **`Decide` 模式表**（`:67-96`）：
  - readonly：只有 Read 放行，其余 Deny（`:69-73`）。verify：Read、ExecSafe 放行，其余 Deny（`:74-78`）。
  - 其他模式：Floor → Deny；Read、EditWorktree、ExecSafe → Allow（`:80-85`）。
  - open：其余全部 Allow（`:87-88`）。auto：Irreversible → AskUser，其余 → AskAI（`:89-93`）。default：其余 → AskUser（`:95`）。
- **`EffectiveMode`**（`:106-111`）：server 上的 open 降为 default。
- **`DecideFor` 的 server 分支**（`:117-131`）：restricted 模式不变；server 上 Floor → Deny；**EditWorktree、Delete、Service、Irreversible → AskUser**（`:126-127`），不经审批 agent，也不按表放行编辑。
- **分类**：`Classify`（`:211-256`）；没有 kind 也没有命令时 → `ExecOther "未知类型的操作"`（`:255`，sous 自己的 MCP 工具就落在这里）。`splitter`（`:355`）就是 `&&|\|\||;|\||\n`，**不认引号**。`classifyBase`（`:458-720`）里有各工具的子命令表。`readOnly`（`:746-757`）、`safeTools`（`:760-766`）、`isSystemPath`（`:736-743`，包含 `/etc/`、`/usr/`）。`cd` 只看目标是否在外面（`:498-503`），**不改变后续部分的相对路径基准**。

### 3.2 `policy/assess.go`

**`hardSignals`**（`:68-93`）：变量里有 10 条正则，加上另外 3 个来源，共 13 个硬信号来源。

| # | 名字 | 行 | 匹配要点 | 实测误报 |
|---|---|---|---|---|
| 1 | 读写凭据或密钥 | `:69-70` | `~/.ssh` 等家目录凭据、`id_rsa`、`/etc/shadow`、`Library/Keychains`、`security (find\|dump\|export\|add)-` | 文档或 heredoc 里的字样；`ls ~/Library/Keychains` |
| 2 | 向外发送数据 | `:71-76` | curl 带数据或非 GET、`scp/sftp/nc/socat/telnet`、`rsync host:`、s3/gsutil/rclone、PowerShell 上传 | — |
| 3 | 远程登录 | `:77` | `(^\|[\s;&\|(])ssh\s+\S` | `journalctl -u ssh`、`systemctl status ssh` |
| 4 | 混淆或下载后执行 | `:78-81` | base64 -d 接管道给 shell、`eval`、`sh <(…)`、`$(curl`、`python -c` 里有 exec 或网络调用、`iex` | — |
| 5 | 持久化 | `:82-83` | `crontab`、`schtasks`、`launchctl load…`、`systemctl enable…`、`reg add`、`.bashrc/.zshrc/…`、`authorized_keys`、`/etc/cron` | `crontab -l`、`cat /etc/crontab`、`ls /etc/cron.d`、`cat ~/.zshrc` |
| 6 | 改动 git 的远端、钩子或全局配置 | `:84-86` | `git remote add/set-url…`、`git config --global…`、`.git/hooks` | — |
| 7 | 绕过检查 | `:87-88` | `--no-verify`、`curl -k`、关闭 TLS 校验 | — |
| 8 | 提权或改权限 | `:89` | `chown/chgrp/setcap/setfacl`、`chmod +s` | — |
| 9 | 特权容器 | `:90` | `docker --privileged`、`docker.sock`、`-v /:` | — |
| 10 | 改动 GitHub 上的权限或密钥，或合并 PR | `:91-92` | `gh secret/auth/…`、`gh pr merge`、`gh api` 写操作 | 文档或 heredoc 里的字样 |
| 11 | 自定义（`extraHard`） | `:147-151` | node.json 的 `policy.extra_hard` | — |
| 12 | 从非默认来源安装（`installSource`） | `:96-97`，在 `:152` 生效（仅 Install 类别） | `git+`、URL、`--registry`、`(^\|\s)-i\s+\S`…… | `grep -i version` |
| 13 | push 到受保护的分支 | `:155-161`（`pushedProtected` `:175-206`） | main/master/release/… | — |

匹配对象（`:133-136`）：命令全文加上全部路径，**不分可执行部分和数据部分**（heredoc 正文、引号里的字符串、commit message 都算）。

- **`softSignals`**（`:99-105`）：联网下载、读写 .env、批量或递归操作、强制或丢弃改动、后台常驻进程。只作为提醒附进 detail，不改变走向。
- **`routine` 白名单**（`:230-274`）：只看没有管道、重定向、替换的单条命令（`simpleCmd` `:209`）。GitRemote：`gh pr create/view/edit/ready/comment/checks/diff/list/status`，或者 `git push [-u] <remote> <非受保护分支>`；Delete：`rm [-rf] <worktree 里的构建产物或缓存目录>`（`cacheDirs`、`outputDirs` `:220-226`）。
- **`Judge` 的判定顺序**（`:308-329`）：
  1. `act = DecideFor(role, mode, c)`；
  2. restricted 模式：Deny → Deny（理由 `restrictedWhy`）；有硬信号 → **Deny**（`:314-316`）；否则按表处理；
  3. open（实际生效的模式）或表说 Deny → 照表；
  4. 有硬信号，并且表说 Allow 或 AskAI → **AskUser**（`:322-324`）。也就是说，在 default/auto 模式里，硬信号连本来放行的 Read 也会拦下来；
  5. AskAI 并且 routine 不为空 → Allow（`:325-327`）；
  6. 否则照表。

### 3.3 `task/approve.go`：所有交给用户的分支

| 行 | 条件 | 去向 |
|---|---|---|
| `:48-49` | `review != "ai"` | `followUser`（只自动跟从“拒绝”） |
| `:52-53` | 没有 allow 选项 | 交给用户 |
| `:55-56` | `aiNever[category]`（`:32`：irreversible、floor） | 交给用户 |
| `:64-71` | `userPrecedent`：同一任务里，用户对**同一个** category+title+detail 已经答复过 | 跟着用户：允许和拒绝**都复用**（这是 review=ai 的路径） |
| `:84-85` | 这个 Run 里审批 agent 已经批准了 ≥ `maxAIAllowsPerRun`（`:21`，150）次 | 交给用户 |
| `:87-88` | 同一 title+category 被审批 agent 拒绝 ≥ `maxAIDeniesPerOp`（`:24`，3）次 | 交给用户 |
| `:91-92` | 审批模型不可用 | 交给用户（“没有配置审批模型”） |
| `:102-106` | 调用失败到第 `approverGiveUp` 次 | 交给用户 |
| `:109`、`:112-113` | `sure = confidence 不是 low 也不是 medium`；allow 但不 sure（medium、low） | 交给用户（“把握不够”） |
| `:117-118` | deny 但没有 reject 选项，或者 confidence=low | 交给用户（“倾向拒绝”） |
| `:122` | decision 是 ask_user 或别的值 | 交给用户 |
| `:219-241` | `followUser`：review=user 的请求，用户以前**拒绝**过同一操作就自动拒绝；**以前允许过就不复用**（`:231-234`，注释说“允许是用户自己要重复的”） | — |

要注意的一点：`userPrecedent` 要求 `Detail` 完全相同。detail 里含有 rawInput 的漂亮 JSON，`description` 字段一变就匹配不上，所以“复用用户的答复”实际很少生效。

### 3.4 审批 agent 的提示词和输入（根因）

- 提示词：`prompts/roles.go:22-59`（`prompts.Approver`）。内容是四条判断标准（必要性、范围、可逆、来源）、各类别的参考、“永远不批准”清单，以及“信息不够就 deny 并告诉 agent 先怎么确认”（`:36`）。
- 输入：`approverInput`（`task/approve.go:172-214`）包括任务标题、类型、说明（3000 字）、Run 的 prompt（3000 字）、操作的类别、标题、detail（4000 字）、本任务最近 10 条审批。
- detail 的来源（`node/run.go:800-805`）：硬信号说明 + why + rawInput JSON，再加 `scriptPreview` 的脚本内容（4 KB，`node/outcome.go:149`）。`scriptPreview`（`outcome.go:156-175`）**只看命令的第一个词**：是 `sh/bash/zsh/dash/fish`、解释器，或者 `./x`、`*.sh`。`cd X && bash y.sh`、`T=…; sh y.sh`、`timeout 180 bash y.sh` 都拿不到预览。
- 调用方式：`askJSON`（`task/advance.go:562-584`）只做**一次**模型调用，没有工具。
- **结论：审批 agent 不能自己读文件、跑查看命令，也不能核实任何事。** 提示词又要求它“信息不够就 deny、让 agent 先 cat”，结果每次信息缺口都变成“拒绝 → agent 重新组织 → 再申请”的来回。来回多了就碰上 `maxAIDeniesPerOp` 和 `maxRefusalTurns`（`node/run.go:833`，3 轮），最后变成 Run 失败或者交给用户。这是用户说的“调用失败多、问我多”的主要机制之一。

### 3.5 `node/run.go`：Judge 的调用点和拒绝理由的回传

- 三个入口都走 `r.judge`（`run.go:818-822`）：`onPermission`（`:768`，`:787` 调用）、`workbench.permit`（`node/workbench.go:176`），以及 deliver。
- node 自己拒绝（`:793-796`）：`noteRefusal(title, "sous 的安全规则禁止："+why)`，然后回 ACP 的 reject 选项。
- hub 拒绝（`:807-809`）：把审批的 note 记下来。
- **理由怎么回到 agent**：ACP 的 `reject_once` 不能带说明。所以 agent 在当前这一轮只看到 `User refused permission to run tool`（样本里 4 次都是这句），**看不到理由**。理由要等 agent 自己结束这一轮（stop=cancelled）之后，才由 `refusalPrompt`（`:845-848`）拼成下一轮的提示发过去（`:525-541`，最多 `maxRefusalTurns`=3 轮）。如果 agent 不结束这一轮，而是换个写法继续，它就是在猜。这也解释了“同一操作被拒 N 次”。
- 改进方向（P2 的一部分）：ACP 的 reject 带不了理由，但工具结果可以。可以考虑在拒绝之后立刻用 `session/prompt` 的追加消息（如果 agent 支持），或者用 sous MCP 的通道把理由推给 agent。在那之前，至少应该在 `refusalPrompt` 之外，把理由写进 `run.update` 的工具输出里，让界面和主管都看得到。

### 3.6 `machine_look` 在服务器上被拒的真实原因（实测，不是推测）

调用链：`task/look.go:46 Call` → `Nodes.Look` → node 端 `node/look.go:33 look`：

```
cat, why := pol.Classify(req)                                         // look.go:48
act, reason := policy.Judge(role, policy.ModeReadOnly, cat, Assess…)  // look.go:49
if act != policy.Allow { 拒绝 "machine_look 只能执行只读的查看命令…（cat）…" } // :50
if secretDump.MatchString(cmd) { 拒绝 }                               // :53
```

**结论**：没有走 `Trigger`，也没有走另一套规则。machine_look 固定用 **`ModeReadOnly`**（`look.go:49`），所以 `Decide` 只放行 `Category == Read`（`policy.go:69-73`）；分到 exec-other、service、irreversible 的一律直接拒绝，不问任何人。有硬信号的 Read 也拒绝（`assess.go:314-316`）。server 角色在这里不起作用（`DecideFor` 对 restricted 模式直接走 `Decide`，`policy.go:119-121`）。

用户报告的各条命令，在当前代码上的实测结果：

| 命令 | 结果 | 确切原因 |
|---|---|---|
| `cat /etc/os-release` | 放行 | `cat` 在 `readOnly` 里，`/etc/` 是 `isSystemPath` |
| `ps aux \| grep x` | **放行** | 当前代码判成 Read |
| `ps aux \| grep -E 'frps\|nginx'`、`ps aux \| grep 'sous\|frp'` | 拒绝 exec-other | **引号里的 `\|` 被 `splitter` 切开**（`policy.go:355`），`nginx'` 成了命令名 |
| `grep PermitRootLogin /etc/ssh/sshd_config` | **放行** | — |
| `grep -E '^(Port\|PermitRootLogin…)' /etc/ssh/sshd_config` | 拒绝 exec-other | 同上，`PermitRootLogin` 成了命令名 |
| `journalctl … \| grep -c …` | 拒绝 exec-other | `journalctl` 不在 `readOnly`（`policy.go:746`），落到 `:719`；`-u ssh` 还额外命中“远程登录”（`assess.go:77`） |
| `crontab -l` | 拒绝 exec-other | `crontab` 不在 `readOnly`，并且命中“持久化”（`assess.go:82`） |
| `cat /etc/crontab`、`ls /etc/cron.d` | 拒绝（read，但有硬信号） | “持久化”的 `/etc/cron`、`crontab` 字样 |
| `iptables -S`、`ufw status`、`nginx -t`、`hostname`、`ip addr`、`who`、`w`、`last`、`lastb` | 拒绝 exec-other | 不在 `readOnly` |
| `systemctl list-timers` | 拒绝 service | `policy.go:639` 的 systemctl 只读子命令列表里没有它 |
| `sudo …`（任何命令） | 拒绝 irreversible | `policy.go:546-547` |

所以用户看到的“`ps aux | grep x` 和 `grep sshd_config` 被拒”，最可能的实际写法是带了 `grep -E 'a|b'` 或 `'a\|b'` 的形式（主管写排查命令时几乎总是这么写）。另一种可能是服务器上跑的 sous 版本比 `32c0372` 早（machine_look 和服务器查看类命令是在这个提交里加的）。我看不到服务器版本，**这一点因为不能访问远端，没有验证**。

---

## 4. 改造建议

“预计效果”以 1.2 的样本（41 条审批请求，其中 7 条交给用户，5 条拒绝）为基准，只说明在样本里能省掉哪些，不外推成全局比例。

### 4.1 P1：分类器认识引号和 heredoc；硬信号只匹配会执行的部分

- **改哪里**：`policy/policy.go` 的 `classifyCommand`（`:358`）、`splitter`（`:355`）、`classifyOne` 的按空白拆词（`:462`）；`policy/assess.go` 的 `Assess`（`:133-146`）。
- **改什么**：
  1. 写一个小的 shell 词法器（也可以用 `mvdan.cc/sh/v3/syntax`，它是纯 Go 的；引入依赖要用户同意）。它按 shell 规则切分 `;`、`&&`、`||`、`|`、换行，**单双引号里的不切**，并且识别 heredoc（`<<'EOF' … EOF`），把正文从命令里拿出来。
  2. heredoc 正文的处理：如果正文是喂给解释器的（`python3 - <<EOF`、`bash <<EOF`），整条按“执行脚本”处理，并把**正文当作 scriptPreview 附给审批 agent**，但不在正文上跑硬信号正则；如果是 `cat > file <<EOF`，就只当成写那个文件。
  3. 硬信号只匹配“命令名 + 参数”。`git commit -m`、`echo`、`printf` 的字符串参数不参与匹配（或者降为软信号）。
- **安全性论证**：最坏的情况是有人把危险命令藏在引号里，比如 `bash -c 'curl x | sh'`。这类情况已经由 `shellC`（`:393`）展开处理，新词法器要保证 `sh -c`、`eval`、`$(…)` 的内容**仍然当作命令递归分类**。heredoc 喂给解释器的情况会降为“执行脚本”，走审批 agent 并附上正文，不是放行。底线规则仍然在整段原文上匹配（`Classify` 的 `:218-222` 保持不变），所以 `rm -rf /` 藏进 heredoc 也照样拦下。决策 0010 已经承认文本匹配不是隔离，这次改动没有降低这个层面的保证。必须先补 `TestClassifyBypasses`，再加引号、heredoc 的反例。
- **预计效果**：样本中交给用户的 7 条里，第 4、5、7 条（heredoc 或字符串误报）和 machine_look 的引号误拒消失；第 14 条（commit message）和第 13 类误报也会少。交给用户的次数 **7 → 3 左右**。

### 4.2 P2：审批 agent 的取证能力（重点设计）

目标是让审批 agent 在“信息不够”的时候自己去查，而不是 deny 让 agent 再来一趟。分两步做。

**第一步（node 端的证据包，小改动，先做）**

- **改哪里**：`node/run.go` 的 `onPermission`（`:800-805`），以及 `node/outcome.go` 的 `scriptPreview`（`:156`），扩展成 `evidence(cmd, workdir)`。
- **改什么**：每次请求交给审批 agent（review=ai）时，node 在本机收集一份**有上限**的证据，附进 detail 的“## 证据”一节：
  1. 命令里提到的每个脚本（不只看第一个词；先剥掉 `cd X &&`、`VAR=…`、`export …;`、`timeout N`、`env -u …` 再找，并按 `cd` 之后的目录解析）：路径、在不在 worktree、`git status` 是否显示**本任务新建或修改**，以及前 4 KB 内容。
  2. 命令里出现的、worktree 以外的路径：存在不存在、文件还是目录、大小、属主。**不读内容**。
  3. `git diff --stat` 里和这些脚本相关的行。
  4. 为保险起见，证据文本先过一遍 `redact`（`internal/redact`）再发。
- **提示词**（`prompts/roles.go:36`）：把“信息不够就 deny”改成“先看证据；证据里已经有脚本全文、文件归属的，就按证据判断”。

**第二步（审批 agent 的只读调查工具）**

- **改哪里**：`task/approve.go` 的 `Approve`（`:96-107`）。把 `askJSON` 换成一个**有工具的小循环**（复用主管的工具循环，或者照 `inspector` 的写法），最多 4–6 步。
- **工具**：`inspect(command)` 在**发起请求的那台机器**、那个 Run 的 worktree 里执行，走现成的 `node.request machine.look`（`node/look.go`），因此自动继承：
  - `ModeReadOnly` 的 Judge（只有 Read 类能跑，硬信号拒绝）；
  - `secretDump`（不打印环境变量和 .env）；
  - 20 秒超时和 16 KB 输出上限。
  
  再加一个 `read_file(path, max_bytes)`：限定在 worktree 加系统临时目录里，走 `project_read` 现有的 `isGitName` 等防护。
- **审计**：每次调查都写进审批的 note（“查过：cat e2e.sh、git status”），界面上能看到。
- **安全性论证**：
  - 最坏的情况是审批 agent 被 detail 里的提示注入诱导，去读不该读的东西。它能读的范围就是 machine_look 的范围（只读、不读凭据路径、不打印环境变量），而主管现在已经拥有这个能力，所以**没有增加新的暴露面**。读到的内容只进审批模型的上下文，不发到别处。
  - 审批 agent 仍然不能执行任何写操作。它的结论仍然要 `high` 才能放行（P8 另行讨论）。`aiNever` 和硬信号的转用户路径都不变。
  - 成本上限：每次审批最多 N 步、每个 Run 的调查次数有上限，超过就退回到现在的行为。
- **预计效果**：样本中 5 条拒绝里，第 8、9 条（共 3 次）可以直接批准（脚本是本任务刚写的，内容一看就清楚）；第 11 条（dig）它可以先核实“hub.db 在不在本地”，不会基于错误前提拒绝。也就是 **5 次拒绝里 3–4 次不必发生**，每次拒绝省掉的是 agent 的一整轮返工，而不仅是一次点击。

### 4.3 P3：`scriptPreview` 剥掉前缀（P2 第一步的最小版本，可以马上做）

- **改哪里**：`node/outcome.go:160-175`。
- **改什么**：按（P1 之后的）分隔符拆开命令，跳过 `cd`、`export`、`VAR=…` 和 wrapper（`timeout`、`env -u`，可以复用 `policy.unwrap`），找到第一个 `sh/bash/解释器/./x` 部分；相对路径按前面 `cd` 的目录解析（`cd` 到 worktree 以外就不预览）。
- **安全性论证**：只是给审批 agent 多一些信息，不改变任何决定的走向。读取范围仍然限定在 worktree 和系统临时目录（`:191-201`）。
- **预计效果**：同 4.2 的 3 次拒绝。

### 4.4 P4：硬信号分成“只读”和“写/外发”两档

- **改哪里**：`policy/assess.go` 的 `Assessment`（`:13`）加一个 `HardRead []string`；`hardSignals` 里第 1 条（凭据）、第 5 条（持久化）拆分；`Judge`（`:322`）。
- **改什么**：
  - 只有当整条命令的 category 是 **Read**（在 P1 之后，Read 判断才可靠），并且命中的是“凭据位置”或“持久化位置”的**路径**（不是 `security find-`/`export-` 这类会吐出秘密的命令）时，才归为 `HardRead`。
  - `Judge`：auto 模式下，`HardRead` 交给审批 agent（并在 note 里标明“只读访问敏感位置”），不再直接给用户。default 模式不变（仍然问用户）。readonly 档（machine_look）里，`crontab -l`、`cat /etc/crontab`、`ls /etc/cron.d`、`systemctl list-timers` 这类**目录或列表**放行；`~/.ssh`、`Library/Keychains` 仍然拒绝。
  - 修正第 3 条“远程登录”：要求 `ssh` 前面是命令边界、后面不是 `--`、`-u` 的值；或者在 P1 之后改成只匹配命令名 `ssh`。
  - 修正 `installSource` 的 `-i`：只在 Install 类别那一段的参数上匹配，不在整条命令上匹配。
- **保留（不降级）**：`~/.ssh/id_*`、`/etc/shadow`、`security find-generic-password`、`security dump-keychain`、`security export` 以及一切外发（第 2 条），仍然交给用户。
- **安全性论证**：最坏的情况是审批 agent 批准了 `cat ~/.zshrc`，而 `.zshrc` 里 export 了 API Key，内容进了**编程 agent 的上下文**。这比现状差一点，但范围有限：只是本机已登录用户的数据进了本机 agent 的上下文；任何把它发出去的动作仍然命中第 2 条，交给用户。需要提示词明确：“读家目录配置只为找 PATH、版本的，要求 agent 用 `grep -n '^export PATH'` 这样的窄读法”，审批 agent 有了 P2 的工具也能自己先看一眼。
- **预计效果**：样本中第 1、3、6 条（3 次交给用户）改为审批 agent 判断。和 P1 合起来，交给用户的次数 **7 → 1**（只剩第 2 条的同类误报，P4 的 `-i` 修正也会把它去掉）。

### 4.5 P5：`classifyCommand` 记住 `cd`

- **改哪里**：`policy/policy.go` 的 `classifyCommand`（`:371-376`）：按顺序处理各部分时，遇到 `cd <worktree 内的相对路径>`，就把后续部分的 `workdir` 换成那个子目录，同时保留原来的 worktree 根作为“内外”的判断基准。
- **安全性论证**：`outside()` 的判断基准仍然是 worktree 根，只是相对路径从正确的位置开始算。`cd` 到外面的情况已经由 `:498-503` 判成 outside。不能确定的写法（`cd $X`、`cd -`）就保持现在的行为（按根算）。
- **预计效果**：13 次 outside-worktree 里第 13 类 2–3 次。

### 4.6 P6：sous 自己的 MCP 工具直接放行

- **改哪里**：`policy/policy.go` 的 `Classify`（`:224-255`）之前，或者 `node/run.go` 的 `onPermission`：标题是 `mcp__sous__(report_progress|report_result|ask_supervisor)` 的请求 → Read（或者直接 Allow 并 `recordPolicy`）。更稳妥的做法是在 `internal/agents` 里给 Claude 的会话预设 allow 规则，让这几个工具根本不发权限请求。
- **安全性论证**：这几个工具只把文字发给 sous 自己（daemon 的 IPC，每个 Run 一个随机令牌，`node/sous_tools.go`），没有本机副作用。按工具名精确匹配，其他 MCP 工具不受影响。
- **预计效果**：每个 Run 少 1–3 次审批 agent 调用（样本：2/41）。

### 4.7 P7：扩大 `readOnly` 和只读子命令表（主要为了服务器上的 machine_look）

- **改哪里**：`policy/policy.go:746-757`（`readOnly`）、`:639-642`（systemctl/launchctl 子命令）、`classifyBase` 的 switch。
- **建议加入**（全部是只读，或者只读参数形式）：
  - 直接加进 `readOnly`：`hostname`、`who`、`w`、`last`、`lastb`、`lsblk`、`lscpu`、`getent`、`dig`、`nslookup`、`host`、`zcat`、`zgrep`、`xxd`（不带 `-r`）、`column`、`tr`、`nl`、`od`、`strings`。
  - 带子命令限制：`journalctl`（拒绝 `--vacuum-*`、`--rotate`、`--flush`）；`ip`（只允许 `addr/a/route/r/link/l` 加 `show`，或者没有动词）；`iptables`/`ip6tables`/`nft`（只允许 `-S`、`-L`、`list ruleset`；**注意它们通常需要 root，在非 root 用户下会报错，这是正常的**）；`ufw status`；`nginx -t`/`-T`（`-T` 会打印完整配置，可能含密钥路径，建议只放 `-t`）；`crontab -l`（不带 `-r`、`-e`，同时从“持久化”硬信号里豁免）；`systemctl list-timers`/`list-unit-files`/`is-enabled`/`get-default`；`docker stats --no-stream`、`docker top`；`git` 的 `rev-list`/`shortlog`/`reflog`（show）；`sous status`、`sous version`、`sous --help`。
  - `dig`/`nslookup`/`host` 有一次对外的 DNS 查询（不带本机数据）。如果把它看作外发，可以只放进 auto 模式下的 ExecSafe，不放进 readonly。
- **安全性论证**：这些命令本身不改变状态。对有破坏性形态的命令（journalctl、iptables、crontab），按参数白名单放行，**其他形态仍然落到 exec-other**。machine_look 还有 `secretDump`、超时和输出上限。
- **预计效果**：用户列出的服务器排查命令（journalctl、crontab -l、iptables -S）全部变成可执行；在编程 agent 的 auto 模式里，这些命令也不再找审批 agent。

### 4.8 P8：低风险类别里 `medium` 也能放行

- **改哪里**：`task/approve.go:109-115`。
- **改什么**：`sure` 改为“high，或者 medium 且 category 属于 {exec-other、install（默认源）、outside-worktree（只读）、delete（worktree 内）}，并且没有软信号”。git-remote、service 仍然要 high。
- **安全性论证**：最坏的情况是审批 agent 以 medium 的把握批准了一个 worktree 内的可逆操作。这些类别的操作都能靠 git 或重装恢复（这也是提示词要求批准的前提）；不可逆的、硬信号的已经在更前面的分支交给用户；另外还有 `maxAIAllowsPerRun` 兜底。建议配合 P2 一起做：有了取证，medium 会少很多。
- **预计效果**：取决于“把握不够”在 `escalation` 里的占比，要用附录 A 的第 2 条 SQL 来看。**本机样本里看不到 escalation，不估算。**

### 4.9 P9：同一任务里复用用户的 `allow_always`

- **改哪里**：`task/approve.go:231-234`（`followUser`），以及 `userPrecedent`（`:158-167`）。
- **改什么**：
  1. 用户上一次选的选项 kind 是 `allow_always` 时，`followUser` 自动放行同一操作（选 `allow_once` 时仍然不复用，保持现有语义）。
  2. `userPrecedent` 的匹配从“detail 完全相同”放宽到“category + 规范化后的命令（去掉 rawInput 里的 `description`、`timeout` 等非命令字段）相同”。
- **安全性论证**：用户选 `allow_always` 本来就表示“这类操作以后都可以”，在同一个任务里复用，没有超出用户给的授权。范围限定在 task_id 以内，不跨任务。最坏的情况是 agent 在两次请求之间改了脚本内容，但命令文本没变。所以带脚本预览的请求要把**脚本内容的哈希**也算进匹配键。
- **预计效果**：取决于用户选 `allow_always` 的频率，要用附录 A 的 SQL 来看。

### 4.10 P10：`maxAIAllowsPerRun`

- **改哪里**：`task/approve.go:21`、`:72-85`。
- **改什么**：改成按类别计数（exec-other 300，outside-worktree、install、delete 各 100），或者统一提高到 300。
- **安全性论证**：这个上限的作用是“让人看一眼停不下来的 Run”。巡检 agent（repeat、stalled、long、tokens）和 node 的 `stuck`（同一工具连续失败 4 次）已经能发现停不下来的 Run，这个上限只是第二道保险。
- **预计效果**：样本里一个 Run 最多 26 次，**碰不到 150**。只有附录 A 里出现“已批准 N 个”时才值得做。优先级最低。

---

## 5. 明确不建议动的

| 机制 | 位置 | 理由 |
|---|---|---|
| 底线规则（`rm -rf /`、mkfs、dd 到磁盘、fork bomb、关机、`curl \| sh` 等） | `policy.go:135-149` | 后果不可恢复、范围是整台机器，没有任何任务需要它们。样本里没有一次是底线规则造成的摩擦，放宽没有收益，只有风险。 |
| `irreversible`、`floor` 不交给审批 agent | `approve.go:32`、`:55-56`；`policy.go:90-91` | push --force、删分支、publish、sudo 一旦做错，没法靠 git 或重装恢复。审批 agent 的判断依据是“可逆”，这一类正好在它的能力边界之外。样本里唯一的一条 irreversible（第 4 条）是 heredoc 误报，应该靠 P1 修正分类，而不是放开这一类。 |
| 服务器角色上的删除、服务、不可逆、编辑仍然交给用户 | `policy.go:122-128` | 服务器上是线上服务和别人的数据，worktree 和快照的“可恢复”前提不成立（ops 任务是就地执行的）。决策 0014 的取舍仍然成立。服务器上的**查看**摩擦用 P1 和 P7 解决，不需要动这里。 |
| 拒绝的审计记录 | `node/run.go:793-796`、`:807-809`（`recordPolicy`、`noteRefusal`）；`run.ended.refused` | 这是这次调查唯一能拿到的“拒绝理由”数据，也是 `followUser` 和返工提示的输入。放宽策略之后更需要它们来复盘。 |
| “向外发送数据”的硬信号 | `assess.go:71-76` | 外发会把数据带出机器，无法撤回。P4 只降级“只读本机敏感位置”，外发不降级。 |
| machine_look 的 `secretDump` 和 readonly 档 | `node/look.go:28`、`:49` | 输出会进主管的上下文，并保存在对话历史里。P2 也复用这一层，所以它反而需要保持严格。 |

---

## 6. 查过但没有问题的模块

- `policy.EffectiveMode`、`DecideFor`（`policy.go:106-131`）：逻辑和注释、决策 0014 一致，server 上的 open 确实降级了。
- `routine`（`assess.go:230-274`）：样本里没有 git-remote 和产物删除的摩擦，规则本身清楚，没有发现误判。
- `Judge` 在 restricted 模式下“有硬信号就拒绝”（`assess.go:314-316`）：行为正确；问题出在硬信号的误报（P1、P4），不在这条分支。
- `approve.go` 的 `aiNever`、`maxAIDeniesPerOp`、模型不可用、调用失败放弃：分支都正确，样本里没有触发。
- `node/run.go` 的三个入口都走 `r.judge`（`run.go:787`、`workbench.go:176`）：一致，没有绕过的入口。
- `node/look.go` 的超时、输出上限和 `secretDump`：工作正常（实测的拒绝都发生在 Judge，没有一条发生在 secretDump）。
- `ps aux | grep x`、`grep … /etc/ssh/sshd_config`（不带引号里的 `|`）在当前代码上是**放行**的，`isSystemPath` 覆盖了 `/etc/`。

---

## 附录 A：在 hub 服务器上跑的只读 SQL

```sh
sqlite3 'file:<data_dir>/hub.db?mode=ro' <<'SQL'
.mode column
-- 1. 总数与分组
SELECT COUNT(*) FROM approvals;
SELECT category, COUNT(*) n FROM approvals GROUP BY 1 ORDER BY n DESC;
SELECT review, COUNT(*) FROM approvals GROUP BY 1;
SELECT decided_by, status, COUNT(*) FROM approvals GROUP BY 1,2;
-- 2. 转人工的原因（escalation 为空且 review=user = node 端硬信号或 default 模式）
SELECT CASE
  WHEN escalation='' THEN '(node 直接交用户)'
  WHEN escalation LIKE '%把握不够%' THEN '把握不够'
  WHEN escalation LIKE '%没有配置审批模型%' THEN '没有审批模型'
  WHEN escalation LIKE '%已经批准了%' THEN '已批准满额'
  WHEN escalation LIKE '%拒绝%次%' THEN '同一操作被拒 N 次'
  WHEN escalation LIKE '%不交给审批 agent%' THEN 'aiNever'
  WHEN escalation LIKE '%倾向拒绝%' THEN '倾向拒绝(low)'
  WHEN escalation LIKE '%调用失败%' THEN '模型调用失败'
  ELSE '其他：' || substr(escalation,1,30) END why,
  COUNT(*) n, ROUND(100.0*COUNT(*)/SUM(COUNT(*)) OVER (),1) pct
FROM approvals WHERE review='user' GROUP BY 1 ORDER BY n DESC;
-- 3. 各类别转人工率
SELECT category, COUNT(*) total, SUM(review='user') to_user,
       ROUND(100.0*SUM(review='user')/COUNT(*),1) pct
FROM approvals GROUP BY 1 ORDER BY to_user DESC;
-- 硬信号分布（detail 第一行）
SELECT substr(detail,1,instr(detail||char(10),char(10))-1) sig, COUNT(*) n
FROM approvals WHERE detail LIKE '高风险信号%' GROUP BY 1 ORDER BY n DESC LIMIT 20;
-- 4. 被拒操作的命令前缀 TOP（命令名 + 第一个参数）
SELECT substr(title,1,instr(title||' ',' ')-1) cmd, COUNT(*) n FROM approvals
WHERE status='decided' AND option_id LIKE '%reject%' GROUP BY 1 ORDER BY n DESC LIMIT 20;
SELECT decided_by, substr(note,1,80), COUNT(*) n FROM approvals
WHERE status='decided' AND option_id LIKE '%reject%' GROUP BY 1,2 ORDER BY n DESC LIMIT 30;
-- allow_always 的使用频率（P9）
SELECT COUNT(*) FROM approvals WHERE option_id LIKE '%always%' OR option_id LIKE '%updates%';
-- 5. Run 失败聚类
SELECT status, COUNT(*) FROM runs GROUP BY 1;
SELECT CASE
  WHEN error LIKE '%额度%' OR error LIKE '%quota%' OR error LIKE '%Upgrade%' THEN '额度'
  WHEN error LIKE '%限流%' OR error LIKE '%rate%' THEN '限流'
  WHEN error LIKE '%登录%' OR error LIKE '%auth%' THEN '未登录'
  WHEN error LIKE '%拒绝%' THEN '被拒绝'
  WHEN error LIKE '%超时%' OR error LIKE '%timeout%' THEN '超时'
  WHEN error LIKE '%worktree%' OR error LIKE '%准备%' THEN '准备目录'
  WHEN error LIKE '%用户取消%' THEN '用户取消'
  ELSE substr(error,1,40) END k, COUNT(*) n
FROM runs WHERE status NOT IN ('succeeded','running','queued') GROUP BY 1 ORDER BY n DESC;
SQL
```

注意：`option_id` 是 agent 自己定义的（Claude 是 `allow-once`、`allow-with-updates`、`reject`），按 kind 统计更准确的办法是用 `json_each(options)` 和 `option_id` 连接。上面按名字匹配，只作近似。
