# Sous 新架构图

> 目标态，不是当前部署图。配套：[讨论结果](discussion-results.md) · [技术设计](design.md) · [实施与验收任务](tasks.md)。

## 1. 模块、协议与权威状态

```mermaid
flowchart TB
    desktop["桌面 App · 内置 WebUI"]
    web["手机 / 浏览器 · WebUI"]
    external["外部 Agent 客户端 · 可选"]
    entry["工作区入口 · 本机接入或受认证远端接入"]
    hub["Hub · 业务 API、MCP、审批与调度"]
    state[("该工作区的权威状态<br/>对话、记忆、项目、任务、Run 与事件")]
    chief["可选主管 · Sous Agent 或经验证的 ACP Agent"]
    node["授权的目标 Node · 本机沙盒 / 其他机器 / 云工作机"]
    worker["执行 Agent · 与 Node 同一执行环境"]

    desktop <-->|"业务 API / 事件"| entry
    web <-->|"业务 API / 事件"| entry
    external -->|"受限 MCP · 不经主管"| entry
    entry <--> hub
    hub <--> state
    hub <-->|"ACP · 回合、流式输出、取消"| chief
    chief -->|"MCP · 查资料、建任务、提交判断"| hub
    hub <-->|"Run 派发、进度、审批与结果"| node
    node <-->|"本地 ACP"| worker
```

- 外部 MCP、远程主管和多机连接是可选能力，不是纯本地路径的依赖。WebUI 不直连主管或 Node；主管通过 Hub 工具，不直连 Node 或 Hub 数据库。一个工作区只有一个 Hub 权威，不把同屏出现的本地工作区和云工作区混成双主。
- **App 是装配层**：同包不等于强依赖，不为每个框各起一个远程服务。Hub 与主管同机时也保持 ACP/MCP 边界；主管角色与执行 Agent 的身份和权限不共享。

## 2. 两种默认部署；混搭只替换相应端点

```mermaid
flowchart TB
    subgraph local["全本地 · 桌面 App · 无 Relay / 云账号依赖"]
        direction LR
        l_ui["内置 WebUI"] <-->|"本机身份 + IPC / loopback"| l_hub["本地 Hub + 本地状态"]
        l_hub <-->|"本地 ACP / MCP"| l_chief["可选本地主管"]
        l_hub <-->|"执行时接入"| l_box["本机沙盒 Node + 执行 Agent"]
    end

    subgraph hosted["标准托管 · 工作区主区域"]
        direction LR
        c_entry["工作区入口 · 需要时使用 Relay"] <--> c_hub["常在线云 Hub + 同区状态"]
        c_hub <-->|"同机 / 同区域 ACP、MCP 桥"| c_chief["可选云主管"]
        c_hub <-->|"有授权 Run 才接入"| c_vm["云工作机 · 按需 Node + 执行 Agent"]
    end

    client["桌面或手机浏览器 · 静态页面可走 CDN"] <-->|"业务 API / 持久状态与实时流"| c_entry
    target["在线电脑 · 宿主薄接入 + 沙盒 Node"] -->|"由设备主动连接"| c_entry
```

| 混搭 | 只替换哪条边 | 不会发生什么 |
|---|---|---|
| 本地 Hub + 云主管 / 手机 WebUI | 远程 WebUI 接到本地 Hub；主管 ACP 和受限 MCP 经认证适配器跨 WAN | 云端不会复制一个权威 Hub；家中 Hub 断线时不能接管其任务 |
| 云 Hub + 本地 Codex 等主管 | 本地连接器接到云 Hub，Agent 端仍可跑 ACP stdio；桌面和其他 Node 独立在线 | 主管掉线不会阻断 Hub 的手动派发，不静默换主管 |
| 外部 MCP + 桌面；本地 / 云工作区任选 | 外部客户端与桌面选中同一工作区时共用同一权威数据；选中不同工作区时隔离 | 不需要每次 MCP 工具调用途经 Sous Agent；不做同工作区双写、Node 互相转发或自动把离线的本地目标换成云目标 |

**热路径**：普通对话只经过入口、Hub、主管及其模型，不等待 Node 或 VM。部署靠近 Hub 的是高频工具调用和数据层，不是静态前端制品。需要现有信任/中继通道时保留语义；没必要的前端应用代理不加入实时链路。

## 3. 云 Hub 到本机 Node：运行请求不是宿主文件权限

图中的本机临时 microVM 由 **Microsandbox** 提供；此选型不限定第 4 节的云工作机。

```mermaid
flowchart TB
    hub["云 Hub / 对应 Relay<br/>Run 与不透明项目授权 ID"]
    user["本机用户 · 原生选择器 / 确认 / 撤销"]
    subgraph host["宿主 · 本机可信权限边界"]
        direction TB
        ingress["受限接入 · 主动连工作区"]
        broker["本机授权器 + VM 管理器<br/>保存真实路径、用途、模式与期限"]
        files[("用户选定的宿主文件 / 目录")]
        report["本机改动审阅 · 冲突检查"]
    end
    subgraph guest["临时 microVM · 独立 Guest 内核"]
        direction TB
        node["完整 Node · Run、终端、Git、搜索、文件工具"]
        agent["执行 Agent"]
        copy[("Guest 内项目副本")]
        node <-->|"ACP stdio"| agent
        node <--> copy
    end

    ingress -->|"宿主主动建连 · 双向传递受限帧"| hub
    ingress -->|"验证后的任务请求"| broker
    user -->|"本机授权 · 逐次或明确限时"| broker
    broker -->|"检查通过后 · 受限 IPC 控制"| node
    files -->|"只导入用户授权范围 · 按 Run"| broker
    broker -->|"临时导入 · 不开启自动写回"| copy
    node -->|"允许的状态 / 结果"| ingress
    copy -->|"候选改动"| report
    user -->|"独立确认写回"| report
    report -->|"仅写回获批变更"| files
```

**默认不做实时共享。** Run 结束、取消、授权到期或撤销后关闭本机授权窗口，销毁临时 VM 并清理导入副本；如果后来启用挂载，撤不掉由宿主终止 VM 和发布共享的服务。入口没有任意路径、宿主 shell 或 VM 通用管理接口。Guest 默认不通公网/宿主 LAN；仅按本机策略放行所需模型或依赖，不复用有项目内容的 VM 快照。隔离不能追回已通过授权通道发出的数据。

## 4. 云工作机：独立于聊天的启停状态机

```mermaid
stateDiagram-v2
    [*] --> Off
    state "计算已释放 · Hub / 成果仍在" as Off
    state "启动环境" as Starting
    state "注册探活 / 准备项目" as Preparing
    state "可执行 / 执行中" as Running
    state "短暂空闲保活" as Idle
    state "排空与持久化确认" as Draining
    state "启动或准备失败 · 任务保留" as Failed
    state "成果未确认 · 保留必要现场" as Held

    Off --> Starting: 云目标有可执行的授权任务
    Starting --> Preparing: 新实例启动，发短时注册凭据
    Preparing --> Running: 通过探活，项目与代次就绪
    Starting --> Failed: 有限重试仍失败或超时
    Preparing --> Failed: 注册或材料准备失败
    Failed --> Starting: 仍有可执行任务并获准重试
    Failed --> Off: 取消或清理失败实例
    Running --> Idle: 无在途 Run、可执行队列或保活授权
    Idle --> Running: 新任务复用现有实例
    Idle --> Draining: 空闲阈值到期
    Draining --> Running: 新任务到达且可取消回收
    Draining --> Off: 已持久化，禁止旧代次接单并回收
    Draining --> Held: 无法确认成果已保存
    Held --> Draining: 成果补存后重新确认
```

草稿、普通聊天、前置审批、等本地 Node 都不触发 `Off → Starting`。已进入 Run 的审批等待不是空闲。无法取消排空时，新任务先留在 Hub，回收后按新代次启动。恢复的是持久成果和业务记录，不是原进程内存；启动/回收失败要对账，不盲目再执行。


## 当前已实测的托管执行路径（参考 worker）

```mermaid
sequenceDiagram
  participant Owner as 手机 WebUI / Passkey
  participant Hub as 常在线 Hosted Hub
  participant Work as 独立 worker
  participant Guest as 临时无网络 Microsandbox
  Owner->>Hub: 显式授权任务版本、命令及材料副本
  Hub-->>Owner: 持久化等待意图（202）
  Work->>Hub: 领取并持有短时租约
  Work->>Guest: 启动并导入本次授权材料
  Guest-->>Work: 执行结果快照
  Note over Work: 私有检查点同步落盘
  Work->>Guest: 销毁并验证
  Work->>Hub: 租约绑定的幂等完成回执
  Hub-->>Owner: 成果已保存；可复用于同项目新执行
```

该证据只覆盖 Apple Silicon 的离线 Guest，不包含云厂商启动或停止 Linux 宿主，也不支持云 Hub 操作个人电脑或旧 Node。

## 当前已联合验证的容量控制路径（厂商夹具）

```mermaid
sequenceDiagram
  participant UI as 手机 WebUI
  participant Hub as 常在线 Hosted Hub
  participant Control as 独立容量控制器
  participant Driver as 厂商驱动（验收为文件夹具）
  participant Worker as 逐代 worker 与内层 Guest
  UI->>Hub: 近期 Passkey + 明确授权 → 待执行
  Control->>Hub: 提交下一代预留与引导摘要
  Hub-->>Control: 条件满足时持久化 starting
  Control->>Driver: 幂等确保外层实例；仅交短时引导
  Worker->>Hub: 预检后用一次性引导换逐代会话
  Worker->>Hub: 领取、回执、清完检查点后再次确认空闲
  Control->>Hub: 无排队/在途且空闲已确认 → 原子封锁旧代
  Control->>Driver: 回收指定预留；未确认则继续对账
  Note over UI,Hub: 回收期间可授权新任务，留在队列等下一代
  Driver-->>Control: 确认实例不存在且迟到创建已封锁
  Control->>Hub: 确认 off，允许下一代申请
```

已测的是 Hub、控制器进程、worker 和真实内层 Guest；外层厂商实例仍以夹具代替。当前版本跨过排空栅栏后不复活旧代，回收期间来的任务始终排队。不把夹具的 `absent` 当成真实账单归零。
