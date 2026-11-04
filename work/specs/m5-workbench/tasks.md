# M5 工作台：任务

需求见 `requirements.md`，设计见 `design.md`。每一组结束时都能跑起来、能验收。

## 当前状态（接手先读这里）

- **进度**：0～4 组的代码、测试和本地验收都已完成（2026-11-04）。本地验收用一个 Hub、两台 daemon（开发机 + `--role server` 的服务器）、假 agent、假 `gh` 和真实的 `wok`，在内置浏览器里跑通：工作空间扫描 → 登记发现的仓库 → 前端岗位改代码 → 审批 → 验收 → push 和 PR → 模拟合并 → 自动在服务器上部署（服务器上的命令交给用户审批）→ 新建 wokspace 项目；桌面、手机宽度、深色模式都看过。
- **真实模型验收**（2026-11-04，OpenCode Go 的 `deepseek-v4.1-flash`，三台 daemon：两台开发机和一台服务器）：在对话里让主管登记项目和部署目标（`project_put`）、clone 到第二台开发机（`project_clone`）、新建项目（`project_create`）、在项目页说“让前端岗给这个项目加 hello.txt”（上下文和岗位都对）、真实模型验收、PR、说“部署到服务器”建出运维任务，全部跑通；之后 PR 被看到已合并时，没有重复部署。
- **没验收的**：真实 Linux 服务器上的 `sous service install`、`enable-linger` 和 agent 登录。
- **和设计不同的地方**见 `design.md` 末尾的“实现说明”。
- **先读**：`requirements.md` 的“核心使用逻辑”、`design.md`、`work/specs/sous-mvp/tasks.md` 的当前状态（M4 遗留的用户操作）、`apps/core/AGENTS.md`、`apps/web/AGENTS.md`。

## 0 确认

- [x] 用户确认需求和设计（2026-11-04 用户要求全面完成 M5）
- [x] 写决策记录 0013（岗位档案）、0014（服务器角色与运维任务）
- [x] 更新 `docs/api/` 契约（`core-hub-http.md`、`core-hub-node.md`）

## 1 工作空间和项目

- [x] `internal/project`：remote 规范化，带单元测试
- [x] 迁移：`projects.remote_key`、`project_nodes.source`、`repo_scans`、内置“杂项”项目
- [x] node：`workspaces` 配置、扫描、`node.repos` 上报（变化才报），带测试
- [x] hub：`node.repos` 投影，自动生成和清理 `scan` 来源的项目位置
- [x] clone：`purpose: clone` 的 Run（经本地策略审批），完成后重扫并记下位置
- [x] 扫描时识别 wokspace，记到 `projects.layout`；inventory 上报 `wok` 版本
- [x] 新建：`purpose: create` 的 Run，默认 `wok init` + `wok check`，可选 `gh repo create`（经本地策略审批）
- [x] 主管工具：`project_put`、`project_create`、`project_clone`（项目列表在每轮的环境说明里）；任务必须属于一个项目；提示里写明新建项目默认用 wokspace
- [x] 验收：场景 1、2、4、4a 在浏览器里跑通（两台开发机）

## 2 岗位

- [x] 迁移和事件：`personas`、`tasks.persona_id`，内置默认岗位
- [x] router：按岗位选 agent，机器角色过滤，带测试
- [x] `run.start` 带岗位说明和模型选项，Node 按 `configOptions` 设置
- [x] 主管工具：`task_create` / `task_update` 的 `persona`；岗位清单在每轮的环境说明里
- [x] HTTP 接口：岗位的增删改查
- [x] 验收：场景 3 的后端部分（任务记录岗位，Run 带岗位说明和模型选项；真实模型在对话里按岗位派活也跑通了）

## 3 服务器和部署

- [x] 机器角色：配对时选择、可修改、同步到 `node.json`
- [x] policy：服务器上禁止 `open`、`service` 类命令、`irreversible` 和 `delete` 交给用户，带测试
- [x] 运维任务：`kind: ops`，就地执行，引擎跳过验收和交付
- [x] 部署目标：存储（`projects.deploy`）、接口、运维 Run 的提示
- [x] 合并后自动部署：PR 状态检查和触发
- [ ] 在真实 Linux 上实测：`sous service install`、`enable-linger`、agent 登录（`docs/guides/connect-machine.md` 已补全，等真机）
- [x] 验收：场景 5（假 agent + 假 `gh` 模拟合并）

## 4 界面

- [x] 状态视觉表（`lib/`），所有页面共用
- [x] 外壳：Rail、Workbench 路由、ChatPanel、SettingsModal；旧的侧边栏入口搬进设置
- [x] `GET /api/v1/overview` 和 Overview 矩阵
- [x] ProjectBoard（看板 + 阶段进度条）
- [x] TaskView（合并 TaskDetail 和 RunCard 的步骤）
- [x] ChatPanel：RunChip 代替 RunCard、历史下拉、上下文标签（含后端 `context`）
- [x] 设置：岗位分栏、机器分栏的角色和工作空间
- [x] 手机：底部标签、总览卡片、全屏抽屉
- [x] 验收：场景 3 的界面部分、场景 6（桌面和手机宽度都检查，浅色和深色）

## 收尾

- [x] 把长期有效的结论整理进 `docs/architecture/`，更新 `docs/README.md`
- [x] 更新 `apps/core/AGENTS.md`、`apps/web/AGENTS.md`、`work/agent/memory.md`
- [x] 用真实模型验收主管的工具：按岗位派活、`project_create`、`project_clone`、带工作台上下文的消息
