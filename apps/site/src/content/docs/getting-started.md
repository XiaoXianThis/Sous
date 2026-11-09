---
title: "5 分钟跑起来：安装与部署 Hub"
description: "安装 sous 二进制、在本地或 Linux 服务器上启动 sous-hub、打开网页注册 Passkey 并接入第一台电脑。"
group: "getting-started"
order: 2
sourceDoc: "docs/guides/deploy-hub.md"
updatedAt: "2026-11"
---

本页带你用最快的方式把 `sous` 跑起来。你可以先在**同一台电脑上花 5 分钟完成本地体验**（路线 A），也可以直接在 **Linux 服务器上完成生产级 HTTPS 部署**（路线 B）。

## 本页目录

- [路线 A：5 分钟本机快速跑起来](#路线-a5-分钟本机快速跑起来)
- [路线 B：在 Linux 服务器上生产部署 Hub](#路线-b在-linux-服务器上生产部署-hub)
- [日常运维、自动恢复与数据备份](#日常运维自动恢复与数据备份)

---

## 路线 A：5 分钟本机快速跑起来

**什么时候用**：你想先在自己的 Mac、Linux 或 Windows 电脑上体验 `sous` 的工作台、主管对话和本地 Agent 调度，暂时不配公网域名。

### 1. 构建或获取二进制

如果你已经克隆了仓库并安装了 Go 与 Bun（推荐使用仓库的 `wok` 工具）：

```sh
git clone https://github.com/XiaoXianThis/Sous.git
cd Sous
wok build web
wok build core
```

构建完成后：
- `build/web/`：PWA 前端静态资源
- `build/core/sous-hub`：服务端 Hub 二进制
- `build/core/sous`：客户端 CLI 与 daemon 二进制

> 如果你只想安装客户端 `sous` 连接到已有的 Hub，可直接跳到第 4 步使用官方一键安装脚本。

### 2. 启动本地 Hub

在仓库根目录直接使用开发配置启动 `sous-hub`（默认监听 `http://127.0.0.1:8787`）：

```sh
wok dev core
```

如果没有安装 `wok`，也可以在 `apps/core/` 目录下执行：

```sh
cd apps/core
go run ./cmd/sous-hub -config configs/hub.dev.yaml
```

> **提示**：`configs/hub.dev.yaml` 开启了仅限本机回环地址（`127.0.0.1` / `localhost`）的 `dev: true` 免密开发登录。首次启动后，你可以在网页上直接配置模型供应商与 API Key，无需重启。

### 3. 打开网页并配置主管模型

1. 用浏览器打开 `http://localhost:8787`（若开启了 `dev: true`，点击开发登录即可进入；若使用正式配置，从终端日志中找到包含 `setup` 的一次性链接注册 Passkey）。
2. 点击左侧栏底部的 **设置（齿轮图标） → 模型**：
   - 点击 **添加供应商**，选择协议类型（`openai-chat`、`openai-responses` 或 `anthropic-messages`），填入 Base URL 与 API Key（或指定服务器环境变量名）。
   - 在上方角色列表中，为 **主管（`supervisor`）** 选择模型与思考强度（其余角色 `reviewer`、`approver`、`inspector`、`compactor` 默认跟随主管，也可单独指定更轻量的模型）。

### 4. 接入你的第一台电脑

1. 在网页左侧打开 **设置 → 机器**（或工作台顶部机器图标），点击 **配对新机器**，界面会生成一条 10 分钟内有效的 6 位配对命令。
2. 在你的电脑终端安装 `sous` 客户端（macOS / Linux）：

```sh
curl -fsSL https://raw.githubusercontent.com/XiaoXianThis/Sous/main/work/scripts/install-sous.sh | sh
```

Windows PowerShell：

```powershell
irm https://raw.githubusercontent.com/XiaoXianThis/Sous/main/work/scripts/install-sous.ps1 | iex
```

3. 执行网页上给出的配对命令（把 `~/Code` 换成你平时放 git 仓库的目录）：

```sh
sous pair http://localhost:8787 123456 --name 我的开发机 --workspace ~/Code
```

4. 检查本机已安装并登录的编程 Agent，然后启动后台 daemon：

```sh
sous agents
sous daemon
```

此时回到网页工作台，你会看到电脑已变为**在线**，并且 `~/Code` 下的 git 仓库已被自动扫描发现。

---

## 路线 B：在 Linux 服务器上生产部署 Hub

**什么时候用**：你希望在手机、平板、笔记本和台式机上随时访问同一个副主厨。把 `sous-hub` 部署到一台拥有域名的 Linux 服务器（x86-64 或 ARM64），由 `systemd` 托管进程、`Caddy` 提供自动 HTTPS。

### 1. 准备发行包或交叉编译

最简单的方式是从 [GitHub Releases](https://github.com/XiaoXianThis/Sous/releases) 下载对应架构的发行包：

- x86-64 服务器：`sous-hub_<版本>_linux_amd64.tar.gz`
- ARM64 服务器：`sous-hub_<版本>_linux_arm64.tar.gz`

解压后包含 `sous-hub` 二进制、`web/`（PWA 静态文件）、`hub.example.yaml` 以及 `deploy/`（`systemd` 单元与 `Caddyfile`）。

如果从源码交叉编译，在本机仓库根目录执行：

```sh
wok build web
wok run core build-hub-linux
```

> 若目标服务器为 ARM64，可将 `apps/core/AGENTS.md` 中的 `GOARCH=amd64` 改为 `GOARCH=arm64` 后构建。

### 2. 创建服务器用户与目录

以 `root` 身份在 Linux 服务器上执行：

```sh
useradd --system --home /opt/sous --shell /usr/sbin/nologin sous
mkdir -p /opt/sous/data /opt/sous/web /etc/sous /var/backups/sous
chown -R sous:sous /opt/sous/data /var/backups/sous
```

### 3. 上传文件

在本机仓库根目录执行（把 `server` 替换为你的服务器 SSH 地址）：

```sh
scp build/core/linux-amd64/sous-hub server:/opt/sous/sous-hub
scp -r build/web/. server:/opt/sous/web/
scp apps/core/configs/hub.example.yaml server:/opt/sous/hub.yaml
scp work/infra/deploy/hub/sous-hub.service work/infra/deploy/hub/sous-hub-backup.service work/infra/deploy/hub/sous-hub-backup.timer server:/etc/systemd/system/
```

### 4. 修改 `hub.yaml` 与环境变量

1. 编辑服务器上的 `/opt/sous/hub.yaml`：
   - 将 `public_url` 改为你的 HTTPS 域名（如 `https://agent.wxian.cc`，WebAuthn Passkey 会绑定该域名）。
   - `listen` 保持 `127.0.0.1:8787`，**生产环境切勿添加 `dev: true`**。
2. 写入环境变量文件（如果使用环境变量提供模型密钥）并收紧权限：

```sh
printf 'DEEPSEEK_API_KEY=%s\n' '你的密钥' > /etc/sous/hub.env
chmod 600 /etc/sous/hub.env
chmod 755 /opt/sous/sous-hub
```

### 5. 启动 systemd 服务与配置 HTTPS

启用并启动 `sous-hub` 与每日自动备份定时器：

```sh
systemctl daemon-reload
systemctl enable --now sous-hub sous-hub-backup.timer
systemctl status sous-hub
```

配置 Caddy 反向代理（将 `work/infra/deploy/hub/caddyfile` 内容写入 `/etc/caddy/Caddyfile`，确保域名 DNS 已解析到本机）：

```sh
systemctl reload caddy
```

> **直连 HTTPS 说明**：若不使用反向代理，可在 `hub.yaml` 中配置 `tls_cert` 与 `tls_key`（PEM 路径）并将 `listen` 设为 `0.0.0.0:443`。当 `listen` 为非回环地址且未配 TLS 时，Hub 会拒绝启动以防令牌明文传输（容器加密网络可显式设 `insecure_listen: true`）。

### 6. 注册第一个 Passkey 与安装手机主屏 App

首次启动时，Hub 会在日志中打印一条 24 小时内有效的注册链接：

```sh
journalctl -u sous-hub | grep setup
```

在浏览器打开该链接完成 Passkey 注册（要求指纹、面容或设备 PIN 验证）。随后用手机浏览器访问你的域名，登录后选择「添加到主屏幕」（iOS 使用 Safari 的「分享」菜单），即可作为全屏独立 App 使用并开启通知推送。

若链接过期或更换设备需要重置 Passkey，可在服务器上执行：

```sh
sudo -u sous /opt/sous/sous-hub -config /opt/sous/hub.yaml admin passkey-reset
```

追加 `--revoke-all` 会同时删除所有旧 Passkey 并注销全部已登录会话。

---

## 日常运维、自动恢复与数据备份

- **升级 Hub**：网页「机器」面板会提示新版本。将新版本的 `sous-hub` 与 `web/` 上传覆盖后执行 `systemctl restart sous-hub` 即可，SQLite 数据库表结构会在启动时自动按迁移升级。
- **内置看门狗与断线对账**：当副作用队列、任务引擎、定时任务、巡检停止心跳或数据库写锁阻塞超过 2 分钟时，`sous-hub` 会主动退出交由 `systemd`（`Restart=always`）重启；重启后未完成的副作用接着跑，各台电脑上的 `sous daemon` 重连后会自动根据 `outbox.jsonl` 对账补发事件。
- **备份与恢复**：`sous-hub-backup.timer` 每天 04:00 左右在 `/var/backups/sous/` 生成 `hub-<时间>.db` 一致性快照并保留 14 天。手动备份与恢复命令：

```sh
# 手动生成一致性快照
sudo -u sous /opt/sous/sous-hub -config /opt/sous/hub.yaml admin backup /var/backups/sous/manual.db

# 从快照恢复
systemctl stop sous-hub
cp /var/backups/sous/hub-<时间>.db /opt/sous/data/hub.db
rm -f /opt/sous/data/hub.db-wal /opt/sous/data/hub.db-shm
chown sous:sous /opt/sous/data/hub.db
systemctl start sous-hub
```

下一步：阅读 [第一个任务：对话、拆解、审批与结果](/docs/first-task)，开始下达你的第一个任务。
