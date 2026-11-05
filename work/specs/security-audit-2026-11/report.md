# Sous 仓库密钥泄露检查报告

- 日期：2026-11-05
- 仓库：`XiaoXianThis/Sous`（GitHub 显示为 **PUBLIC**）
- 远程：`https://github.com/XiaoXianThis/Sous.git`
- 检查分支：`sous/sous-github-7zqy64mq2s`
- 检查时 HEAD：`c52c7825004fdbc482176783234de0ff0ee806d9`（与 `origin/main` 相同）
- 方式：只读检查；未修改代码、历史、远程配置，未 push，未安装新工具
- 工具：`git`、`gh`、`ripgrep`、Python 启发式扫描（本机没有 gitleaks / trufflehog / git-secrets）

## 1. 结论

**截至本次检查，没有发现真实的 API Key、令牌、密码、私钥或其他凭据被提交并推送到 GitHub 远程仓库。**

当前工作树的 425 个被跟踪文件中没有真实凭据；26 个提交的完整历史、已删除文件、曾跟踪过的文件名、提交信息、标签信息以及本地不可达对象中，也没有发现真实凭据。发现的少量 `sk-`、`Bearer`、`user:password@` 内容均为占位符或测试假值，详见第 3 节。

需要注意：该仓库是公开仓库，任何真实凭据一旦进入历史都会立即暴露。当前没有证据表明发生过这种情况，但仍建议保持第 5 节的防护措施。

## 2. 检查范围与结果

| 范围 | 方法 | 结果 |
|---|---|---|
| 远程地址 | `git remote -v`、`git config` | 仅 `origin`，URL 中不含用户名/令牌 |
| 仓库可见性 | `gh repo view XiaoXianThis/Sous --json visibility,isPrivate` | `PUBLIC` |
| 当前工作树 | 425 个 `git ls-files` 文件，按文件名和内容扫描 | 未发现真实凭据 |
| `.env` 及变体 | 文件名扫描 + 历史文件名扫描 | 只存在 `apps/core/.env.example`，值为空或占位符 |
| 私钥/证书文件 | `*.pem`、`*.key`、`*.p12`、`*.pfx`、`id_rsa*`、`id_ed25519*`、`vapid.json`、`*.db` 等 | 工作树中均不存在 |
| 常见密钥前缀 | `sk-`、`sk-ant-`、`ghp_`、`gho_`、`github_pat_`、`xox*-`、`AKIA`、`ASIA`、`AIza`、`ya29.`、`glpat-`、`sk_live_`、`hf_`、`npm_`、`pypi-`、`SG.`、`shpat_`、`dop_v1_`、`oc_`、`BEGIN … PRIVATE KEY` 等 | 当前树和历史中均无真实命中；`sk-` 命中均为 `task-list`、`mask-`、占位符等 |
| 敏感赋值 | 历史中所有新增行，匹配 `api_key`、`secret`、`password`、`token`、`credential`、`private_key`、`vapid` 的赋值 | 只有测试假值、变量名、SQL 语句和占位符 |
| 带凭据的 URL | `https?://user:pass@host` 模式 | 只有测试用例中的假 URL，见第 3 节 |
| 提交历史 | 26 个提交，`git log --all -p` 全量补丁扫描 | 未发现真实凭据 |
| 删除过的文件 | `git log --all --diff-filter=D --name-only` | 删除的是 Svelte/TS 组件和测试，没有 `.env`、密钥或证书 |
| 曾跟踪过的文件名 | `git log --all --name-only` 全量去重 | 敏感文件名只有 `apps/core/.env.example` |
| 提交信息/标签/notes | `git log --format=%B`、`git tag -n99`、`git notes list` | 无凭据 |
| 不可达对象 | `git fsck --unreachable`（仅扫描，不清理） | 33 个 blob、4 个 stash/WIP commit、45 个 tree；无真实凭据 |
| 远程分支/PR | `git ls-remote origin`、`gh pr list --state all` | 远程只有 `main`、两个已合并 PR 的分支和 3 个标签；没有其他 PR |
| 本地提交是否都已推送 | `git for-each-ref --contains` 检查每个提交 | 所有本地提交都包含在某个远程 ref 中 |
| CI 配置 | `.github/workflows/ci.yml`、`release.yml` | 只引用 `${{ secrets.GITHUB_TOKEN }}`，没有写死的自定义凭据 |
| GitHub Actions secrets | `gh api repos/.../actions/secrets` | 仓库级 secrets 数量为 0；环境数量为 0 |
| GitHub secret scanning | `gh api repos/.../secret-scanning/alerts` | 告警数量为 0；secret scanning 和 push protection 均已启用 |
| 二进制文件 | 对 PNG 等二进制文件执行 `strings`，匹配常见密钥前缀 | 无命中 |
| 依赖锁文件 | `package-lock.json`、`go.sum` | 只有 npm registry / Go module 校验和，没有私有 registry 令牌 |

### 远程 ref 概况

- `refs/heads/main`：`c52c782`
- `refs/pull/1/head`：`5e9da15`（PR #1，已合并）
- `refs/pull/2/head`：`5cdceaa`（PR #2，已合并）
- 标签：`v0.1.0` → `d423083`，`v0.1.1` → `b6f9bc4`，`v0.1.2` → `f26767d`
- 26 个提交全部可从远程 ref 到达；没有“本地有、远程没有”的提交。

## 3. 发现的疑似项（均非真实凭据）

| 位置 | 类型 | 是否仍在当前版本 | 是否已推送 | 判断与建议 |
|---|---|---|---|---|
| `apps/core/.env.example`，提交 `718e191` | `DEEPSEEK_API_KEY=sk-y…`，长度 16 的占位文本（含 `your-key-here`） | 否。`64d465f` 已改为空值，当前文件只有 `OPENCODE_API_KEY=` | 是，仍在历史中 | 明显的示例占位符，不是可用密钥。无需轮换或改写历史；如果 secret scanner 误报，可在扫描器里加 allowlist |
| `apps/core/internal/web/web_test.go:39`，提交 `36614fc` | 测试 URL 中的假凭据 `http://user:…@example.com/` | 是 | 是 | 用于验证拒绝带凭据 URL 的测试数据，不是真实服务 |
| `apps/core/internal/project/project_test.go:10`，提交 `64d465f` | 测试 URL 中的假凭据 `https://user:…@GitHub.com/XiaoXianThis/Sous/` | 是 | 是 | 用于测试 remote URL 解析，不是真实令牌 |
| `apps/core/internal/mcp/manager_test.go:237`，提交 `3074616` | 测试 URL 中的假凭据 `https://u:…@x.com` | 是 | 是 | 用于测试拒绝带凭据的 MCP URL，不是真实服务 |
| `apps/core/internal/models/models_test.go:36,117`、`provider/provider_test.go:87`、`mcp/client_test.go:77,84` 等 | 测试用 `Bearer k`、`Bearer k1`、`Bearer k2`、`Bearer tok` | 是 | 是 | 单元测试假令牌，不对应任何真实账号 |
| `apps/web/package-lock.json` | `sk-l…` 命中 | 是 | 是 | 来自 `task-list`、`mask-…` 等包名/样式名，不是密钥 |

除上述内容外，没有发现真实凭据。

## 4. `.gitignore` 评估与改进建议

当前根 `.gitignore` 第 52–59 行已覆盖：

- `.env`、`.env.*`（并保留 `.env.example`）
- `*.pem`、`*.key`、`*.p12`、`*.pfx`

`apps/core/.gitignore` 已忽略 `/.data/`，因此默认数据目录里的 `vapid.json`、Hub 数据库和会话数据不会进入 Git。`apps/core/internal/notify/webpush.go` 在首次启动时生成 VAPID 密钥并写入 `dataDir/vapid.json`（权限 0600），测试中使用临时目录，没有写死密钥。

仍然存在一些常见缺口，建议在根 `.gitignore` 的“Secrets and certificates”段追加：

```gitignore
# More private keys and credential files
*.jks
*.keystore
*.p8
*.ovpn
id_rsa*
id_dsa*
id_ecdsa*
id_ed25519*
.ssh/
.npmrc
.pypirc
.netrc
.git-credentials
.aws/
.azure/
.config/gcloud/
.kube/
.docker/config.json
*.kdbx

# App data that can contain device tokens or keys
vapid.json
*.db
*.db-wal
*.db-shm
*.sqlite
*.sqlite3
```

另外建议补充：

```gitignore
# Environment files with a non-dot name, such as the deployment example hub.env
*.env
```

> 注意：当前任务要求严格只读，以上只是建议，本次没有修改 `.gitignore`。如果采纳，`*.env` 与现有 `!.env.example` 不冲突；`apps/core/.env.example` 仍会被保留。

## 5. 处置建议

1. **无需轮换密钥**：没有发现真实凭据。唯一形似密钥的 `sk-y…` 是历史中的示例占位符，不是可用密钥。
2. **无需清理 Git 历史**：没有需要从历史中删除的真实凭据；为一个假占位符改写历史不划算。
3. **保持 secret scanning / push protection 开启**：当前两者均已启用，告警为 0。可以考虑启用 non-provider patterns 和 validity checks，以覆盖自定义令牌格式。
4. **CI 加固**：未来可在 CI 中增加 gitleaks/trufflehog 扫描，并对历史中的 `sk-your-key-here` 加 allowlist；本次按约束没有联网安装工具。
5. **保持密钥只在仓库外**：生产环境的 `/etc/sous/hub.env`、`~/.sous/node.json`、`<data_dir>/vapid.json` 应继续保持 0600/0600/0600 权限；不要复制进仓库。
6. **占位文件保持空值**：`apps/core/.env.example` 只写变量名，不写任何看似真实的 key；当前已经是这样。
7. **补强 `.gitignore`**：按第 4 节追加规则，尤其是 `*.env`、`vapid.json`、`*.db`、SSH/GPG/云厂商凭据文件。

## 6. 盲区与残余风险

- 本机没有 gitleaks / trufflehog，本次使用手工模式匹配、熵启发式扫描和逐条人工判断，不能保证覆盖所有自定义或高熵短令牌。
- GitHub 仓库级 Actions secrets 和环境数量均为 0，但组织级 secrets、外部 CI 系统变量、部署服务器上的环境变量不在 Git 仓库内，无法也不应通过本仓库检查。
- GitHub Issue、PR 评论、Wiki、Release 正文等非 Git 内容不在本次范围；Release 资产只确认了文件名（二进制包和 `checksums.txt`），未逐个下载审计。
- 如果远程曾经被 force-push 覆盖，旧提交可能仍能通过已知 SHA 访问，但无法从公开 ref 枚举；本次只能确认当前 refs 和可达历史干净。
- 二进制文件只做了常见密钥前缀的 `strings` 扫描；如果密钥被加密或编码进图片/压缩包，模式扫描无法识别。
- 本地工作树中没有被忽略的 `.env` 或数据库文件；原仓库目录中的本地 `.env` 即使存在也不会被推送，但本次未读取仓库外文件。

## 7. 复现命令（只读）

```sh
git remote -v
gh repo view XiaoXianThis/Sous --json visibility,isPrivate,url
git rev-list --all --count
git ls-files | wc -l

# 当前树常见密钥模式
rg -n --hidden -g '!.git/**' \
  -e 'sk-[A-Za-z0-9_-]{8,}' -e 'ghp_[A-Za-z0-9]{20,}' \
  -e 'github_pat_' -e 'xox[baprs]-' -e 'AKIA[0-9A-Z]{16}' \
  -e 'AIza[0-9A-Za-z_-]{35}' -e 'BEGIN [A-Z ]*PRIVATE KEY' .

# 历史全量补丁
git log --all -p --pretty=format:'COMMIT %H %s' | rg -n \
  -e 'sk-[A-Za-z0-9_-]{10,}' -e 'ghp_' -e 'github_pat_' \
  -e 'xox[baprs]-' -e 'AKIA' -e 'AIza' -e 'BEGIN .*PRIVATE KEY'

# 历史文件名与删除文件
git log --all --name-only --pretty=format: | sort -u
git log --all --diff-filter=D --name-only --pretty=format: | sort -u

# 不可达对象（只列出，不清理）
git fsck --full --unreachable --no-reflogs

# 远程 refs / PR / 平台侧
git ls-remote origin
gh pr list --repo XiaoXianThis/Sous --state all
gh api repos/XiaoXianThis/Sous/actions/secrets
gh api repos/XiaoXianThis/Sous/secret-scanning/alerts
```

## 8. 最终答复摘要

- **有/没有泄露**：没有发现真实密钥泄露到 `XiaoXianThis/Sous` 远程仓库。
- **检查覆盖**：当前工作树 425 个文件、26 个提交的完整历史、删除文件、曾跟踪文件名、不可达对象、远程 refs、两个已合并 PR、CI 配置、GitHub Actions secrets 和 secret scanning 告警。
- **唯一形似密钥的内容**：历史提交 `718e191` 中的 `apps/core/.env.example` 占位符 `sk-y…`，已在该文件后续版本中改为空值，不是真实密钥。
- **建议**：无需轮换或改写历史；补强 `.gitignore`，保持 push protection，未来在 CI 中加入密钥扫描。
