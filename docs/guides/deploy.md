# 部署 sous 0.3（中继 + Vercel + 大脑机器）

0.3 起服务器上只跑一个盲中继：它看不到数据，也控制不了任何机器（`docs/architecture/trust.md`）。页面托管在 Vercel，AI 在你指定的“大脑机器”上思考。一共三步：部署中继、部署页面、在页面上完成首次设置。从 0.2 升级的，最后看“从 0.2 迁移”。

## 1. 中继（Linux 服务器）

构建：在仓库根目录执行 `wok build core`，或者直接从 GitHub Releases 下载 `sous-relay_<版本>_linux_<架构>.tar.gz`。

以 root 身份执行：

```sh
useradd --system --home /opt/sous --shell /usr/sbin/nologin sous
mkdir -p /opt/sous/relay && chown sous:sous /opt/sous/relay
```

从本机上传（把 `server` 换成你的服务器）：

```sh
scp sous-relay server:/opt/sous/sous-relay
scp work/infra/deploy/relay/sous-relay.service server:/etc/systemd/system/
```

在服务器上：

1. 编辑 `/etc/systemd/system/sous-relay.service`，把 `-origin` 改成页面的地址（第 2 步里 Vercel 上的自定义域名）。
2. 生成认领码，第 3 步要用，只能用一次：

   ```sh
   sudo -u sous /opt/sous/sous-relay init -data /opt/sous/relay
   ```

3. 启动服务：

   ```sh
   systemctl daemon-reload
   systemctl enable --now sous-relay
   ```

4. HTTPS：安装 Caddy，把 `work/infra/deploy/relay/caddyfile` 放进 `/etc/caddy/Caddyfile`，把里面的域名换成中继的域名，然后执行 `systemctl reload caddy`。

中继的数据（`/opt/sous/relay`）只有密文和签名文档，不需要备份：大脑机器上有完整数据，丢了中继数据只是要重新做首次设置和配对。

## 2. 页面（Vercel）

1. 在 Vercel 上建一个项目，绑定**你自己的域名**，比如 `sous.example.com`。Passkey 绑定在这个域名上：以后从 Vercel 换到别的托管商没关系，换域名就得重新设置。给 Vercel 账号开启硬件密钥两步验证。
2. 在本机构建，把中继地址写进 CSP：

   ```sh
   VITE_SOUS_RELAY=https://relay.example.com wok build web
   ```

   Windows PowerShell：`$env:VITE_SOUS_RELAY="https://relay.example.com"; wok build web`
3. 从本机部署，不要连接 Git 自动部署：

   ```sh
   wok run web deploy
   ```

   第一次运行时 Vercel CLI 会让你登录、选择项目。构建产物里的 `vercel.json` 带着严格的 CSP：脚本只允许来自页面自己，网络只允许连到这个中继。

## 3. 首次设置

1. 打开 `https://sous.example.com`，填写中继地址和第 1 步得到的认领码，点“创建 Passkey 并完成设置”，刷 Face ID、指纹或 Windows PIN。
2. 推荐使用同步的 Passkey（iCloud 钥匙串或 Google 密码管理器）：换手机直接就能用。sous 不设恢复码，Passkey 全部丢失时，聊天数据无法恢复，机器需要在本机执行 `sous trust reset` 后重新配对。
3. 在“设置 → 机器 → 添加机器”里接入电脑（详见 `connect-machine.md`），至少把一台你信任的常开电脑设为**大脑机器**。大脑机器上线后，聊天和任务就能用了。
4. 模型在“设置 → 模型”里添加，需要刷一次 Face ID，Key 只保存在大脑机器上。也可以在大脑机器的 `~/.sous/brain.yaml` 里写 `providers` 和 `roles`，格式和以前的 `hub.yaml` 相同。

## 从 0.2 迁移

1. 在旧服务器上先导出数据：`sudo -u sous /opt/sous/sous-hub -config /opt/sous/hub.yaml admin backup /tmp/hub.db`，然后把 `/tmp/hub.db` 拷到要当大脑的电脑上。
2. 按上面第 1 到第 3 步部署中继和页面，完成首次设置。
3. 每台电脑都升级到 0.3，用配对串重新配对：0.2 的配对在 0.3 里不再有效。
4. 在大脑机器上执行 `sous brain import hub.db`，然后重启它的 daemon。它当上大脑后会使用这份数据，并加密上传到中继。
5. 在旧服务器上停掉并删除 `sous-hub`、它的数据目录和**全部备份**，并重新生成以前存在 Hub 上的模型 API Key。迁移前留在服务器上的明文，没法保证已经彻底清除。

## 发布新版本（作者）

1. 打 `v*` tag，CI 构建并发布到 GitHub Releases。
2. 在你自己的电脑上，用离线发布密钥给它签名。机器只安装签过名的版本，CI 不持有这把密钥：

   ```sh
   gh release download vX.Y.Z -p checksums.txt
   sous release sign --key ~/.config/sous/release-key.json --tag vX.Y.Z checksums.txt
   gh release upload vX.Y.Z checksums.txt.sig
   ```

3. 发布私钥 `~/.config/sous/release-key.json` 要另外备份到离线介质。丢了它就不能再发布让机器自动更新的版本，只能在每台机器上手动安装新版本。
