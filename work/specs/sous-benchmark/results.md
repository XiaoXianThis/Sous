# Sous 主管 Benchmark：历次结果

由 runner 生成，请勿手工改表。完整结构化索引是同目录的 `results.ndjson`；逐题证据和独立报告在 `work/logs/sous-bench/<run-id>/`（本地忽略）。

- 主管分是限定条件下严格成功率，不是综合产品分。`offline` 只用于验收设施，不能当模型成绩。
- 差值以百分点表示。区间为固定题库的采样不确定性提示，不代表真实用户总体；区间跨零时不能宣布提升。
- 后续仅在相同可比签名下对照；换题库、模型、预算等需重建基线。v0.3.0 不纳入基线。
- `supervisor-v1` 为发现判题缺陷前的预跑，保留原记录，不用作正式基线。见[基线验收与判题审计](tasks.md)。

| 时间 (UTC) / Run ID | 模式 | 模型 / 题库 | 构建 | 各臂得分（通过数/计划数） | 编排差 / 版本差 | 门禁 / 完整性 |
|---|---|---|---|---|---|---|
| 2026-11-18T02:57:59.625Z / `20261118t025757z-eade9b1e` | offline | protocol-mock / supervisor-v1-subset | v0.2.4 (e7b7672a) | reference: — (0/5)；baseline: — (0/5) | baseline−reference — | incomplete |
| 2026-11-18T02:58:32.862Z / `20261118t025831z-96ee4150` | offline | protocol-mock / supervisor-v1-subset | v0.2.4 (e7b7672a) | reference: — (0/5)；baseline: — (0/5) | baseline−reference — | incomplete |
| 2026-11-18T03:00:17.921Z / `20261118t025948z-074d6d0a` | offline | protocol-mock / supervisor-v1 | v0.2.4 (e7b7672a) | reference: 11.8 (2/17)；baseline: 11.8 (2/17) | baseline−reference 0.0 pp | completed |
| 2026-11-18T03:01:14.842Z / `20261118t030111z-7dd25324` | offline | protocol-mock / supervisor-v1-subset | v0.2.4 (e7b7672a) | reference: 33.3 (1/3)；baseline: 33.3 (1/3) | baseline−reference 0.0 pp | completed |
| 2026-11-18T03:02:42.753Z / `20261118t030238z-a1118ab3` | offline | protocol-mock / supervisor-v1-subset | v0.2.4 (e7b7672a) | reference: 100.0 (3/3)；baseline: 100.0 (3/3) | baseline−reference 0.0 pp | completed |
| 2026-11-18T03:04:42.021Z / `20261118t030413z-fcd2c33a` | offline | protocol-mock / supervisor-v1 | v0.2.4 (e7b7672a) | reference: 94.1 (16/17)；baseline: 94.1 (16/17) | baseline−reference 0.0 pp | completed |
| 2026-11-18T03:06:07.107Z / `20261118t030537z-a8cfde84` | offline | protocol-mock / supervisor-v1 | v0.2.4 (e7b7672a) | reference: 100.0 (17/17)；baseline: 100.0 (17/17) | baseline−reference 0.0 pp | completed |
| 2026-11-18T03:11:39.519Z / `20261118t031134z-7d1595fd` | offline | protocol-mock / supervisor-v1-subset | v0.2.4 (e7b7672a) → v0.2.4 (e7b7672a) | reference: 100.0 (1/1)；baseline: 100.0 (1/1)；candidate: 100.0 (1/1) | baseline−reference 0.0 pp；candidate−baseline 0.0 pp；candidate−reference 0.0 pp | completed |
| 2026-11-18T03:13:25.166Z / `20261118t030630z-2c01019d` | matched-tool-chat-v1 | deepseek-v4.1-flash / supervisor-v1 | v0.2.4 (e7b7672a) | reference: 64.7 (11/17)；baseline: 76.5 (13/17) | baseline−reference +11.8 pp [-29.2, 46.8] | gate-fail |
| 2026-11-18T03:16:44.202Z / `20261118t031548z-e02b8171` | offline | protocol-mock / supervisor-v2 | v0.2.4 (e7b7672a) → v0.2.4 (e7b7672a) | reference: 100.0 (17/17)；baseline: 100.0 (17/17)；candidate: 100.0 (17/17) | baseline−reference 0.0 pp；candidate−baseline 0.0 pp；candidate−reference 0.0 pp | completed |
| 2026-11-18T03:18:25.596Z / `20261118t031729z-39da3fd4` | offline | protocol-mock / supervisor-v2 | v0.2.4 (e7b7672a) → v0.2.4 (e7b7672a) | reference: 100.0 (17/17)；baseline: 100.0 (17/17)；candidate: 100.0 (17/17) | baseline−reference 0.0 pp；candidate−baseline 0.0 pp；candidate−reference 0.0 pp | completed |
| 2026-11-18T07:52:41.969Z / `20261118t074631z-9963d54b` | matched-tool-chat-v1 | deepseek-v4.1-flash / supervisor-v2 | v0.2.4 (e7b7672a) | reference: 70.6 (12/17)；baseline: 82.4 (14/17) | baseline−reference +11.8 pp [-29.2, 46.8] | gate-fail |
