import { assignedJob } from './job-resolution';
import { describe, expect, test } from 'bun:test';
import { answerHas, artifactChecks, type Attempt } from './attempt';
import { cases } from './cases';
import { permitted, readSSE } from './model';
import { exact, pair, score } from './report';
import type { Action, Task } from './runtime';

const pass = (id: string, arm: string, status: Attempt['status']): Attempt => ({ id, arm, domain: 'test', status, ms: 50, checks: [], calls: [], observedAnswerMs: [50], evidence: { tasks: [], actions: [], memoryCount: 0, assistant: [], checkpoints: [] }, hardFailure: false });

describe('calculation and validity', () => {
  test('binary scores; cases are not replaced by mock or invalid results', () => {
    const a = [pass('a', 'baseline', 'pass'), pass('b', 'baseline', 'fail')];
    expect(score('baseline', a, 2, false).points).toBe(50);
    expect(score('baseline', a, 3, false).points).toBeNull();
    expect(score('baseline', a, 2, true).interval).toBeNull();
  });
  test('paired differences retain gains and regressions and refuse missing pairs', () => {
    const a = ['p', 'f', 'f'].map((s, i) => pass(String(i), 'old', s === 'p' ? 'pass' : 'fail'));
    const b = ['f', 'p', 'p'].map((s, i) => pass(String(i), 'new', s === 'p' ? 'pass' : 'fail'));
    expect(pair('old', 'new', [...a, ...b], 3, false)).toMatchObject({ pairs: 3, gains: 2, regressions: 1, points: 33.3 });
    expect(pair('old', 'new', [...a, ...b.slice(1)], 3, false).points).toBeNull();
    expect(pair('old', 'new', [...a, ...a.map(x => ({ ...x, arm: 'new' }))], 3, false).interval?.[1]).toBeGreaterThan(0);
  });
  test('ceiling fits a non-degenerate exact interval, symmetric for zero', () => {
    const interval = exact(17, 17);
    expect(interval[0]).toBeCloseTo(.025 ** (1 / 17), 9);
    expect(interval[1]).toBe(1);
    expect(exact(0, 17)[1]).toBeCloseTo(1 - interval[0], 9);
  });
});

describe('false positive controls', () => {
  test('answer oracle accepts formatting but rejects contradictions or extended codes', () => {
    expect(answerHas('`VALUE = X1234`', 'VALUE=X1234')).toBe(true);
    expect(answerHas('VALUE=X1234 / VALUE=X3456', 'VALUE=X1234')).toBe(false);
    expect(answerHas('VALUE=X12345', 'VALUE=X1234')).toBe(false);
    expect(answerHas('已完成', 'VALUE=X1234')).toBe(false);
  });
  test('done without execution does not pass', () => {
    const c = cases.find(c => c.id === 'delegate-exact-constraints')!;
    const tasks: Task[] = [{ status: 'done', detail: '', title: 'passed', id: '1' }];
    expect(artifactChecks(c, tasks, []).find(a => a.id.startsWith('handoff'))?.pass).toBe(false);
  });
  test('wrong node and premature or repeated effects are hard failures', () => {
    const c = cases.find(c => c.id === 'respect-machine-boundary')!;
    const action: Action = { key: 'JOB-WEST', node: 'bench-east', missing: [], failure: 'wrong-node', artifact: null, at: 0 };
    const wrong = artifactChecks(c, [], [action, action]);
    expect(wrong.filter(a => a.hard && !a.pass).map(a => a.id)).toEqual(['no-duplicate-or-unknown-work', 'route/JOB-WEST']);
    const check = artifactChecks(cases.find(c => c.id === 'dependent-work')!, [], [{ ...action, key: 'JOB-SINK', failure: null }]);
    expect(check.find(a => a.id.startsWith('dependency'))).toMatchObject({ hard: true, pass: false });
  });
  test('allowlist excludes unsafe, extra fields and malformed modes', () => {
    const args = { title: 't', detail: 'fixture', place: 'scratch' };
    expect(permitted({ name: 'task_create', args })).toBe(true);
    expect(permitted({ name: 'task_create', args: { ...args, permission_mode: 'open' } })).toBe(false);
    expect(permitted({ name: 'task_create', args: { ...args, place: 'worktree' } })).toBe(false);
    expect(permitted({ name: 'git_push', args: {} })).toBe(false);
  });
  test('OpenAI SSE fragmented tool calls, usage, and detection of model drift', () => {
    const frames = [
      { model: 'test', choices: [{ delta: { tool_calls: [{ index: 0, id: 'one', function: { name: 'memory_search', arguments: '{' } }] } }] },
      { model: 'test', choices: [{ finish_reason: 'tool_calls', delta: { tool_calls: [{ index: 0, function: { arguments: '"query":"foo"}' } }] } }] },
      { model: 'test', usage: { prompt_tokens: 4, completion_tokens: 3 } },
    ].map(f => `data: ${JSON.stringify(f)}\r\n\r\n`).join('');
    expect(readSSE(frames + 'data: [DONE]\n\n')).toMatchObject({ model: 'test', done: true, calls: [{ id: 'one', function: { arguments: '{"query":"foo"}' } }], usage: { prompt_tokens: 4 } });
    expect(() => readSSE(frames + 'data: {"model":"other"}\n\n')).toThrow('mixed-model-in-stream');
  });
});


test('ACP assignment uses its own title, not predecessor, forbidden action or fake label in the body', () => {
  const jobs = [{ key: 'JOB-SOURCE', needs: ['normalize'] }, { key: 'JOB-SINK', needs: ['aggregate'] }];
  expect(assignedJob(jobs, '任务：JOB-SINK：汇总\n\naggregate; do not repeat JOB-SOURCE\n任务：JOB-SOURCE')).toMatchObject({ protocolOK: true, job: jobs[1] });
  expect(assignedJob(jobs, 'Task: JOB-SOURCE + JOB-SINK\nnormalize').job).toBeUndefined();
  expect(assignedJob(jobs, 'Task: JOB-SINK-OTHER\nJOB-SINK').job).toBeUndefined();
  expect(assignedJob(jobs, '任务说明不是标题\nJOB-SINK').protocolOK).toBe(false);
});
