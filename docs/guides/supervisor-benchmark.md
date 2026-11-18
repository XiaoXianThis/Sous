# 如何评测和对照 Sous 主管

[主管 Benchmark](../../work/tests/e2e/sous-bench/README.md) 从已提交的 Git 版本构建隔离的 Hub/Node，通过合成工作区和确定性执行 agent 测主管。真实模型只评主管，不评 worker 的编程质量；不读取生产数据。无需逐题人工评分。

- **验收评测器**：在 runner 目录运行 `bun run test` 和 `bun run bench run --mode offline --version v0.2.4`。离线成功率不是真实模型成绩。
- **评估版本升级**：按 runner 文档提供评测凭据，使用 `--mode live --version v0.2.4 --candidate <提交哈希>`。同一批次对照参考、基线与候选。同一模型和题库下看候选−基线，再检查反向翻转和硬门禁。模型、题库或预算变化时重建同期基线。
- **读成绩**：主分是严格通过题比例；耗时、模型调用、token 和硬门禁独立展示。参考编排器不是模型上限。配对区间跨零时，本批数据不能确认提升或退化；不得用离线档、缺题或不同签名的历史分数补结论。
- **查档**：[历次成绩表](../../work/specs/sous-benchmark/results.md) 自动更新；[基线记录](../../work/specs/sous-benchmark/baseline-v0-2-4.md) 说明可用基线和判题审计。详细合成证据默认在本机忽略目录，可按 runner 文档备份。
