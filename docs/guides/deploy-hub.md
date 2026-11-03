# 部署 Hub

把 sous 的 Hub 部署到一台 Linux 服务器（x86-64），通过 `https://agent.wxian.cc` 访问。用 systemd 托管进程，Caddy 负责 HTTPS 和反向代理。部署用到的文件在 `work/infra/deploy/hub/`。

## 1. 在本机构建

在仓库根目录执行，Windows、macOS、Linux 上的命令相同：

```
wok build web
wok run core build-hub-linux
```

产物：

- `build/web/`：PWA
- `build/core/linux-amd64/sous-hub`：Hub 程序（静态链接，不依赖系统库）

服务器是 ARM 的话，把任务表里的 `GOARCH=amd64` 换成 `GOARCH=arm64` 再构建。

## 2. 准备服务器

以 root 身份执行：

```sh
useradd --system --home /opt/sous --shell /usr/sbin/nologin sous
mkdir -p /opt/sous/data /opt/sous/web /etc/sous /var/backups/sous
chown -R sous:sous /opt/sous/data /var/backups/sous
```

## 3. 上传文件

在本机仓库根目录执行（把 `server` 换成你的服务器）。

macOS / Linux：

```sh
scp build/core/linux-amd64/sous-hub server:/opt/sous/sous-hub
scp -r build/web/. server:/opt/sous/web/
scp apps/core/configs/hub.example.yaml server:/opt/sous/hub.yaml
scp work/infra/deploy/hub/sous-hub.service work/infra/deploy/hub/sous-hub-backup.service work/infra/deploy/hub/sous-hub-backup.timer server:/etc/systemd/system/
```

Windows PowerShell 用同样的 `scp` 命令，只是路径分隔符写成 `\`，例如 `build\core\linux-amd64\sous-hub`。

## 4. 配置

1. 检查 `/opt/sous/hub.yaml`：`public_url` 是 `https://agent.wxian.cc`，`listen` 保持 `127.0.0.1:8787`，**不要加 `dev: true`**。
2. 写入密钥（只存在服务器上，权限 600）：

   ```sh
   printf 'DEEPSEEK_API_KEY=%s\n' '你的密钥' > /etc/sous/hub.env
   chmod 600 /etc/sous/hub.env
   chmod 755 /opt/sous/sous-hub
   ```

## 5. 启动

```sh
systemctl daemon-reload
systemctl enable --now sous-hub sous-hub-backup.timer
systemctl status sous-hub
```

## 6. 配置 HTTPS

安装 Caddy，把 `work/infra/deploy/hub/caddyfile` 的内容放进 `/etc/caddy/Caddyfile`，然后执行：

```sh
systemctl reload caddy
```

域名的 DNS 需要指向这台服务器。Caddy 会自动申请证书。

## 7. 注册第一个 Passkey

第一次启动时，Hub 会在日志里打印一个 24 小时内有效的注册链接：

```sh
journalctl -u sous-hub | grep setup
```

在你常用的设备上打开这个链接，创建 Passkey。之后在已登录的设备上就可以直接用 Passkey 登录了。

链接过期了，或者 Passkey 丢了，执行：

```sh
sudo -u sous /opt/sous/sous-hub -config /opt/sous/hub.yaml admin passkey-reset
```

加上 `--revoke-all`，会同时删除所有旧的 Passkey，并让所有设备退出登录。

## 8. 在手机上安装

用手机浏览器打开 `https://agent.wxian.cc`，登录后选“添加到主屏幕”（iOS 上用 Safari 的“分享”菜单）。之后从主屏幕图标打开，就会以全屏 App 的形式运行。

## 升级

重新执行第 1 步，上传新的 `sous-hub` 和 `web/`，然后：

```sh
systemctl restart sous-hub
```

数据库的结构会在启动时自动升级。

## 备份与恢复

- 每天 04:00 左右，`/var/backups/sous/` 下会生成一份 `hub-<时间>.db` 快照，保留 14 天。
- 手动备份：`sudo -u sous /opt/sous/sous-hub -config /opt/sous/hub.yaml admin backup /var/backups/sous/manual.db`
- 恢复：

  ```sh
  systemctl stop sous-hub
  cp /var/backups/sous/hub-<时间>.db /opt/sous/data/hub.db
  rm -f /opt/sous/data/hub.db-wal /opt/sous/data/hub.db-shm
  chown sous:sous /opt/sous/data/hub.db
  systemctl start sous-hub
  ```
