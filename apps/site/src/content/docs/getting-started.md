---
title: "安装与部署 Hub"
description: "在 Linux 服务器上部署 sous-hub 服务端与 PWA，配置 systemd 托管、Caddy HTTPS、Passkey 注册与定时备份。"
group: "getting-started"
order: 1
sourceDoc: "docs/guides/deploy-hub.md"
updatedAt: "2026-11"
---

`sous` 采用「Hub 动脑、Node 动手」的分离架构。**Hub（`sous-hub`）** 常驻在你的 Linux 服务器上，负责维护事件日志、与你对话、规划与跟进任务，并托管网页端（PWA）；各台开发机与服务器通过客户端 `sous` 主动连向 Hub，无需开放任何入站端口。

本文整理自仓库 `docs/guides/deploy-hub.md`，介绍如何用 `systemd` 与 `Caddy` 部署一套生产可用的 Hub。

## 1. 准备程序

最简单的方式是从 [GitHub Releases](https://github.com/XiaoXianThis/Sous/releases) 下载对应架构的发行包：

- x86-64 服务器：`sous-hub_<版本>_linux_amd64.tar.gz`
- ARM64 服务器：`sous-hub_<版本>_linux_arm64.tar.gz`

解压后包含 `sous-hub` 二进制、`web/`（PWA 静态文件）、`hub.example.yaml` 以及 `deploy/`（`systemd` 单元与 `Caddyfile`）。

如果你从源码构建，在仓库根目录执行（Windows、macOS、Linux 命令一致）：

```sh
wok build web
wok run core build-hub-linux
```

构建产物位于：

- `build/web/`：PWA 前端静态资源
- `build/core/linux-amd64/sous-hub`：静态链接的 Hub 程序（不依赖系统动态库）

> 若目标服务器为 ARM64，可将 `apps/core/AGENTS.md` 任务表中的 `GOARCH=amd64` 替换为 `GOARCH=arm64` 后构建。

## 2. 准备服务器目录与用户

以 `root` 身份在 Linux 服务器上创建专用系统用户与目录：

```sh
useradd --system --home /opt/sous --shell /usr/sbin/nologin sous
mkdir -p /opt/sous/data /opt/sous/web /etc/sous /var/backups/sous
chown -R sous:sous /opt/sous/data /var/backups/sous
```

## 3. 上传文件

在本机仓库根目录执行（将 `server` 替换为你的服务器主机名或地址）：

```sh
scp build/core/linux-amd64/sous-hub server:/opt/sous/sous-hub
scp -r build/web/. server:/opt/sous/web/
scp apps/core/configs/hub.example.yaml server:/opt/sous/hub.yaml
scp work/infra/deploy/hub/sous-hub.service work/infra/deploy/hub/sous-hub-backup.service work/infra/deploy/hub/sous-hub-backup.timer server:/etc/systemd/system/
```

## 4. 配置与密钥

1. 检查 `/opt/sous/hub.yaml`：将 `public_url` 设为你的 HTTPS 域名（如 `https://agent.wxian.cc`），`listen` 保持 `127.0.0.1:8787`，**生产环境切勿开启 `dev: true`**。
2. 将模型 API 密钥写入环境变量文件（仅保存在服务器，权限 `600`）：

```sh
printf 'DEEPSEEK_API_KEY=%s\n' '你的密钥' > /etc/sous/hub.env
chmod 600 /etc/sous/hub.env
chmod 755 /opt/sous/sous-hub
```

> 模型服务商也可以不在 `hub.yaml` 中手写：启动后直接在网页的 **设置 → 模型** 中添加供应商、填写 API Key 并为主管选择模型，修改即时生效，无需重启。`hub.yaml` 中的 `providers` 与 `roles` 仅在首次启动（数据库尚无供应商记录）时导入一次。

## 5. 启动服务与配置 HTTPS

启用并启动 `sous-hub` 及每日备份定时器：

```sh
systemctl daemon-reload
systemctl enable --now sous-hub sous-hub-backup.timer
systemctl status sous-hub
```

使用 Caddy 提供自动 HTTPS 与反向代理，将 `work/infra/deploy/hub/caddyfile` 的内容写入 `/etc/caddy/Caddyfile` 后重载：

```sh
systemctl reload caddy
```

如果不使用反向代理，可在 `hub.yaml` 中配置 `tls_cert` 与 `tls_key`（PEM 路径）让 Hub 直接监听 HTTPS。当 `listen` 为非回环地址且未配置 TLS 时，Hub 会拒绝启动，防止会话与设备令牌在网络上明文传输（若处于已加密的容器网络，可显式设置 `insecure_listen: true`）。

## 6. 注册第一个 Passkey

首次启动时，Hub 会在日志中打印一条 24 小时内有效的注册链接：

```sh
journalctl -u sous-hub | grep setup
```

在你常用的设备上打开该链接并创建 Passkey。若链接过期或需要重置 Passkey，可在服务器上执行：

```sh
sudo -u sous /opt/sous/sous-hub -config /opt/sous/hub.yaml admin passkey-reset
```

追加 `--revoke-all` 参数会同时吊销所有旧 Passkey 并使全部已登录设备下线。

## 7. 手机主屏安装与日常运维

- **安装到手机主屏**：用手机浏览器打开你的 Hub 地址并登录，选择「添加到主屏幕」（iOS 使用 Safari 的「分享」菜单），即可作为全屏独立 App 运行。
- **自动恢复与看门狗**：`sous-hub` 内置看门狗，当关键循环停止心跳或数据库写锁阻塞超过 2 分钟时会主动退出，交由 `systemd`（`Restart=always`）拉起；重启后未完成的副作用（验收、审批、推送）会从队列继续执行，daemon 也会与 Hub 自动对账补发事件。
- **备份与恢复**：`sous-hub-backup.timer` 每天 04:00 左右在 `/var/backups/sous/` 生成 SQLite 快照并保留 14 天；也可手动执行：

```sh
sudo -u sous /opt/sous/sous-hub -config /opt/sous/hub.yaml admin backup /var/backups/sous/manual.db
```

下一步：阅读 [接入一台电脑或服务器](/docs/connect-machine)，将你的开发机配对到 Hub。
