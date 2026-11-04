# 远程管理机器上的 agent：设计

状态：已实现（2026-11-04），只做现有五个 agent，不加 Gemini。标“待实测”的地方还没在真实机器上确认，见文末的实测记录。

实现时的决定（和下文草稿不同的地方）：
- 登录和安装一律在伪终端里运行（`aymanbagabas/go-pty`，macOS / Linux 用 pty，Windows 用 ConPTY），页面用 xterm.js 显示，可以直接操作交互式菜单（如 `opencode auth login`）。管道在 Claude 这类 Ink 界面下不可靠，所以没有做“先管道、不行再伪终端”的两级方案。
- 没有单独的 `recipes` 接口：要运行的命令随 `AgentInfo` 的 `install`、`upgrade`、`login`、`logout` 字段上报，确认框直接显示。
- `AgentInfo.job` 报告正在进行的作业，页面刷新后还能接上终端。
- 编辑 node.json 时，`agent_env` 里的值也显示成 `***`，保存时保持原值。

## 1. 总体

```
PWA ──HTTP──▶ Hub ──node.request──▶ daemon
    ◀──/ws───     ◀─node.response──
                  ◀─node.stream────  （安装、登录的输出，只转发不存）
```

新增一套 Hub → Node 的请求 / 应答，所有管理操作都走它：

| op | 作用 | 同步 / 作业 |
|---|---|---|
| `agents.detect` | 立刻重新检测，返回 `[AgentInfo]`，同时照常发 `inventory` | 同步 |
| `agents.probe` | 启动 agent、`initialize` + `session/new`（临时目录），读 `configOptions` 后关掉 | 同步，超时 60 秒 |
| `agents.install` | 执行内置安装命令 | 作业 |
| `agents.login` | 执行内置登录命令 | 作业（可写 stdin） |
| `agents.logout` | 执行内置退出命令 | 同步 |
| `job.input` | 给正在运行的作业写一行 stdin | 同步 |
| `job.cancel` | 结束作业 | 同步 |
| `config.read` | 读一个内置配置文件，返回内容和 sha256 | 同步 |
| `config.write` | 带上读到的 sha256 写回；不一致返回冲突 | 同步 |
| `agents.set_defaults` | 写 `node.json` 的 `agent_options` | 同步 |
| `agents.set_env` | 写 / 删 `node.json` 的 `agent_env`（API Key） | 同步 |

## 2. 协议（core-hub-node 新增）

| 方向 | 类型 | body | 需要确认 |
|---|---|---|---|
| H→N | `node.request` | `{op, args}`，帧 `id` 作请求 id | — |
| N→H | `node.response` | `{req_id, ok, result?, error?: {code, message}}` | ❌ |
| N→H | `node.stream` | `{job_id, stream: "out"\|"err", chunk}` | ❌ |
| N→H | `node.job_ended` | `{job_id, op, agent, exit_code, error?}` | ✅ |

- 请求和应答不进发件箱：断线时 Hub 让等待中的 HTTP 请求返回 503（`offline`），页面提示重试。只有作业的结束要可靠上报（审计、页面状态）。
- Hub 在 `nodehub` 里维护 `pending map[reqID]chan Response`，默认超时 20 秒，`agents.probe` 60 秒。
- 旧版 daemon 不认识 `node.request` 会忽略它：Hub 看 `hello.version`，旧版本直接返回 `unsupported`，页面提示先更新 sous。更好的做法是 `hello` 加 `caps: ["admin"]`，按能力判断而不是按版本。
- daemon 收到请求时先看 `remote_admin`，为 `false` 就回 `{ok:false, error:{code:"disabled"}}`。

## 3. Agent 注册表扩展（`internal/agents`）

`Spec` 新增：

```go
Install  map[string]Recipe // key: darwin / linux / windows / "*"
Upgrade  map[string]Recipe // 可空，空时用 Install
Login    *Recipe           // 设备码 / 打印链接的登录方式
Logout   *Recipe
Requires []string          // 前置命令，如 "npx"
Configs  []ConfigFile      // 可在页面编辑的文件
KeyEnv   []string          // 允许用 API Key 登录时设置的环境变量名
```

`Recipe{Argv []string; Env []string; Shell bool}`：`Shell` 为真时用 `sh -c` / `powershell -NoProfile -Command` 执行一条固定字符串（官方安装脚本都是 `curl … | bash` / `irm … | iex` 形式）。命令全是代码里写死的常量，请求里只带 agent 名，不带命令。

`ConfigFile{ID, Path, Format}`：`Path` 以 `~` 开头，在 daemon 上展开；`Format` 是 `json` / `jsonc` / `toml` / `text`，决定保存前的校验。

内置条目（全部待实测，确认后写进 design 的实测表）：

| agent | 安装（macOS / Linux） | 安装（Windows） | 登录 | 配置文件 |
|---|---|---|---|---|
| claude | `curl -fsSL https://claude.ai/install.sh \| bash` | `irm https://claude.ai/install.ps1 \| iex` | `claude setup-token`（打印链接，需回填授权码） | `~/.claude/settings.json` |
| codex | `npm i -g @openai/codex` | 同左 | `codex login --device-auth` | `~/.codex/config.toml` |
| cursor | `curl https://cursor.com/install -fsS \| bash` | `irm 'https://cursor.com/install?win32=true' \| iex` | `cursor-agent login`（`NO_OPEN_BROWSER=1`，打印链接） | `~/.cursor/cli-config.json` |
| opencode | `curl -fsSL https://opencode.ai/install \| bash` | `npm i -g opencode-ai` | `opencode auth login`（交互式选服务商，见 §5 风险） | `~/.config/opencode/opencode.json` |
| kiro | `curl -fsSL https://cli.kiro.dev/install \| bash` | 待查 | `kiro-cli login --use-device-flow` | 待查（`~/.kiro/settings/cli.json`？） |

- 安装脚本是 sous 自己执行的固定命令，不经过 `internal/policy`（底线规则里的 `curl … | sh` 针对的是 agent 发起的命令）。页面确认框里显示完整命令。
- `Requires` 不满足时（如没有 `npx`），安装按钮旁说明缺少 Node.js，本期不自动安装 Node.js。
- daemon 作为系统服务运行时 PATH 可能和用户 shell 不同：安装后新二进制常在 `~/.local/bin`、`~/.npm-global/bin`。检测时额外查这些目录（`agents.ExtraPaths`），否则会出现“装完仍显示未安装”。

## 4. 作业（安装、登录）

- daemon 用 `exec.Cmd` 加管道运行，stdout / stderr 按块（≤ 8 KB，最多每 100 ms 一块）发 `node.stream`。同一台机器同时只跑一个安装作业；登录作业每个 agent 一个。
- 输出里的 URL 和形如 `ABCD-1234` 的验证码由页面识别并高亮（纯函数，放 `reduce.ts`）。
- 登录作业 10 分钟超时；安装 15 分钟。结束后 daemon 立刻重新检测并发 `inventory`。
- Hub 把 `node.stream` 转成 ephemeral 的 `/ws` 事件 `agent.job.output`（seq 0，不存）；`node.job_ended` 存成事件 `agent.job.ended`。作业开始时 Hub 存 `agent.job.started`（op、agent、谁发起）。
- 页面刷新后看不到之前的输出（不存），只能看到作业状态；需要的话 daemon 保留最后 64 KB，`job.tail` 再取一次。

**风险（待实测）**：部分登录命令要求终端（TTY），在管道下可能直接报错或不打印链接。处理顺序：
1. 先选这些 CLI 的非交互 / 设备码参数（上表）。
2. 不行的在 macOS / Linux 上用伪终端运行（`github.com/creack/pty`），Windows 用 ConPTY（`github.com/UserExistsError/conpty` 或 `x/sys/windows`）。输出要去掉 ANSI 控制序列再发。
3. 仍然不行的 agent 只提供 API Key 和“在这台电脑上运行 `<命令>`”的提示。

## 5. 模型与思考强度

- ACP 的 `session/new` 结果里有 `configOptions`（[规范](https://agentclientprotocol.com/protocol/session-config-options)）：每项 `{id, name, description?, category?, type: "select", currentValue, options}`，`options` 可以是平铺的值或分组。category 为 `model`、`thought_level`、`model_config`、`mode`。用 `session/set_config_option {sessionId, configId, value}` 修改，返回完整的新列表。
- 早期的 claude-agent-acp 用不稳定的 `models` 字段和 `session/set_model`。`acp.Session` 两种都解析：有 `configOptions` 时只用它，否则把 `models` 转成一个 `category: model` 的选项。
- `agents.probe` 在 `~/.sous/scratch/probe-<agent>` 里开一个会话，读完关掉，结果缓存在 daemon 内存里，并随 `inventory` 的 `AgentInfo.options` 上报（只上报 `model`、`thought_level`、`model_config` 三类，不含 `mode`，模式仍由权限模式决定）。
- 默认值存在 `node.json`：

```json
"agent_options": { "claude": { "model": "opus", "effort": "high" } }
```

  键是 config option 的 `id`，值是选项值。`run.drive` 在 `session/new`（或 `session/load`）之后、`set_mode` 之前逐个 `set_config_option`；值已经不在可选列表里时跳过并发一条 `status` 提醒，不让 Run 失败。
- Cursor 现在靠 `--model auto` 覆盖启动命令；改成默认值之后，`design.md` §7 的这条说明要更新。

## 6. 配置文件读写

- 只允许 `Spec.Configs` 里的文件和 `node.json`。`config.read {agent, file_id}`：不存在时返回空内容和 `exists: false`；超过 1 MB 拒绝。
- `config.write {agent, file_id, content, base_sha256}`：
  1. 当前文件的 sha256 和 `base_sha256` 不一致 → `conflict`。
  2. 按格式校验（`json`、`jsonc` 去注释后解析、`toml` 用 `github.com/pelletier/go-toml/v2`）。
  3. 写 `<文件>.sous-bak`（覆盖上一份），再原子替换，保留原权限；新文件 0600。
- `node.json`：读的时候把 `token` 换成 `"***"`；写的时候忽略传入的 `token`、`hub_url`、`node_id` 和 `remote_admin`，保留原值（R6.3：远程不能打开或关闭远程管理）。写入后 daemon 重新加载：重建 `specs`（launch 覆盖、`agent_env`、`agent_options`）、`policy`、并发数（只影响之后的 Run）。
- `agent_env`：`{"claude": {"ANTHROPIC_API_KEY": "..."}}`，追加到 agent 进程环境里。`inventory` 和 `config.read` 只报告设置了哪些变量名，不报告值。

## 7. Hub 的 HTTP 接口（core-hub-http 新增）

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/nodes/{id}/agents/detect` | 重新检测 |
| POST | `/api/v1/nodes/{id}/agents/{agent}/probe` | 读模型等选项 |
| POST | `/api/v1/nodes/{id}/agents/{agent}/install` | `{upgrade?: bool}` → `{job_id}` |
| POST | `/api/v1/nodes/{id}/agents/{agent}/login` | → `{job_id}` |
| POST | `/api/v1/nodes/{id}/agents/{agent}/logout` | |
| PUT | `/api/v1/nodes/{id}/agents/{agent}/defaults` | `{options: {id: value}}` |
| PUT | `/api/v1/nodes/{id}/agents/{agent}/env` | `{name, value}`，`value` 为空表示删除 |
| GET | `/api/v1/nodes/{id}/agents/{agent}/recipes` | 将要执行的安装 / 登录命令（给确认框显示） |
| POST | `/api/v1/nodes/{id}/jobs/{job}/input` | `{text}` |
| POST | `/api/v1/nodes/{id}/jobs/{job}/cancel` | |
| GET | `/api/v1/nodes/{id}/files/{agent}/{file}` | `{content, sha256, exists, path, format}`；`agent` 为 `sous` 时是 `node.json` |
| PUT | `/api/v1/nodes/{id}/files/{agent}/{file}` | `{content, base_sha256}`，冲突 409 |

- 机器离线 409 `offline`，daemon 关闭了远程管理 403 `disabled`，daemon 太旧 409 `unsupported`。
- `/ws` 新事件：`agent.job.started`、`agent.job.output`（不存）、`agent.job.ended`、`node.config_changed`（只有 agent、文件 id，不含内容）。

## 8. 页面（apps/web）

- `Machines.svelte` 的 agent 表格改为列出全部 agent。每行：名称、版本、登录状态、默认模型 / 思考强度；操作：安装 / 升级、登录 / 退出、设置。
- 新组件 `AgentPanel.svelte`（从机器卡片打开的 Sheet），分区：
  - 状态：安装、登录、缺少的前置条件、说明。
  - 模型与思考：按 category 渲染下拉框；首次打开时调 `probe`（显示“正在读取…”）；保存调 `defaults`。
  - API Key：列出 `KeyEnv` 里的变量，显示“已设置 / 未设置”，可以设置或清除。
  - 配置文件：文件列表，点开进 `ConfigEditor.svelte`（等宽 `<textarea>`，不引入编辑器库），保存时处理 409 冲突（提示重新加载）。
- 新组件 `JobLog.svelte`：显示作业输出，高亮链接和验证码，登录作业下方有一行输入框（`isComposing` 时 Enter 不提交）。
- 作业输出的累积、链接 / 验证码识别写成 `reduce.ts` 里的纯函数并配测试。

## 9. 测试

- `acptest` 假 agent 加 `configOptions` 和 `set_config_option`（新模式 `options`），测 probe 和 Run 开始时套用默认值。
- `internal/agents`：Recipe 按系统选择、`ExtraPaths`。
- `internal/node`：作业（假的安装脚本：输出几行后退出 / 读一行 stdin 再退出）、`config.write` 的冲突、校验、备份、`node.json` 屏蔽字段和热加载、`remote_admin: false`。
- `nodehub`：请求超时、断线时 pending 请求返回、`unsupported`。
- e2e：在 `e2e_test.go` 的框架里跑一次 “detect → probe → set defaults → Run 套用默认值”。
- 真实 agent 的安装、登录在 macOS 上逐个实测，结果写进本文件的实测表；Windows / Linux 至少各测一个。

## 10. 文档

完成后更新：`docs/api/core-hub-node.md`、`docs/api/core-hub-http.md`、`apps/core/AGENTS.md`（结构里加 admin 相关包、`node.json` 新字段）、`apps/web/AGENTS.md`（如有新约定），并在 `docs/decisions/` 加一条记录（远程管理的安全边界：固定命令和固定文件，可在本地关闭）。

## 11. 实测记录（2026-11-04，macOS）

| agent | 读取选项（`agents.probe`） | 登录（伪终端） |
|---|---|---|
| Claude Code（claude-agent-acp 0.100.0） | ✅ `model`（default、opus、sonnet、haiku 等 5 个）、`effort`（thought_level：default 到 max）、`ultracode`（model_config：on / off）；未登录时也能开会话 | ✅ `claude auth login` 打印授权链接，提示“Paste code here”；同时会在这台电脑上打开浏览器 |
| Cursor | ✅ 只有 `model`，47 个值，值里带参数（如 `claude-opus-5[thinking=true,effort=high]`） | 未测（已登录，避免把登录弄掉） |
| OpenCode | ✅ `model`（46 个）、`effort`（thought_level，7 个） | ✅ `opencode auth login` 的选择菜单在 xterm 里正常显示，可以用方向键操作 |
| Codex | 本机没装，未测 | 未测 |
| Kiro CLI 3.0.0 | ✅ `model`（auto 和 Claude、GPT 等十几个）、`memoryReflection`、`autopilot`、`contentCollection`，另有 `mode`（vibe、spec、plan…，是工作流不是权限级别） | ✅ `kiro-cli login --use-device-flow`：先选登录方式（菜单），再给出验证码和链接，用户在浏览器里确认后退出码 0 |

浏览器验收（2026-11-04，连接本机调试用的 Chrome，单独的 Hub 和 daemon home）：
- 机器列表列出全部 agent，未安装的显示“安装”，未登录的显示“登录”，默认模型和思考强度显示在版本后面；机器离线时隐藏管理操作，面板说明原因，接口返回 409 `offline`。
- 从页面安装 Kiro CLI：确认框显示命令，终端实时输出，中途刷新页面后列表显示“正在安装…”并接上终端，装完自动检测为“未登录”。
- 从页面登录 Kiro：终端里的菜单用屏幕按键选择，验证码和链接被单独列出；用户在浏览器里完成登录后显示“已登录”。OpenCode 的登录菜单方向键可用，“停止”按钮结束作业并显示“已停止”。
- 给 Claude Code 设默认模型 Haiku、思考强度 Low，真实 Run 结束后会话选项就是这两个值。
- API Key：保存后 node.json 里有值，Hub 的接口和 node.json 编辑器里都看不到值；清除后从 node.json 删掉。
- 配置文件：`~/.claude/settings.json` 写坏的 JSON 不能保存，原文件不变；关闭有修改的编辑器会先确认。node.json：改并发数可以保存，令牌和 `remote_admin` 改不了，留了备份；机器上的文件被改过之后保存提示冲突，不覆盖，重新读取后可以保存。
- 手机宽度（390px）下面板变成底部弹层，选项和 API Key 各占一行。

验收中发现并修掉的问题：
- `--home` 用相对路径时，Run 的工作目录也是相对路径，Claude 拒绝开会话。现在 `Root()` 转成绝对路径。
- 页面刷新后正在进行的作业找不回来：`AgentInfo.job` 报告它，列表和面板都会接上。
- “读取可选项”之后要等下一次 inventory 才显示：现在直接用 probe 的结果。
- 只有字母的设备码（Kiro 的 `WQXD-ZDRP`）没被认出来。
- 用户停止的作业显示成“失败（退出码 -1）”，现在是“已停止”。
- **Kiro 的 `autopilot` 默认开启，会不请求权限就执行 shell 命令，绕过 sous 的策略**。现在默认和自动审批模式下把它设为 off（设置失败则 Run 失败），完全开放模式下设为 on；关掉后 Kiro 会为写文件请求审批。但 Kiro 仍按 `~/.kiro/settings/permissions.yaml` 自己放行（本机这个文件允许所有操作），页面上提示这一点，并可以编辑这个文件。

- 其他四个 agent 的安装命令还没有在真实机器上跑过。
- Windows 和 Linux 未实测；Windows 的 ConPTY 只验证了能编译。
