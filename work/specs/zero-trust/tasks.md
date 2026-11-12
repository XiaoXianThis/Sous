# 任务：零信任（0.3.0-alpha1）

D1–D10 已拍板（见 `design.md` §0）。按里程碑顺序执行，每个里程碑结束时更新契约、`apps/*/AGENTS.md` 和这份清单。

## M0 验证（先做，结果可能改变设计）

- [ ] 0.1 WebAuthn PRF：iOS Safari 主屏幕 PWA、macOS Safari / Chrome、Windows Chrome / Edge（Windows Hello PIN）、Android Chrome、跨设备扫码（hybrid）（design §16.1）
- [ ] 0.2 WebCrypto：P-256 不可导出密钥、AES-GCM `unwrapKey`（iOS Safari 重点）
- [ ] 0.3 X-Wing / HPKE：Go 端实现选定；浏览器端库选定并确认审计状态；两边用同一套测试向量互通
- [ ] 0.4 Secure Enclave / TPM：Go 的访问方式，以及对 cgo 和交叉编译的影响；定下 alpha1 是否先用软件密钥
- [ ] 0.5 Service Worker 能否在解锁前检查资源清单
- [ ] 0.6 Chrome 虚拟认证器是否支持 PRF（决定端到端测试怎么写）
- [ ] 0.7 Vercel 自定义域名的访问速度
- [ ] 0.8 把验证结果写回 `design.md`，有改动的地方先和用户确认

## M1 信任底座（Node 只认用户）

- [ ] 1.1 `internal/trust`：信封、规范化编码、签名文档、名册、会话证书、WebAuthn 断言的验证；`src/trust`：对应的 web 实现；共用测试向量
- [ ] 1.2 身份：首次设置、Passkey PRF 包装、解锁、会话密钥、自动锁定（1 分钟到永不，默认 15 分钟）、`session.end`
- [ ] 1.3 配对：配对串 / 二维码、`sous pair <配对串>`、`sous trust reset`；去掉 6 位配对码和设备令牌
- [ ] 1.4 Node 验证链：名册、任期、序号和哈希链、过期时间、三档授权（design §6）
- [ ] 1.5 Node 本地硬规则：凭据目录、`shell`、`allow_open`、自我冻结（design §10.3）
- [ ] 1.6 审计日志（哈希链）和签名的日志头上报
- [ ] 1.7 发布签名：`work/scripts/release-sign`、编译进来的发布公钥、写死仓库地址、`node.update` 去掉 `repo`、更新改为关键操作

## M2 中继和端到端加密

- [ ] 2.1 `sous-relay`：WebSocket、成员挑战准入、`sous-relay init`、信箱（7 天）、域日志、大块数据、名册和租约广播、配对会合
- [ ] 2.2 域和纪元：密钥包装、轮换、普通 Node 只写不读（按 Run 的流密钥）（design §7）
- [ ] 2.3 加密的事件日志：`eventlog` 写入时加密追加到中继，带任期、序号、哈希链和签名；本地 store 可以从日志重建
- [ ] 2.4 页面迁到 Vercel：中继地址、CSP、`vercel deploy --prebuilt`、资源清单签名和检查；`apps/web/AGENTS.md` 加部署任务
- [ ] 2.5 app 本地缓存（IndexedDB，加密）

## M3 大脑迁出

- [ ] 3.1 大脑模块编进 `sous`：从 `sous-hub` 搬过 supervisor、task、router、trigger、inspector、eventlog、store、provider、mcp、web、notify、sysmap、prompts、tool
- [ ] 3.2 大脑资格（名册）、租约、选主、让位、上任时追赶日志并核对日志头（design §8.2–8.3）
- [ ] 3.3 RPC 适配层：现有 `httpapi` 改为在信封里收发；实时事件推送
- [ ] 3.4 项目自主范围文档、范围检查、范围外的点击卡片、关键操作的断言流程
- [ ] 3.5 推送改由大脑发送（RFC 8291），VAPID 私钥放进主域
- [ ] 3.6 `web_fetch` 默认拒绝局域网；模型 Key、MCP 配置进主域
- [ ] 3.7 去掉 `passkey-reset`、`dev-login` 等后门；本地开发流程（localhost 中继 + 大脑 + 页面）写进 `apps/core/AGENTS.md`

## M4 管控、迁移和验收

- [ ] 4.1 踢出（关键操作）、冻结、解冻、全部暂停；机器列表左滑操作；各台机器的名册确认情况
- [ ] 4.2 app 的审计视图：哈希链核对、日志头比对、异常提醒
- [ ] 4.3 迁移：`sous brain import`；迁移指南写进 `docs/guides/`
- [ ] 4.4 `work/tests/` 下的恶意中继测试套件（design §12 的全部攻击）
- [ ] 4.5 被攻破的普通 Node、被攻破的大脑的测试（requirements 验收 2、3）
- [ ] 4.6 中继数据库明文检查（requirements 验收 4）
- [ ] 4.7 真机使用验收：iPhone、macOS、Windows（requirements 验收 5）
- [ ] 4.8 文档：`docs/api` 的三份新契约（替换 `core-hub-node.md`、`core-hub-http.md`）、`docs/architecture/trust.md`、`apps/core/AGENTS.md`、`apps/web/AGENTS.md`
- [ ] 4.9 发布 `v0.3.0-alpha1`（预发布），用新流程签名
