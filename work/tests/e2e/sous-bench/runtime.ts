import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { BenchError, decodeCall, Model, tools, type Chat } from './model';
import type { Call, Case, Job, Step } from './cases';

export const home = resolve(import.meta.dir, '../../../..');
export const tmp = join(home, 'work/tmp');
export type Binary = { label: string; commit: string; hub: string; node: string; shaHub: string; shaNode: string };
export type Task = { id: string; title: string; detail: string; status: string; review?: string; depends_on?: string[]; next?: string };
export type Memory = { id: string; kind: string; content: string };
export type Action = { key: string | null; node: string; missing: string[]; failure: string | null; artifact: null | { key: string; values: string[]; node: string }; at: number; cwd?: string };
export type Fact = { text: string; observedMs: number; error?: boolean };
type Message = { role: string; content: string; kind?: string; reply_to?: number; id: string; error?: boolean };
export const isEnd = (t: Task) => ['done', 'failed', 'cancelled', 'blocked'].includes(t.status);
const cleanEnv = { PATH: process.platform === 'win32' ? process.env.PATH ?? '' : '/usr/bin:/bin', LANG: 'en_US.UTF-8', TZ: 'UTC', ...(process.platform === 'win32' ? { SystemRoot: process.env.SystemRoot } : {}) };

export async function poll<T>(signal: AbortSignal, f: () => Promise<T | undefined>, period = 200): Promise<T> {
  for (;;) {
    signal.throwIfAborted();
    const value = await f();
    if (value !== undefined) return value;
    await Bun.sleep(period);
  }
}

export class ApiError extends Error {
  constructor(public status: number, public code: string) { super(`HTTP ${status}: ${code}`); }
}
class Api {
  private session = '';
  private csrf = '';
  constructor(readonly url: string, public signal?: AbortSignal) {}
  async call<T = any>(path: string, method = 'GET', data?: unknown): Promise<T> {
    const response = await fetch(this.url + path, {
      method, signal: this.signal ? AbortSignal.any([this.signal, AbortSignal.timeout(6000)]) : AbortSignal.timeout(6000), redirect: 'error',
      headers: { 'content-type': 'application/json', ...(this.session ? { cookie: `sous_session=${this.session}; sous_csrf=${this.csrf}`, 'x-csrf-token': this.csrf } : {}) },
      ...(data === undefined ? {} : { body: JSON.stringify(data) }),
    });
    for (const cookie of response.headers.getSetCookie()) {
      const m = cookie.match(/^(sous_session|sous_csrf)=([^;]+);/);
      if (m?.[1] === 'sous_session') this.session = m[2]!;
      if (m?.[1] === 'sous_csrf') this.csrf = m[2]!;
    }
    const value = await response.json().catch(() => null) as any;
    if (!response.ok) throw new ApiError(response.status, /^[a-z_]+$/.test(value?.error?.code ?? '-') ? value.error.code : 'unknown');
    if (value === null) throw new BenchError('non-json-hub-response');
    return value;
  }
  get<T = any>(path: string) { return this.call<T>(`/api/v1${path}`); }
  post<T = any>(path: string, data: unknown = {}) { return this.call<T>(`/api/v1${path}`, 'POST', data); }
}

export class World {
  ledger: string;
  api!: Api;
  private children: Bun.Subprocess[] = [];
  private gateway?: ReturnType<Model['serve']>;
  private captures: Promise<unknown>[] = [];
  private log: string[] = [];
  bootMs = 0;
  private constructor(readonly dir: string, readonly arm: 'sous' | 'reference', readonly b: Binary, readonly model: Model) {
    this.ledger = join(dir, 'worker.ndjson');
  }
  static async make(arm: World['arm'], b: Binary, model: Model, c: Case) {
    const dir = await mkdtemp(join(tmp, 'sous-bench-'));
    const world = new World(dir, arm, b, model);
    try { await world.start(c); return world; }
    catch (e) { await world.close(); throw e; }
  }
  private keep(p: Bun.Subprocess) {
    this.children.push(p);
    const cap = async (stream: ReadableStream<Uint8Array> | null | number | undefined) => {
      if (!(stream instanceof ReadableStream)) return;
      const d = new TextDecoder();
      for await (const b of stream) {
        this.log.push(d.decode(b).slice(-4096));
        if (this.log.length > 16) this.log.shift();
      }
    };
    this.captures.push(cap(p.stderr).catch(() => {}), cap(p.stdout).catch(() => {}));
    return p;
  }
  diagnostics() {
    // Process logs stay in RAM. Return only stable categories, not process output or paths.
    const t = this.log.join(' ');
    return ['permission denied', 'address already in use', 'protocol', 'connection refused', 'not paired', 'no such file'].filter(x => t.toLowerCase().includes(x));
  }
  async start(c: Case) {
    const start = performance.now();
    const scope = AbortSignal.timeout(18000);
    await mkdir(join(this.dir, 'home'));
    await mkdir(join(this.dir, 'workspace'));
    const env = { ...cleanEnv, HOME: join(this.dir, 'home'), USERPROFILE: join(this.dir, 'home'), XDG_CONFIG_HOME: join(this.dir, 'home'), TMPDIR: this.dir, TEMP: this.dir, BENCH_LOOPBACK_KEY: 'benchmark-loopback-not-a-secret' };
    const socket = Bun.listen({ hostname: '127.0.0.1', port: 0, socket: { data() {} } });
    const port = socket.port;
    socket.stop();
    const url = `http://127.0.0.1:${port}`;
    this.api = new Api(url, scope);
    if (this.arm === 'sous') this.gateway = this.model.serve();
    const prov = [{ name: 'mock', kind: 'mock' }];
    if (this.gateway) for (const name of ['primary', 'compact']) prov.push({ name, kind: 'openai-chat', base_url: `${this.gateway.url.origin}/${name}`, api_key_env: 'BENCH_LOOPBACK_KEY' } as any);
    const profile = {
      listen: `127.0.0.1:${port}`, public_url: `http://localhost:${port}`, data_dir: join(this.dir, 'data'), dev: true,
      web: { disabled: true }, mcp: { disabled: true, stdio_disabled: true }, retention_days: -1, providers: prov,
      roles: { supervisor: this.gateway ? `primary/${this.model.cfg.model}` : 'mock/echo', compactor: this.gateway ? `compact/${this.model.cfg.model}` : 'mock/echo', reviewer: 'mock/verdict', approver: 'mock/verdict', inspector: 'mock/verdict' },
    };
    const cfg = join(this.dir, 'hub.yaml');
    await writeFile(cfg, JSON.stringify(profile), { mode: 0o600 });
    const p = this.keep(Bun.spawn([this.b.hub, '-config', cfg], { cwd: this.dir, env, stdout: 'pipe', stderr: 'pipe' }));
    const health = await poll(scope, async () => {
      if (p.exitCode !== null) throw new BenchError(`hub-exit-${p.exitCode}-${this.diagnostics().join(',')}`);
      try { return await this.api.call<{ version: string; status: string }>('/healthz'); } catch { return undefined; }
    });
    if (health.status !== 'ok' || health.version !== this.b.label) throw new BenchError('invalid-hub-build-label');
    await this.api.post('/auth/dev-login');
    const configured = await this.api.get<{ roles: Record<string, { model: string }> }>('/models');
    if (configured.roles.supervisor?.model !== (this.gateway ? this.model.cfg.model : 'echo')) throw new BenchError('invalid-hub-role');
    // Keep the measured context window fixed. Older binaries that cannot support it fail preflight.
    if (this.gateway) await this.api.call('/api/v1/models/roles', 'PUT', { supervisor: { provider_id: (configured.roles.supervisor as any).provider_id, model: this.model.cfg.model, context_window: 12000 } });
    if (c.jobs?.length) {
      const workerConfig = join(this.dir, 'worker-config.json');
      await writeFile(workerConfig, JSON.stringify({ jobs: c.jobs satisfies Job[], ledger: this.ledger, root: this.dir }), { mode: 0o600 });
      for (const node of c.twoNodes ? ['bench-east', 'bench-west'] : ['bench-east']) {
        const { code } = await this.api.post<{ code: string }>('/nodes/pairing-code');
        const nodeHome = join(this.dir, node);
        const proc = Bun.spawn([this.b.node, 'pair', url, code, '--name', node, '--home', nodeHome, '--workspace', join(this.dir, 'workspace')], { cwd: this.dir, env, stdout: 'ignore', stderr: 'ignore' });
        const status = await Promise.race([proc.exited, Bun.sleep(8000).then(() => -1)]);
        if (status !== 0) { if (status === -1) proc.kill(); throw new BenchError('node-pair-failed'); }
        const nodeFile = join(nodeHome, 'node.json');
        const old = await Bun.file(nodeFile).json();
        await writeFile(nodeFile, JSON.stringify({ ...old, default_agent: 'bench-worker', remote_admin: false, remote_shell: false, auto_fetch: false, sous_mcp: ['off'], concurrency: 2,
          custom_agents: [{ name: 'bench-worker', title: '确定性工作代理', command: [process.execPath, '--no-env-file', join(import.meta.dir, 'worker.ts'), workerConfig, node] }],
        }), { mode: 0o600 });
        this.keep(Bun.spawn([this.b.node, 'daemon', '--home', nodeHome], { cwd: this.dir, env, stdout: 'pipe', stderr: 'pipe' }));
        await poll(scope, async () => {
          const { nodes } = await this.api.get<{ nodes: any[] }>('/nodes');
          return nodes?.find(n => n.name === node && n.online && n.agents?.some((a: any) => a.name === 'bench-worker' || a.id === 'bench-worker'));
        });
      }
    }
    this.api.signal = undefined;
    this.bootMs = Math.round(performance.now() - start);
  }
  async close() {
    this.gateway?.stop(true);
    for (const p of [...this.children].reverse()) if (p.exitCode === null) p.kill();
    await Promise.race([Promise.allSettled(this.children.map(p => p.exited)), Bun.sleep(1800)]);
    for (const p of this.children) if (p.exitCode === null) p.kill('SIGKILL');
    await Promise.race([Promise.allSettled(this.children.map(p => p.exited).concat(this.captures as Promise<number>[])), Bun.sleep(1000)]);
    await rm(this.dir, { force: true, recursive: true, maxRetries: 2 });
  }
  async readActions(): Promise<Action[]> {
    const raw = await readFile(this.ledger, 'utf8').catch(e => { if (e.code === 'ENOENT') return ''; throw e; });
    return raw.trim().split('\n').filter(Boolean).map(x => JSON.parse(x));
  }
  createThread() { return this.api.post<{ id: string }>('/threads', { title: 'Benchmark challenge' }); }
  async tasks() { return (await this.api.get<{ tasks: Task[] }>('/tasks')).tasks ?? []; }
  async memories(query?: string) { return (await this.api.get<{ memories: Memory[] }>(`/memories${query ? `?q=${encodeURIComponent(query)}&limit=10` : ''}`)).memories ?? []; }
  async say(thread: string, text: string, topic: string | undefined, signal: AbortSignal) {
    const sent = await this.api.post<{ seq: number; topic_id?: string }>(`/threads/${thread}/messages`, { content: text, ...(topic ? { topic_id: topic } : {}) });
    if (!sent.seq) throw new BenchError('invalid-chat-contract');
    const start = performance.now();
    const m = await poll(signal, async () => {
      const { messages } = await this.api.get<{ messages: Message[] }>(`/threads/${thread}/messages?limit=100`);
      return (messages ?? []).find(m => m.role === 'assistant' && m.reply_to === sent.seq && (!m.kind || m.kind === 'silent'));
    });
    return { fact: { text: m.content, error: m.error, observedMs: Math.round(performance.now() - start) }, sent };
  }
}

function describe(t: Task) { return `${t.title} [${t.id}] ${t.status}`; }
export const referencePrompt = '你是独立任务主管。根据用户本轮目标行动，在授权和约束内用工具。区分计划、派发、以及真实完成；未知就澄清或明确说未知。发给 worker 的 detail 应完整传达必需的上下文。用工具回执而不是猜测来报告实际状态。只在用户授权时产生副作用。';
export class Reference {
  history: Chat[] = [];
  constructor(readonly world: World) {}
  reset() { this.history = [{ role: 'system', content: referencePrompt }]; }
  private async execute(call: Call, thread: string) {
    const api = this.world.api;
    const a = call.args;
    switch (call.name) {
      case 'task_create': {
        let node: string | undefined;
        if (a.machine) {
          const { nodes } = await api.get<{ nodes: { id: string; name: string }[] }>('/nodes');
          node = nodes?.find(n => n.id === a.machine || n.name === a.machine)?.id;
          if (!node) return '错误：没有这台机器';
        }
        const t = await api.post<Task>('/tasks', { title: a.title, detail: a.detail, place: a.place, dispatch: a.dispatch ?? true, node_pref: node, depends_on: a.depends_on, review: a.review, thread_id: thread });
        const planned = a.dispatch === false && t.status === 'inbox' ? await api.call<Task>(`/api/v1/tasks/${t.id}`, 'PATCH', { status: 'planned' }) : t;
        return `已创建：${describe(planned)}`;
      }
      case 'task_list': {
        const explicit = a.status as string[] | undefined;
        const all = await this.world.tasks();
        const selected = all.filter(t => explicit?.length ? explicit.includes(t.status) : !['done', 'cancelled'].includes(t.status));
        return selected.length ? selected.map(describe).join('\n') : '没有符合条件的任务';
      }
      case 'task_result': {
        const r = await api.get<{ task: Task; runs: { status: string; summary?: string; error?: string }[] }>(`/tasks/${encodeURIComponent(String(a.task))}`);
        const run = r.runs?.at(-1);
        return `任务：${describe(r.task)}；${run ? `执行 ${run.status} ${run.error ?? ''} ${run.summary ?? ''}` : '任务还没有执行过'}`;
      }
      case 'memory_save': {
        const path = a.id ? `/api/v1/memories/${encodeURIComponent(String(a.id))}` : '/api/v1/memories';
        const m = await api.call<Memory>(path, a.id ? 'PUT' : 'POST', { kind: a.kind, content: a.content });
        return `${a.id ? '已更新' : '已记住'} [${m.id}]`;
      }
      case 'memory_search': {
        const found = await this.world.memories(String(a.query));
        return found.length ? found.map(m => `[${m.id}] (${m.kind}) ${m.content}`).join('\n') : '没有找到相关记忆。';
      }
      case 'memory_forget':
        await api.call(`/api/v1/memories/${encodeURIComponent(String(a.id))}`, 'DELETE');
        return `已删除 [${a.id}]`;
      default: return '错误：无效或超出合同的工具调用。';
    }
  }
  async say(thread: string, step: Extract<Step, { op: 'say' }>, signal: AbortSignal): Promise<Fact> {
    const start = performance.now();
    this.history.push({ role: 'user', content: step.text });
    let rendered = '';
    for (;;) {
      signal.throwIfAborted();
      const { parsed: r } = await this.world.model.request(this.history, tools, 'supervisor', signal);
      rendered += r.text;
      this.history.push({ role: 'assistant', content: r.text || (r.calls.length ? null : ''), ...(r.calls.length ? { tool_calls: r.calls } : {}) });
      if (!r.calls.length) return { text: rendered, observedMs: Math.round(performance.now() - start) };
      for (const c of r.calls) {
        let output: string;
        try { output = await this.execute(decodeCall(c), thread); }
        catch (e) { if (!(e instanceof ApiError) || e.status >= 500) throw e; output = `错误：${e.code}`; }
        this.history.push({ role: 'tool', content: output, tool_call_id: c.id });
      }
    }
  }
}
