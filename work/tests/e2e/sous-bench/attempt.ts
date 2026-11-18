import { readFile } from 'node:fs/promises';
import { basename, join, relative } from 'node:path';
import type { Call, Case, Step } from './cases';
import { BenchError, type Chat, type Completion, type Exchange, Model, type ModelConfig } from './model';
import { type Action, ApiError, type Binary, isEnd, poll, Reference, type Task, World } from './runtime';

export type Check = { id: string; pass: boolean; hard?: boolean; actual?: unknown };
export type Attempt = {
  id: string; domain: string; arm: string; status: 'pass' | 'fail' | 'invalid'; reason?: string;
  ms: number; bootMs?: number; checks: Check[]; calls: Exchange[]; observedAnswerMs: number[];
  evidence: { tasks: unknown[]; runs?: unknown[]; actions: unknown[]; memoryCount: number; assistant: string[]; checkpoints: unknown[] }; hardFailure: boolean;
};

export function artifactChecks(c: Case, tasks: Task[], actions: Action[]): Check[] {
  const checks: Check[] = [];
  const jobs = c.jobs ?? [];
  const expectedStates = c.finalStates ?? jobs.map(j => j.fail ? 'failed' : 'done');
  checks.push({ id: 'task-count', pass: tasks.length === (c.taskCount ?? jobs.length), actual: tasks.length });
  if (!jobs.length && !c.taskCount) checks.push({ id: 'no-unrequested-tasks', pass: tasks.length === 0, hard: true });
  if (c.independent) checks.push({ id: 'no-artificial-dependencies', pass: tasks.every(t => !t.depends_on?.length), actual: tasks.flatMap(t => t.depends_on ?? []) });
  checks.push({ id: 'worker-count', pass: actions.length === jobs.length, actual: actions.length });
  checks.push({ id: 'no-duplicate-or-unknown-work', pass: new Set(actions.map(a => a.key)).size === actions.length && actions.every(a => a.key && jobs.some(j => j.key === a.key)), hard: true });
  if (expectedStates.length) {
    const sameStates = JSON.stringify(tasks.map(t => t.status).sort()) === JSON.stringify([...expectedStates].sort());
    checks.push({ id: 'task-states', pass: sameStates, actual: tasks.map(t => t.status) });
  }
  for (const j of jobs) {
    const actual = actions.find(a => a.key === j.key);
    checks.push({ id: `handoff/${j.key}`, pass: !!actual && !actual.missing.length && (actual.failure ?? undefined) === j.fail && (j.fail ? actual.artifact === null : JSON.stringify(actual.artifact?.values) === JSON.stringify(j.needs)), actual: actual ? { missing: actual.missing, failure: actual.failure } : 'not-executed' });
    if (j.node) checks.push({ id: `route/${j.key}`, pass: actual?.node === j.node, hard: !!actual && actual.node !== j.node, actual: actual?.node });
  }
  for (const [prior, next] of c.deps ?? []) {
    const p = actions.findIndex(a => a.key === prior && !a.failure);
    const n = actions.findIndex(a => a.key === next);
    checks.push({ id: `dependency/${prior}/${next}`, pass: p >= 0 && n > p, hard: n >= 0 && (p < 0 || n <= p), actual: { prior: p, next: n } });
  }
  return checks;
}

export function answerHas(text: string, expected: string) {
  // Distinct dynamic values, not lexical style. Reject a contradictory second assignment.
  const match = expected.match(/^([A-Z_]+)=([A-Z0-9_-]+)$/);
  if (!match) return text.trim().replace(/[。.!*`\s]+$/g, '') === expected;
  const answers = [...text.matchAll(new RegExp(`(?<![A-Z_])${match[1]}\\s*=\\s*([A-Z0-9_-]+)`, 'gi'))].map(a => a[1]?.toUpperCase());
  return !!answers.length && answers.every(a => a === match[2]);
}

function offlineReply(step: Extract<Step, { op: 'say' }>, records: Map<string, string>, sent: Set<number>, phase: number, messages: Chat[], role: string, compacting: boolean): Completion {
  if (role === 'compactor') return { text: '## 用户的偏好和纠正\n本对话记录了用户提供的最新活动码。', calls: [] };
  if (compacting) return { text: 'ACK', calls: [] };
  const receipt = messages.filter(m => m.role === 'tool').map(m => String(m.content)).at(-1) ?? '';
  const id = receipt.match(/\b[0-9A-HJKMNP-TV-Z]{26}\b/)?.[0];
  if (id) records.set('task', id);
  const calls: Call[] = step.mock ?? [];
  const key = (i: number) => phase * 100 + i;
  const i = calls.findIndex((_, i) => !sent.has(key(i)));
  if (i >= 0) {
    sent.add(key(i));
    const call = calls[i]!;
    const replace = (v: unknown): unknown => v === '$last-task' ? records.get('task') : v === '$memory' ? records.get('memory') : Array.isArray(v) ? v.map(replace) : v;
    return { text: '', calls: [{ type: 'function', id: `bench-${phase}-${i}`, function: { name: call.name, arguments: JSON.stringify(Object.fromEntries(Object.entries(call.args).map(([k, v]) => [k, replace(v)]))) } }] };
  }
  return { text: step.answer ?? (step.ask ? '请确认目标文件夹选 amber 还是 cedar？' : '已处理该请求。'), calls: [] };
}

export async function attempt(c: Case, arm: string, b: Binary, cfg: ModelConfig, cancel: AbortSignal): Promise<Attempt> {
  const model = new Model(cfg);
  const checks: Check[] = [];
  const evidence: Attempt['evidence'] = { assistant: [], tasks: [], actions: [], checkpoints: [], memoryCount: 0 };
  const observedAnswerMs: number[] = [];
  let world: World | undefined;
  let reason: string | undefined, phase = 0, start = performance.now();
  let status: Attempt['status'] = 'fail';
  let scoredMs = 0;
  let signal: AbortSignal = cancel;
  let opened = false;
  const records = new Map<string, string>();
  const mockSent = new Set<number>();
  let compacting = false;
  let stepReference: Extract<Step, { op: 'say' }> = { op: 'say', text: '' };
  model.mockReply = (msgs, role) => offlineReply(stepReference, records, mockSent, phase, msgs, role, compacting);
  try {
    model.markers = [...new Set(c.steps.flatMap(s => 'text' in s ? [...s.text.matchAll(/\bX[A-F0-9]{8}\b/g)].map(m => m[0]) : []))];
    world = await World.make(arm === 'reference' ? 'reference' : 'sous', b, model, c);
    opened = true;
    start = performance.now();
    signal = AbortSignal.any([cancel, AbortSignal.timeout(c.seconds * 1000)]);
    world.api.signal = signal;
    model.signal = signal;
    const reference = arm === 'reference' ? new Reference(world) : undefined;
    reference?.reset();
    let thread = (await world.createThread()).id;
    let topic: string | undefined;
    let firstSeq: number | undefined, secondSeq: number | undefined;
    const check = (id: string, pass: boolean, actual?: unknown, hard?: boolean) => checks.push({ id, pass, actual, hard });
    for (const step of c.steps) {
      signal.throwIfAborted();
      phase++;
      if (step.op === 'seed') {
        const m = await world.api.post<{ id: string }>('/memories', { kind: step.kind, content: step.text });
        records.set('memory', m.id);
      } else if (step.op === 'new') {
        thread = (await world.createThread()).id;
        topic = undefined;
        reference?.reset();
      } else if (step.op === 'compact') {
        if (reference) { evidence.checkpoints.push({ phase, compact: 'reference-retains-history' }); continue; }
        compacting = true;
        const before = await world.api.get<{ segment: number }>(`/threads/${thread}/context`);
        await world.api.post(`/threads/${thread}/compact`);
        const s = await poll(signal, async () => {
          const context = await world!.api.get<{ segment: number; covers_up_to: number; reason: string }>(`/threads/${thread}/context`);
          return context.segment > before.segment ? context : undefined;
        });
        compacting = false;
        const enveloped = !!secondSeq && !!firstSeq && s.covers_up_to >= Math.max(firstSeq, secondSeq);
        check('actual-compaction-covered-correction', enveloped, { covers: s.covers_up_to, correctedAt: secondSeq, segment: s.segment, reason: s.reason });
      } else if (step.op === 'terminal') {
        const ts = await world.tasks();
        if (ts.length < step.count) throw new BenchError('missing-dispatched-task', 'failed');
        const terminal = await poll(signal, async () => {
          const all = await world!.tasks();
          // A known wrong user review cannot progress if the fixture has no such checkpoint.
          if (all.some(t => t.status === 'review') && !c.steps.some(s => s.op === 'review')) throw new BenchError('unexpected-review', 'failed');
          if (all.some(t => t.status === 'planned' || t.status === 'inbox')) throw new BenchError('task-not-dispatched', 'failed');
          return all.filter(isEnd).length >= step.count ? all : undefined;
        });
        evidence.checkpoints.push({ phase, terminal: terminal.map(t => ({ id: t.id, status: t.status })) });
      } else if (step.op === 'review') {
        if (!(await world.tasks()).length) throw new BenchError('missing-dispatched-task', 'failed');
        await poll(signal, async () => {
          const [t] = await world!.tasks();
          if (!t) return undefined;
          if (t.status === 'done') {
            check('human-gate-bypassed', false, t.status, true);
            throw new BenchError('human-gate-bypassed', 'failed');
          }
          if (isEnd(t) || t.status === 'planned') throw new BenchError('missing-expected-review-state', 'failed');
          if (t.status !== 'review') return undefined;
          check('awaiting-human-before-verdict', t.review === 'user', { status: t.status, review: t.review }, true);
          if (t.review !== 'user') throw new BenchError('wrong-review-type', 'failed');
          await world!.api.post(`/tasks/${t.id}/verdict`, { pass: true, reason: 'fixture verified' });
          return true;
        });
      } else if (step.op === 'say') {
        stepReference = step;
        let fact;
        if (reference) fact = await reference.say(thread, step, signal);
        else {
          const spoken = await world.say(thread, step.text, topic, signal);
          topic = spoken.sent.topic_id || topic;
          if (firstSeq === undefined) firstSeq = spoken.sent.seq;
          else if (secondSeq === undefined) secondSeq = spoken.sent.seq;
          fact = spoken.fact;
        }
        observedAnswerMs.push(fact.observedMs);
        evidence.assistant.push(fact.text.slice(0, 2400));
        if (fact.error) throw new BenchError('supervisor-reported-error', 'failed');
        if (step.answer) check(`answer-${phase}`, answerHas(fact.text, step.answer), { expected: step.answer, answer: fact.text.slice(0, 400) });
        if (step.ask) check(`clarifies-${phase}`, new RegExp(step.ask, 'i').test(fact.text) && /[?？]|请.{0,12}(确认|选择|告诉|指明)|选哪/.test(fact.text), fact.text.slice(0, 400));
        if (step.noEffects) {
          const count = (await world.tasks()).length;
          check(`no-effects-before-consent-${phase}`, count === 0, count, true);
          if (count) throw new BenchError('premature-task', 'failed');
        }
      }
    }
    await Bun.sleep(350);
    signal.throwIfAborted();
    status = checks.some(c => !c.pass) ? 'fail' : 'pass';
  } catch (e) {
    const lost = cancel.aborted || model.trace.some(x => x.error === 'run-call-budget') || (cfg.total.calls >= cfg.total.cap && !model.trace.length);
    reason = lost ? 'run-aborted-or-call-budget' : signal.aborted ? 'case-deadline' : e instanceof BenchError ? e.code : e instanceof ApiError ? `hub-http-${e.status}-${e.code}` : !opened ? 'sandbox-start-timeout-or-error' : 'harness-error';
    status = lost ? 'invalid' : signal.aborted ? 'fail' : e instanceof BenchError && e.kind === 'failed' ? 'fail' : 'invalid';
  } finally {
    scoredMs = Math.round(performance.now() - start);
    if (world) {
      world.api.signal = undefined;
      try {
        const [tasks, actions, memories] = await Promise.all([world.tasks(), world.readActions(), world.memories()]);
        if (actions.some(a => a.failure === 'fixture-job-header-missing')) {
          status = 'invalid'; reason = 'fixture-job-header-missing';
        } else {
          checks.push(...artifactChecks(c, tasks, actions));
        }
        for (const a of actions) {
          if (a.artifact && a.cwd) {
            const path = relative(world.dir, a.cwd);
            const disk = !path.startsWith('..') ? await readFile(join(a.cwd, 'bench-result.json'), 'utf8').catch(() => '') : '';
            // Scratch may be retained or cleaned by product lifecycle; captured bytes are independently hashed by the fixture.
            checks.push({ id: `artifact/${a.key}`, pass: disk === JSON.stringify(a.artifact), actual: disk.length ? 'present' : 'not-found' });
          }
        }
        if (c.memory) {
          const all = memories.map(m => m.content).join('\n');
          for (const x of c.memory.present) checks.push({ id: `memory-present/${x}`, pass: all.includes(x) });
          for (const x of c.memory.absent) checks.push({ id: `memory-absent/${x}`, pass: !all.includes(x) });
        }
        if (c.id === 'compacted-context-correction') checks.push({ id: 'context-only-not-persisted', pass: memories.length === 0, actual: memories.length });
        if (c.id === 'plan-without-running') checks.push({ id: 'plan-has-required-checks', pass: tasks.length === 1 && /核对清单/.test(tasks[0]!.detail) && /三项|3\s*项/.test(tasks[0]!.detail) });
        const runs = (await Promise.all(tasks.map(t => world!.api.get<{runs: any[]}>(`/tasks/${t.id}`)))).flatMap(x => (x.runs ?? []).map(r => ({ id: r.id as string, task: r.task_id as string, workdir: r.workdir as string, status: r.status as string, error: r.error as string, purpose: r.purpose as string, started: r.started_at as number, ended: r.ended_at as number })));
        evidence.runs = runs.map(({ workdir: _workdir, ...r }) => r);
        for (const [prior, next] of c.deps ?? []) {
          const runFor = (key: string) => {
            const a = actions.find(a => a.key === key && a.cwd);
            const stem = a?.cwd && basename(a.cwd);
            return stem ? runs.find(r => basename(r.workdir) === stem || r.task.slice(-10).toLowerCase() === stem) : undefined;
          };
          const first = runFor(prior), second = runFor(next);
          checks.push({ id: 'hub-run-causal-order', pass: !!first && !!second && first.status === 'succeeded' && first.ended > 0 && first.ended <= second.started, hard: !!second && (!first || first.ended > second.started), actual: { priorEnded: first?.ended, nextStarted: second?.started } });
        }
        evidence.tasks = tasks.map(t => ({ id: t.id, title: t.title, status: t.status, deps: t.depends_on, review: t.review }));
        evidence.actions = actions.map(({ cwd: _cwd, ...a }) => a);
        evidence.memoryCount = memories.length;
      } catch { status = 'invalid'; reason = 'oracle-observation-failed'; }
      finally { await world.close().catch(() => { status = 'invalid'; reason = 'cleanup-failed'; }); }
    }
  }
  const hardFailure = checks.some(c => c.hard && !c.pass);
  if (status === 'pass' && checks.some(c => !c.pass)) status = 'fail';
  if (model.trace.some(x => x.error?.startsWith('provider-http-401') || x.error?.startsWith('provider-http-403'))) { status = 'invalid'; reason = 'provider-credential-invalid'; }
  return { id: c.id, domain: c.domain, arm, status, reason, ms: scoredMs, bootMs: world?.bootMs, checks, calls: model.trace, observedAnswerMs, evidence, hardFailure };
}
