export type DocGroupKey = 'getting-started' | 'usage' | 'development' | 'reference';

export interface PlannedDocItem {
  title: string;
  description: string;
  slug?: string;
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

export const DOC_GROUPS: DocGroupMeta[] = [
  {
    key: 'getting-started',
    code: '01',
    title: '入门',
    subtitle: '部署服务端 Hub、注册 Passkey、将你的开发机或服务器配对接入。',
    items: [
      {
        title: '安装与部署 Hub',
        description: '在 Linux 服务器上部署 sous-hub 与 PWA，配置 systemd、Caddy HTTPS、Passkey 与自动备份。',
        slug: 'getting-started',
        repoPath: 'docs/guides/deploy-hub.md',
        stage: 1,
      },
      {
        title: '接入一台电脑或服务器',
        description: '一键安装 sous 客户端、登录本机编程 agent、配对开发机或服务器并注册常驻服务。',
        slug: 'connect-machine',
        repoPath: 'docs/guides/connect-machine.md',
        stage: 1,
      },
    ],
  },
  {
    key: 'usage',
    code: '02',
    title: '使用',
    subtitle: '在工作台中管理项目、预设、任务审批、项目顾问与远程终端。',
    items: [
      {
        title: '工作台：项目、预设与任务流',
        description: '理解主管对话、项目顾问、独立 worktree / 就地执行、分层自动审批与按需 git 交付。',
        slug: 'architecture-overview',
        repoPath: 'docs/architecture/workbench.md',
        stage: 1,
      },
      {
        title: '权限模式、自动审批与底线规则',
        description: '三档权限模式（default / auto / open）、确定性硬信号、审批 agent 与 node.json 本地底线。',
        repoPath: 'docs/decisions/0019-auto-approval-layers.md',
        stage: 2,
      },
      {
        title: 'macOS 签名与隐私权限（TCC）',
        description: '使用固定自签名身份签名 sous，避免 LaunchAgent 重建后丢失完全磁盘访问授权。',
        repoPath: 'docs/guides/macos-signing-and-permissions.md',
        stage: 2,
      },
    ],
  },
  {
    key: 'development',
    code: '03',
    title: '开发',
    subtitle: '系统架构、Hub ↔ Node 边界、事件溯源、主管上下文分段与架构决策记录（ADR）。',
    items: [
      {
        title: '架构概览：Hub、Node 与工作台',
        description: '大脑与执行端分离、事件日志单一事实来源、ACP 子 agent 调度、主管闭环与上下文管理。',
        slug: 'architecture-overview',
        repoPath: 'docs/architecture/README.md',
        stage: 1,
      },
      {
        title: '主管上下文与长对话分段摘要',
        description: '前缀稳定以命中 KV 缓存、prompts.Tail 动态环境注入、分段结构化摘要与原文查回。',
        repoPath: 'docs/architecture/supervisor-context.md',
        stage: 2,
      },
      {
        title: '架构决策记录（ADR 0001–0026）',
        description: '从 Super-agent 架构、ACP 协议、三档权限、Liquid Glass 手机端到主管闭环的完整决策演进。',
        repoPath: 'docs/decisions/README.md',
        stage: 2,
      },
    ],
  },
  {
    key: 'reference',
    code: '04',
    title: '参考',
    subtitle: '应用间 HTTP / WebSocket / 本地 IPC 协议契约与命令行速查。',
    items: [
      {
        title: 'Hub HTTP & WebSocket 接口契约',
        description: 'core（sous-hub）向 web 与 desktop 提供的 REST API、SSE/WebSocket 事件流与鉴权规范。',
        repoPath: 'docs/api/core-hub-http.md',
        stage: 2,
      },
      {
        title: 'Hub ↔ Node 通信契约',
        description: 'daemon 入站 WebSocket 帧协议、Run 生命周期、权限审批、伪终端作业与断线对账。',
        repoPath: 'docs/api/core-hub-node.md',
        stage: 2,
      },
      {
        title: '本地 Daemon IPC 契约',
        description: 'daemon.sock 本地 JSON-RPC 接口，供 sous CLI（chat / status / update）与 desktop 调用。',
        repoPath: 'docs/api/core-daemon-ipc.md',
        stage: 2,
      },
    ],
  },
];

export const GROUP_LABELS: Record<DocGroupKey, string> = {
  'getting-started': '入门',
  usage: '使用',
  development: '开发',
  reference: '参考',
};
