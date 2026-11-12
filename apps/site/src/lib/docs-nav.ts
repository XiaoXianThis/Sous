import { withBase } from './with-base';

export type DocGroupKey =
  | 'getting-started'
  | 'concepts'
  | 'guides'
  | 'reference'
  | 'development'
  | 'architecture'
  | 'faq';

export interface PlannedDocItem {
  title: string;
  description: string;
  slug: string;
  repoPath: string;
  stage: 1 | 2;
}

export interface DocGroupMeta {
  key: DocGroupKey;
  code: string;
  title: string;
  subtitle: string;
  items: PlannedDocItem[];
}

export const REPO_URL = 'https://github.com/XiaoXianThis/Sous';
export const RELEASES_URL = 'https://github.com/XiaoXianThis/Sous/releases';
export const SITE_URL = new URL(withBase('/'), import.meta.env.SITE).href;

export const DOC_GROUPS: DocGroupMeta[] = [
  {
    key: 'getting-started',
    code: '01',
    title: '入门',
    subtitle: '认识副主厨隐喻与主厨/助理分工，5 分钟跑起 Hub 与第一台电脑，跑通第一个任务。',
    items: [
      {
        title: 'Sous 是什么：副主厨与分工',
        description: '理解主厨（你）、副主厨（主管 sous）与后厨厨师（各编程 Agent）的协作方式与适用场景。',
        slug: 'introduction',
        repoPath: 'README.md',
        stage: 2,
      },
      {
        title: '5 分钟跑起来：安装与部署 Hub',
        description: '安装二进制、本地快速启动或在 Linux 服务器上部署 sous-hub，打开网页并接入第一台电脑。',
        slug: 'getting-started',
        repoPath: 'docs/guides/deploy.md',
        stage: 1,
      },
      {
        title: '第一个任务：对话、拆解、审批与结果',
        description: '怎么跟主管说话、怎么唤起项目顾问、任务如何被拆解派发、怎么原地审批、怎么验收与看结果。',
        slug: 'first-task',
        repoPath: 'docs/architecture/workbench.md',
        stage: 2,
      },
    ],
  },
  {
    key: 'concepts',
    code: '02',
    title: '概念',
    subtitle: '深入理解主管与子 Agent、任务与 Run、三种执行位置、预设与模型、分层审批与项目部署。',
    items: [
      {
        title: '主管、子 Agent、任务与执行位置',
        description: '主管与 ACP 子 Agent 闭环、Task 与 Run 的关系，以及 worktree / in_place / scratch 三种执行位置。',
        slug: 'core-concepts',
        repoPath: 'docs/architecture/workbench.md',
        stage: 2,
      },
      {
        title: '预设（岗位）、机器工作空间与项目部署',
        description: '预设（Agent + 模型 + 思考强度）挑选逻辑、开发机与服务器角色、工作空间扫描与部署目标自动触发。',
        slug: 'presets-and-machines',
        repoPath: 'docs/decisions/0024-presets-machine-agents-and-a-lighter-supervisor.md',
        stage: 2,
      },
      {
        title: '审批机制：策略、硬信号、审批 Agent 与复用',
        description: 'default / auto / open 权限模式、Node 底线正则、硬信号、审批 Agent 取证循环与 allow_always 复用。',
        slug: 'approval-mechanism',
        repoPath: 'docs/decisions/0019-auto-approval-layers.md',
        stage: 2,
      },
    ],
  },
  {
    key: 'guides',
    code: '03',
    title: '使用指南',
    subtitle: '接入电脑与配置各家 Agent、自定义预设、减少审批打断、查看产物与预览、Git 交付与定时任务。',
    items: [
      {
        title: '接入电脑与配置各家 Agent',
        description: '安装与登录 Claude Code / Codex / Cursor / OpenCode / Kiro，以及通过 custom_agents 接入 Antigravity 等自定义 Agent。',
        slug: 'connect-machine',
        repoPath: 'docs/guides/connect-machine.md',
        stage: 1,
      },
      {
        title: '预设配置、减少审批打断、看结果与预览产物',
        description: '管理自定义预设、调优自动审批减少打断、查看任务实时进展与结构化结果、在线浏览与预览项目文件及终端。',
        slug: 'workbench-and-presets',
        repoPath: 'docs/architecture/workbench.md',
        stage: 2,
      },
      {
        title: 'Git 交付操作、多机同步与定时任务',
        description: '按需执行提交、快进合并、推送、开 PR、撤回与就地快照恢复，多机 checkout 同步/存 WIP 与 Cron 定时任务。',
        slug: 'git-and-schedules',
        repoPath: 'docs/api/core-hub-node.md',
        stage: 2,
      },
    ],
  },
  {
    key: 'reference',
    code: '04',
    title: '参考',
    subtitle: '命令行子命令、node.json 与 hub.yaml 完整配置项、HTTP 接口、事件类型与 Agent 模型矩阵。',
    items: [
      {
        title: 'CLI 命令与配置文件参考（node.json / hub.yaml）',
        description: 'sous 与 sous-hub 全部 CLI 子命令与参数，以及客户端 node.json、服务端 hub.yaml 的完整字段说明。',
        slug: 'cli-and-config',
        repoPath: 'apps/core/configs/hub.example.yaml',
        stage: 2,
      },
      {
        title: 'HTTP 接口、事件类型与 Agent 模型矩阵',
        description: '整理自 docs/api 的 REST API 方法与路径、WebSocket 事件流类型、Daemon IPC 方法及内置 Agent 与模型矩阵。',
        slug: 'api-and-events',
        repoPath: 'docs/api/core-hub-http.md',
        stage: 2,
      },
    ],
  },
  {
    key: 'development',
    code: '05',
    title: '开发',
    subtitle: 'wokspace 仓库结构、构建与测试命令、新增 Agent 与 App、GitHub Releases 发布流程与 macOS 签名。',
    items: [
      {
        title: '开发指南：仓库结构、构建测试、扩展与发布签名',
        description: 'wokspace 布局、wok / go / bun 构建测试命令、新增内置或自定义 Agent、新增 App、发版流程及 macOS TCC 签名。',
        slug: 'development-guide',
        repoPath: 'apps/core/AGENTS.md',
        stage: 2,
      },
    ],
  },
  {
    key: 'architecture',
    code: '06',
    title: '架构',
    subtitle: 'Hub/Daemon 分离、连接与鉴权、事件溯源与投影、任务调度与并发、Worktree/Shadow 隔离与 SQLite 表结构。',
    items: [
      {
        title: '系统架构：Hub/Daemon、事件溯源、隔离与存储',
        description: '深入解析 Hub/Node 边界、Passkey 与设备令牌鉴权、eventlog 单一事实来源、并发槽位、Worktree 与 SQLite 表结构。',
        slug: 'architecture-overview',
        repoPath: 'docs/architecture/README.md',
        stage: 1,
      },
    ],
  },
  {
    key: 'faq',
    code: '07',
    title: 'FAQ',
    subtitle: '常见报错（Agent 未登录、额度不足、Worktree 失败、macOS TCC 弹窗、504 更新超时、审批卡住）排查手册。',
    items: [
      {
        title: 'FAQ 与常见故障排查',
        description: '针对 Agent 未登录、额度不足、Worktree 准备失败、TCC 权限弹窗、504 更新失败、审批卡住给出「现象 → 原因 → 步骤」。',
        slug: 'faq-troubleshooting',
        repoPath: 'docs/guides/macos-signing-and-permissions.md',
        stage: 2,
      },
    ],
  },
];

export const GROUP_LABELS: Record<DocGroupKey, string> = {
  'getting-started': '入门',
  concepts: '概念',
  guides: '指南',
  reference: '参考',
  development: '开发',
  architecture: '架构',
  faq: 'FAQ',
};

export const GROUP_RANK: Record<DocGroupKey, number> = {
  'getting-started': 1,
  concepts: 2,
  guides: 3,
  reference: 4,
  development: 5,
  architecture: 6,
  faq: 7,
};
