# 0012 从页面远程管理机器上的 agent，只跑内置的命令和文件

- Status: Accepted
- Date: 2026-11-04

## Context

机器配对之后，装 agent、登录、换模型、改 agent 的配置，原来都要到那台电脑上操作。用户希望在 PWA 里完成。可是在 Hub 上能做这些事，就等于能在那台机器上运行程序、改它的文件：Hub 或登录会话一旦被盗用，后果比派一个 Run 更直接。

## Decision

- 远程管理默认开启。每台机器可以在本地 node.json 里设置 `remote_admin: false` 关掉，远程不能把它重新打开。
- 请求只带 agent 名和文件 id。要运行的安装、升级、登录、退出命令和能编辑的配置文件，都写死在 `internal/agents` 里；API Key 只能设置每个 agent 列出的变量名。不提供网页终端，也不能指定任意命令或路径。
- 安装和登录在伪终端里运行，页面用 xterm.js 显示，用户可以像在终端里一样回答提示（选菜单、粘贴授权码）。优先使用不需要本机浏览器的登录方式（设备码、打印链接）。
- 模型和思考强度走 ACP 的 `configOptions` / `session/set_config_option`，每台机器每个 agent 一组默认值，存在 node.json。不按任务单独选模型。
- 设备令牌和 API Key 不离开机器：inventory 只报告变量名，编辑 node.json 时显示成 `***`。事件日志记录谁在哪台机器上做了什么，不记录密钥和文件内容。

## Alternatives

- 网页终端（任意命令）：最灵活，但等于把机器的 shell 交给 Hub，放大了被盗用时的风险。
- 只支持 API Key、不做交互式登录：订阅账号（Claude、Cursor、Kiro）只能走 OAuth，体验差。
- 改各家 agent 的配置文件来选模型：每家格式不同，ACP 的 `configOptions` 能统一，选项也随 agent 版本自动更新。

## Consequences

- 内置命令随 agent 官方的安装方式变化，需要实测后维护。Kiro CLI 没有内置 Windows 的安装方式。
- 能编辑 agent 配置文件，就能改它的 hooks、MCP 之类的设置，间接在那台机器上运行程序；这和“完全开放”模式的风险同级（见 0010）。不想承担的机器应关闭远程管理。
- 不报告 `configOptions` 的 agent，只能编辑它的配置文件来选模型。
- agent 自己的权限配置仍然生效：Kiro 的 `autopilot` 由 sous 在默认和自动审批模式下关掉，但 `~/.kiro/settings/permissions.yaml` 里放行的操作照样不经过 sous（2026-11-04 实测）。页面上提示这一点，并可以直接编辑这个文件。
