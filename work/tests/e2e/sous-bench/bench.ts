import { createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { parseEnv } from 'node:util';
import { attempt, type Attempt } from './attempt';
import { build } from './build';
import { cases, fixtureSeed, suiteId } from './cases';
import { BenchError, Model, tools, type ModelConfig } from './model';
import { archive, archiveText, pair, reportText, score, version, type Run, type Identity } from './report';
import { home, referencePrompt, type Binary } from './runtime';

const sha = (x: string) => createHash('sha256').update(x).digest('hex');
const sources = ['attempt.ts', 'bench.ts', 'build.ts', 'cases.ts', 'job-resolution.ts', 'model.ts', 'report.ts', 'runtime.ts', 'worker.ts'];
const flags = 'mode version candidate model endpoint effort key-env key-file jobs case max-output max-calls total-calls'.split(' ');
function parseOptions(args: string[]) {
  const result = new Map<string, string>();
  for (let i = 0; i < args.length; i += 2) {
    const key = args[i]!.replace(/^--/, ''), val = args[i + 1];
    if (!args[i]!.startsWith('--') || !flags.includes(key) || result.has(key) || !val || val.startsWith('--')) throw new BenchError('invalid-arguments');
    result.set(key, val);
  }
  return result;
}
const help = `Sous supervisor benchmark.
  bun run bench run --mode offline --version v0.2.4
  bun run bench run --mode live --version v0.2.4 --key-env OPENCODE_API_KEY
  bun run bench run --mode live --version v0.2.4 --candidate <commit> --key-env OPENCODE_API_KEY
  bun run bench show <run-id>
  bun run bench compare <older-run-id> <newer-run-id>
  bun run bench history
Options: --key-file <file> --endpoint <HTTPS Chat Completions URL> --model <id> --effort <label>
         --case <comma-separated subset> --jobs 3 --max-output 4096 --max-calls 48 --total-calls 400`;
async function readArchived(id: string) {
  if (!/^\d{8}t\d{6}z-[a-f0-9]{8}$/.test(id)) throw new BenchError('invalid-run-id');
  const log = join(home, 'work/logs/sous-bench', id);
  const raw = await Bun.file(join(home, 'work/specs/sous-benchmark/results.ndjson')).text();
  const run = raw.trim().split('\n').map(line => JSON.parse(line) as Run).find(r => r.id === id);
  if (!run) throw new BenchError('run-not-in-archive');
  const evidence = await Bun.file(join(log, 'attempts.ndjson')).text().catch(() => '');
  const rows: Attempt[] = evidence.trim() ? evidence.trim().split('\n').map(x => JSON.parse(x)) : (run.outcomes ?? []).map(x => ({ ...x, ms: 0, calls: [], checks: [], observedAnswerMs: [], evidence: { tasks: [], actions: [], memoryCount: 0, assistant: [], checkpoints: [] } }));
  return { run, rows };
}
async function main() {
  const [cmd = 'help', ...args] = process.argv.slice(2);
  if (cmd === 'help' || cmd === '--help') { console.log(help); return 0; }
  if (cmd === 'history') {
    const raw = await Bun.file(join(home, 'work/specs/sous-benchmark/results.ndjson')).text();
    console.log(archiveText(raw.trim().split('\n').filter(Boolean).map(s => JSON.parse(s)))); return 0;
  }
  if (cmd === 'show' && args.length === 1) {
    const { run, rows } = await readArchived(args[0]!);
    console.log(reportText(run, rows)); return 0;
  }
  if (cmd === 'compare' && args.length === 2) {
    const a = await readArchived(args[0]!), b = await readArchived(args[1]!);
    if (a.run.signature !== b.run.signature || [a.run, b.run].some(r => r.preflight || r.state === 'incomplete' || r.scores.some(s => s.invalid)) || a.run.identity.mock || b.run.identity.mock) throw new BenchError('incompatible-or-incomplete-runs');
    const arm = (x: typeof a) => x.rows.some(row => row.arm === 'candidate') ? 'candidate' : 'baseline';
    const rows = [a, b].flatMap((x, i) => x.rows.filter(r => r.arm === arm(x)).map(r => ({ ...r, arm: i === 0 ? 'older' : 'newer' })));
    console.log(JSON.stringify({ crossSession: true, note: 'Descriptive only: use simultaneous --candidate for an upgrade claim.', ...pair('older', 'newer', rows, a.run.identity.cases.length, false) }, null, 2));
    return 0;
  }
  if (cmd !== 'run') throw new BenchError('unknown-command');
  const opts = parseOptions(args);
  const int = (key: string, fallback: number, min: number, max: number) => {
    const n = Number(opts.get(key) ?? fallback);
    if (!Number.isSafeInteger(n) || n < min || n > max) throw new BenchError(`invalid-${key}`);
    return n;
  };
  const mode = opts.get('mode') ?? 'offline';
  if (mode !== 'offline' && mode !== 'live') throw new BenchError('invalid-mode');
  const mock = mode === 'offline';
  const endpoint = new URL(opts.get('endpoint') ?? 'https://opencode.ai/inference/go/openai/v1/chat/completions');
  if (endpoint.protocol !== 'https:' || endpoint.username || endpoint.password || endpoint.search || endpoint.hash || !endpoint.pathname.endsWith('/chat/completions')) throw new BenchError('endpoint-must-be-clean-https-chat-completions');
  const modelName = mock ? 'protocol-mock' : opts.get('model') ?? 'deepseek-v4.1-flash';
  const effort = opts.get('effort') ?? '';
  if (!/^[a-z0-9][a-z0-9._:/-]{0,119}$/i.test(modelName) || !/^(|low|medium|high|xhigh|max)$/.test(effort)) throw new BenchError('invalid-model-or-effort');
  const keyName = opts.get('key-env') ?? 'OPENCODE_API_KEY';
  if (!/^[A-Z_][A-Z_0-9]*$/.test(keyName)) throw new BenchError('invalid-key-env-name');
  let key = !mock ? process.env[keyName] ?? '' : '';
  const jobs = int('jobs', 3, 1, 8), maxOutput = int('max-output', 4096, 128, 16000), maxCalls = int('max-calls', 48, 1, 100), totalCalls = int('total-calls', 400, 2, 1600);
  const wanted = opts.get('case')?.split(',');
  if (wanted?.some(id => !cases.some(c => c.id === id))) throw new BenchError('unknown-case-id');
  const selected = wanted ? cases.filter(c => wanted.includes(c.id)) : cases;
  const id = `${new Date().toISOString().replace(/[-:]/g, '').replace(/\.\d+Z$/, 'z').replace('T', 't')}-${crypto.randomUUID().slice(0, 8)}`;
  const started = performance.now();
  const identity: Identity = { model: modelName, actualModels: [], endpoint: mock ? '(none)' : endpoint.toString(), effort: effort || '(provider default)', maxOutput, maxCalls, totalCalls, jobs, mock, profile: 'matched-tool-chat-v1', suite: suiteId + (wanted ? '-subset' : ''), suiteHash: sha(JSON.stringify({ selected, fixtureSeed, tools, referencePrompt })), harnessHash: sha((await Promise.all(sources.map(p => Bun.file(join(import.meta.dir, p)).text()))).join('\0')), scoring: version, platform: `${process.platform}/${process.arch}`, reference: sha(referencePrompt), cases: selected.map(c => c.id) };
  const cancel = new AbortController();
  for (const sig of ['SIGINT', 'SIGTERM'] as const) process.once(sig, () => cancel.abort(new Error('interrupted')));
  const rows: Attempt[] = [], builds: Binary[] = [];
  let preflight: string | undefined;
  const arms = ['reference', 'baseline', ...(opts.has('candidate') ? ['candidate'] : [])];
  try {
    builds.push(await build(opts.get('version') ?? 'v0.2.4', cancel.signal));
    if (opts.has('candidate')) builds.push(await build(opts.get('candidate')!, cancel.signal));
    if (!key && !mock && opts.has('key-file')) key = parseEnv(await readFile(resolve(home, opts.get('key-file')!), 'utf8'))[keyName] ?? '';
    if (!mock && !key) throw new BenchError('missing-model-credential');
    const cfg: ModelConfig = { endpoint: endpoint.toString(), model: modelName, key, effort, output: maxOutput, maxCalls, mock, total: { calls: 0, cap: totalCalls } };
    if (!mock) {
      const data = await new Model(cfg).request([{ role: 'user', content: 'Reply with only OK.' }], [], 'preflight', cancel.signal);
      if (!data.parsed.text.trim()) throw new BenchError('provider-preflight-empty');
      if (data.parsed.model) identity.actualModels.push(data.parsed.model);
      console.log(`Provider preflight OK: ${data.parsed.model ?? '(actual model unreported)'}`);
    }
    console.log(`${id}: ${selected.length} task(s) × ${arms.length} arm(s), ${mode}, ${modelName}`);
    const pool = [...selected].sort((a, b) => sha(`order/${a.id}`).localeCompare(sha(`order/${b.id}`)));
    let next = 0;
    await Promise.all(Array.from({ length: Math.min(jobs, pool.length) }, async () => {
      while (!cancel.signal.aborted && next < pool.length) {
        const index = next++, c = pool[index]!, offset = index % arms.length;
        const order = [...arms.slice(offset), ...arms.slice(0, offset)];
        if (Math.floor(index / arms.length) % 2) order.reverse();
        for (const arm of order) {
          if (cancel.signal.aborted || cfg.total.calls >= totalCalls) break;
          const row = await attempt(c, arm, builds[arm === 'candidate' ? 1 : 0]!, cfg, cancel.signal);
          rows.push(row);
          const failures = [row.reason, ...row.checks.filter(c => !c.pass).map(c => c.id)].filter(Boolean).join(',');
          console.log(`${arm.padEnd(10)} ${c.id.padEnd(31)} ${row.status.padEnd(7)} ${row.ms}ms ${row.calls.length} calls${failures ? ` (${failures})` : ''}`);
        }
      }
    }));
  } catch (e) {
    preflight = e instanceof BenchError ? e.code : opts.has('key-file') && !key ? 'credential-file-read-error' : 'run-infrastructure-error';
    console.error(`Run could not complete: ${preflight}`);
  }
  identity.actualModels = [...new Set([...identity.actualModels, ...rows.flatMap(r => r.calls.map(c => c.model).filter((m): m is string => !!m))])].sort();
  if (!mock && !preflight && (identity.actualModels.length !== 1 || arms.some(a => !rows.some(r => r.arm === a && r.calls.some(c => c.model === identity.actualModels[0]))))) preflight = 'actual-model-missing-or-drift';
  const scores = arms.map(a => score(a, rows, selected.length, mock));
  const pairings = [['reference', 'baseline'], ...(arms.includes('candidate') ? [['baseline', 'candidate'], ['reference', 'candidate']] : [])];
  const deltas = pairings.map(([from, to]) => pair(from!, to!, rows, selected.length, mock));
  if (preflight) {
    for (const s of scores) { s.points = null; s.interval = null; }
    for (const d of deltas) { d.points = null; d.interval = null; }
  }
  const misses = Object.fromEntries(arms.map(arm => [arm, selected.filter(c => !rows.some(a => a.id === c.id && a.arm === arm && a.status !== 'invalid')).map(c => c.id)]));
  rows.sort((a, b) => a.id.localeCompare(b.id) || arms.indexOf(a.arm) - arms.indexOf(b.arm));
  const run: Run = { id, at: new Date().toISOString(), identity, signature: sha(JSON.stringify({ ...identity, totalCalls })), builds: builds.map(({ hub: _h, node: _n, ...b }) => b), scores, deltas, outcomes: rows.map(({ id, domain, arm, status, reason, hardFailure }) => ({ id, domain, arm, status, reason, hardFailure })), preflight, state: scores.some(s => s.hard) ? 'gate-fail' : scores.some(s => s.invalid) || preflight ? 'incomplete' : 'completed', misses, wallMs: Math.round(performance.now() - started) };
  const log = await archive(run, rows, text => key ? text.replaceAll(key, '[credential-redacted]') : text);
  console.log(`${run.state}: ${scores.map(s => `${s.arm} ${s.points ?? '—'} (${s.ok}/${s.planned})`).join('; ')}\nReport: ${join(log, 'report.md')}`);
  if (scores.some(s => s.hard)) return 1;
  if (run.state === 'incomplete') return 2;
  return mock && scores.some(s => s.fail) ? 1 : 0;
}
try { process.exitCode = await main(); }
catch (e) { console.error(e instanceof BenchError ? e.code : 'unexpected-benchmark-infrastructure-error'); process.exitCode = 2; }
