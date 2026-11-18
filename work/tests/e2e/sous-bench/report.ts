import { mkdir, readFile, rename, rmdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import type { Attempt } from './attempt';
import type { Binary } from './runtime';
import { home } from './runtime';

export const version = 'strict-paired-v1';
export type Score = { arm: string; planned: number; ok: number; fail: number; invalid: number; points: number | null; hard: number; calls: number; usageCalls: number; input: number; output: number; cached: number; userMs: number | null; p95Ms: number | null; providerP50Ms: number | null; interval: [number, number] | null };
export type Delta = { from: string; to: string; pairs: number; gains: number; regressions: number; points: number | null; interval: [number, number] | null };
export type Identity = { model: string; actualModels: string[]; endpoint: string; effort: string; maxOutput: number; maxCalls: number; totalCalls?: number; jobs: number; mock: boolean; profile: string; suite: string; suiteHash: string; harnessHash: string; scoring: string; platform: string; reference: string; cases: string[] };
export type Run = { id: string; at: string; identity: Identity; signature: string; builds: Omit<Binary, 'hub' | 'node'>[]; scores: Score[]; deltas: Delta[]; outcomes?: { id: string; domain: string; arm: string; status: Attempt['status']; reason?: string; hardFailure: boolean }[]; state: 'completed' | 'incomplete' | 'gate-fail'; preflight?: string; misses: Record<string, string[]>; wallMs: number; };

const pt = (x: number) => Math.round(x * 1000) / 10;
export function percentile(values: number[], p: number) {
  return values.length ? [...values].sort((a, b) => a - b)[Math.max(0, Math.ceil(values.length * p) - 1)]! : null;
}
function binomUpper(n: number, at: number, p: number) {
  if (p === 0) return at === 0 ? 1 : 0;
  if (p === 1) return 1;
  let logC = 0, sum = 0;
  for (let j = 0; j <= n; j++) {
    if (j >= at) sum += Math.exp(logC + j * Math.log(p) + (n - j) * Math.log1p(-p));
    logC += Math.log(n - j) - Math.log(j + 1);
  }
  return Math.min(sum, 1);
}
// Exact two-sided Clopper-Pearson for independent binary tasks; no zero-width interval at a ceiling.
export function exact(k: number, n: number, alpha = .05): [number, number] {
  if (!Number.isInteger(n) || !Number.isInteger(k) || k < 0 || k > n || !n) throw Error('invalid binomial counts');
  const lower = (count: number) => {
    if (count === 0) return 0;
    let l = 0, h = 1;
    for (let i = 0; i < 45; i++) {
      const m = (l + h) / 2;
      if (binomUpper(n, count, m) < alpha / 2) l = m; else h = m;
    }
    return (l + h) / 2;
  };
  return [lower(k), 1 - lower(n - k)];
}

export function score(name: string, attempts: Attempt[], planned: number, mock: boolean): Score {
  const rows = attempts.filter(a => a.arm === name);
  const ok = rows.filter(a => a.status === 'pass');
  const fail = rows.filter(a => a.status === 'fail');
  const invalid = planned - ok.length - fail.length;
  const scored = invalid === 0 && planned > 0;
  const calls = rows.flatMap(a => a.calls);
  const usage = calls.filter(a => Number.isFinite(a.input) && Number.isFinite(a.output));
  const latency = ok.map(a => a.ms);
  return { arm: name, planned, ok: ok.length, fail: fail.length, invalid, points: scored ? pt(ok.length / planned) : null,
    hard: rows.filter(a => a.hardFailure).length, calls: calls.length, usageCalls: usage.length,
    input: usage.reduce((n, x) => n + x.input!, 0), output: usage.reduce((n, x) => n + x.output!, 0), cached: usage.reduce((n, x) => n + (x.cached ?? 0), 0),
    userMs: percentile(latency, .5), p95Ms: latency.length >= 20 ? percentile(latency, .95) : null,
    providerP50Ms: percentile(calls.filter(x => x.status === 200 && !x.error && !mock).map(x => x.ms), .5),
    interval: scored && !mock ? exact(ok.length, planned).map(pt) as [number, number] : null };
}

export function pair(from: string, to: string, rows: Attempt[], planned: number, mock: boolean): Delta {
  const byId = new Map(rows.filter(a => a.arm === from && a.status !== 'invalid').map(a => [a.id, a]));
  const pairs = rows.filter(a => a.arm === to && a.status !== 'invalid' && byId.has(a.id));
  const gains = pairs.filter(a => a.status === 'pass' && byId.get(a.id)!.status === 'fail').length;
  const regressions = pairs.filter(a => a.status === 'fail' && byId.get(a.id)!.status === 'pass').length;
  const complete = pairs.length === planned && planned > 0;
  // Simultaneous 97.5% intervals for both discordance probabilities give a conservative 95% difference interval.
  const g = complete ? exact(gains, pairs.length, .025) : null;
  const r = complete ? exact(regressions, pairs.length, .025) : null;
  return { from, to, pairs: pairs.length, gains, regressions, points: complete ? pt((gains - regressions) / pairs.length) : null,
    interval: g && r && !mock ? [pt(g[0] - r[1]), pt(g[1] - r[0])] : null };
}

const clean = (s: string) => s.replaceAll('|', '\\|').replace(/\r?\n/g, ' ');
const points = (v: number | null) => v === null ? '—' : v.toFixed(1);
const interval = (v: [number, number] | null) => v ? ` [${v[0].toFixed(1)}, ${v[1].toFixed(1)}]` : '';
const signed = (v: number | null) => v === null ? '—' : `${v > 0 ? '+' : ''}${v.toFixed(1)} pp`;
export const buildName = (b: Omit<Binary, 'hub' | 'node'>) => `${b.label} (${b.commit.slice(0, 8)})`;

export function archiveText(runs: Run[]) {
  return `# Sous 主管 Benchmark：历次结果\n\n由 runner 生成，请勿手工改表。完整结构化索引是同目录的 \`results.ndjson\`；逐题证据和独立报告在 \`work/logs/sous-bench/<run-id>/\`（本地忽略）。\n\n- 主管分是限定条件下严格成功率，不是综合产品分。\`offline\` 只用于验收设施，不能当模型成绩。\n- 差值以百分点表示。区间为固定题库的采样不确定性提示，不代表真实用户总体；区间跨零时不能宣布提升。\n- 后续仅在相同可比签名下对照；换题库、模型、预算等需重建基线。v0.3.0 不纳入基线。\n- \`supervisor-v1\` 为发现判题缺陷前的预跑，保留原记录，不用作正式基线。见[基线验收与判题审计](tasks.md)。\n\n| 时间 (UTC) / Run ID | 模式 | 模型 / 题库 | 构建 | 各臂得分（通过数/计划数） | 编排差 / 版本差 | 门禁 / 完整性 |\n|---|---|---|---|---|---|---|\n${runs.map(r => {
    const s = r.scores.map(s => `${s.arm}: ${points(s.points)} (${s.ok}/${s.planned})`).join('；') || '—';
    const d = r.deltas.map(d => `${d.to}−${d.from} ${signed(d.points)}${interval(d.interval)}`).join('；') || '—';
    return `| ${r.at} / \`${r.id}\` | ${r.identity.mock ? 'offline' : clean(r.identity.profile)} | ${clean(r.identity.model)} / ${r.identity.suite} | ${r.builds.map(buildName).map(clean).join(' → ') || '—'} | ${s} | ${d} | ${r.state}${r.preflight ? ` / ${clean(r.preflight)}` : ''} |`;
  }).join('\n')}\n`;
}

export function reportText(run: Run, rows: Attempt[]) {
  const lines = [
    `# Sous benchmark ${run.id}`, '', `- 状态：**${run.state}**${run.preflight ? `（${run.preflight}）` : ''}`,
    `- ${run.identity.mock ? '离线设施验收，**不是主管能力分**' : '真实主管模型，限定共同工具和合成环境；不是整产品分'}`,
    `- 配置模型 / 回包模型：\`${run.identity.model}\` / ${run.identity.actualModels.join(', ') || '不可核实'}`,
    `- 题库：\`${run.identity.suite}\`；可比签名：\`${run.signature}\``,
    `- 构建：${run.builds.map(buildName).join(' → ') || '无'}`, '',
    '| 实验臂 | 严格完成（95% 区间） | 通过 / 失败 / 无效 | 硬失败 | 成功耗时 P50 / P95 | 模型往返 P50 | 模型调用 | 已核算调用 / 输入 / 输出 / 缓存 token |',
    '|---|---:|---:|---:|---:|---:|---:|---|',
    ...run.scores.map(s => `| ${s.arm} | ${points(s.points)}${interval(s.interval)} | ${s.ok} / ${s.fail} / ${s.invalid} | ${s.hard} | ${s.userMs ?? '—'} / ${s.p95Ms ?? '—'} ms | ${s.providerP50Ms ?? '—'} ms | ${s.calls} | ${s.usageCalls} / ${s.usageCalls ? s.input : '—'} / ${s.usageCalls ? s.output : '—'} / ${s.usageCalls ? s.cached : '—'} |`),
    '', ...run.scores.map(s => `- ${s.arm}：成功任务均摊调用（含失败消耗）${s.ok ? (s.calls / s.ok).toFixed(1) : '—'}；输入/输出 token 每成功任务 ${s.ok && s.usageCalls === s.calls && s.calls ? `${Math.round(s.input / s.ok)}/${Math.round(s.output / s.ok)}` : '—'}`),
    '', 'P95 至少需 20 次成功尝试。耗时不含环境启动，受网关流缓冲和负载条件影响。订阅账单不可逐题核算，不显示货币成本。', '',
    '| 配对 | 共同任务 | 完成率差（保守 95% 区间） | 失败→通过 | 通过→失败 |', '|---|---:|---:|---:|---:|',
    ...run.deltas.map(d => `| ${d.to} − ${d.from} | ${d.pairs} | ${signed(d.points)}${interval(d.interval)} | ${d.gains} | ${d.regressions} |`),
    '', '参考编排器不是模型上限。区间按独立任务假设，仅供这个小题库内参考；区间含零时未证明方向。', '',
    '| 场景 | 域 | 臂 | 结果 | 失败断言或原因 |', '|---|---|---|---|---|',
    ...rows.map(a => `| ${a.id} | ${a.domain} | ${a.arm} | ${a.status} | ${clean([a.reason, ...a.checks.filter(c => !c.pass).map(c => c.id)].filter(Boolean).join(', '))} |`), '',
  ];
  const domains = [...new Set(rows.map(a => a.domain))].sort();
  if (domains.length) {
    const names = run.scores.map(s => s.arm);
    lines.push('## 分项：通过 / 已观测有效 / 已观测', '', '小分项仅供定位，不做排序或独立提升结论。缺题另见完整性。', '', `| 维度 | ${names.join(' | ')} |`, `|---|${names.map(() => '---:|').join('')}`);
    for (const domain of domains) lines.push(`| ${domain} | ${names.map(arm => {
      const sample = rows.filter(a => a.domain === domain && a.arm === arm);
      return `${sample.filter(a => a.status === 'pass').length}/${sample.filter(a => a.status !== 'invalid').length}/${sample.length}`;
    }).join(' | ')} |`);
    lines.push('');
  }
  return lines.join('\n');
}

export async function archive(run: Run, rows: Attempt[], redact: (s: string) => string) {
  const logs = join(home, 'work/logs/sous-bench', run.id);
  await mkdir(logs, { recursive: true, mode: 0o700 });
  await writeFile(join(logs, 'summary.json'), redact(JSON.stringify(run, null, 2)) + '\n', { mode: 0o600 });
  await writeFile(join(logs, 'attempts.ndjson'), rows.map(r => redact(JSON.stringify(r))).join('\n') + '\n', { mode: 0o600 });
  await writeFile(join(logs, 'report.md'), redact(reportText(run, rows)), { mode: 0o600 });
  const spec = join(home, 'work/specs/sous-benchmark');
  const index = join(spec, 'results.ndjson');
  const lock = join(home, 'work/logs/sous-bench', '.archive-lock');
  for (let i = 0;; i++) {
    try { await mkdir(lock); break; } catch (e: any) { if (e.code !== 'EEXIST' || i === 60) throw e; await Bun.sleep(100); }
  }
  try {
    const orig = await readFile(index, 'utf8').catch(e => { if (e.code === 'ENOENT') return ''; throw e; });
    const raw = orig + redact(JSON.stringify(run)) + '\n';
    const all: Run[] = raw.trim().split('\n').map(x => JSON.parse(x));
    const jsonTmp = join(spec, `.results-${run.id}.ndjson`), mdTmp = join(spec, `.results-${run.id}.md`);
    await writeFile(jsonTmp, raw);
    await writeFile(mdTmp, archiveText(all));
    await rename(jsonTmp, index);
    await rename(mdTmp, join(spec, 'results.md'));
  } finally { await rmdir(lock); }
  return logs;
}
