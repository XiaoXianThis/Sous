import type { Call } from './cases';

export class BenchError extends Error {
  constructor(public code: string, public kind: 'invalid' | 'failed' = 'invalid') { super(code); }
}
export type Chat = { role: string; content?: unknown; tool_calls?: ChatCall[]; tool_call_id?: string };
export type ChatCall = { id: string; type: 'function'; function: { name: string; arguments: string } };
type Tool = { type: 'function'; function: { name: string; description: string; parameters: unknown } };
const string = { type: 'string' };
function tool(name: string, description: string, properties: Record<string, unknown>, required: string[] = []): Tool {
  return { type: 'function', function: { name, description, parameters: { type: 'object', properties, required } } };
}
// Identical wire-level user tools for the compared arms; physical task state is in each real Hub.
export const tools = [
  tool('task_create', '登记任务。dispatch 默认 true；仅登记而不执行时显式传 false。工作位置仅支持 scratch。detail 写清目标、限制和验收。使用依赖任务 ID 保证顺序。', {
    title: string, detail: string, place: { type: 'string', enum: ['scratch'] }, dispatch: { type: 'boolean' }, machine: string,
    review: { type: 'string', enum: ['none', 'user'] }, depends_on: { type: 'array', items: string },
  }, ['title', 'detail', 'place']),
  tool('task_list', '列出任务与当前状态，可按状态过滤。', { status: { type: 'array', items: string } }),
  tool('task_result', '读取指定任务的执行结果和证据，不要把排队或失败当成完成。', { task: string, part: { type: 'string', enum: ['summary'] } }, ['task', 'part']),
  tool('memory_save', '写入长期记忆。修改现有记忆时传原 id，避免产生旧值冲突。只写长期有效的约定。', { id: string, content: string, kind: { type: 'string', enum: ['profile', 'project', 'lesson', 'note'] } }, ['content', 'kind']),
  tool('memory_search', '按项目名或关键词检索长期记忆。', { query: string }, ['query']),
  tool('memory_forget', '按 id 删除一条过期或不正确的长期记忆。', { id: string }, ['id']),
];
export const toolNames = new Set(tools.map(t => t.function.name));
export const schema = (names?: Tool[]) => names === undefined ? tools : tools.filter(x => names.some(y => y.function.name === x.function.name));
export function permitted(call: Call): boolean {
  const a = call.args;
  if (!a || typeof a !== 'object' || Array.isArray(a)) return false;
  const t = tools.find(t => t.function.name === call.name);
  if (!t) return false;
  const def = t.function.parameters as { properties: Record<string, any>; required: string[] };
  if (Object.keys(a).some(k => !(k in def.properties)) || def.required.some(k => !(k in a))) return false;
  return Object.entries(a).every(([k, v]) => {
    const p = def.properties[k];
    return (p.type === 'array' ? Array.isArray(v) && v.every(x => typeof x === 'string') : typeof v === p.type) && (!p.enum || p.enum.includes(v));
  });
}
export function decodeCall(c: ChatCall): Call {
  try { return { name: c.function.name, args: JSON.parse(c.function.arguments) }; }
  catch { return { name: c.function.name, args: null as any }; }
}

export type Exchange = {
  role: string; seq: number; ms: number; status: number; firstDataMs?: number; model?: string;
  input?: number; output?: number; cached?: number; responseBytes?: number;
  fixtureMarkersInInput?: string[]; tools?: string[]; rejected?: string[]; error?: string;
};
export type ModelConfig = { endpoint: string; model: string; key: string; effort?: string; output: number; maxCalls: number; total: { calls: number; cap: number }; mock: boolean };
export type Completion = { text: string; calls: ChatCall[]; finish?: string };

export function readSSE(raw: string): { text: string; calls: ChatCall[]; finish?: string; model?: string; usage?: any; done: boolean } {
  let text = '', finish: string | undefined, model: string | undefined, usage: any, done = false;
  const calls = new Map<number, ChatCall>();
  for (const event of raw.split(/\r?\n\r?\n/)) {
    const data = event.split(/\r?\n/).filter(s => s.startsWith('data:')).map(s => s.slice(5).trimStart()).join('\n');
    if (!data) continue;
    if (data === '[DONE]') { done = true; break; }
    const v = JSON.parse(data);
    if (v.error) throw new BenchError('provider-stream-error', 'failed');
    if (typeof v.model === 'string') {
      if (model && model !== v.model) throw new BenchError('mixed-model-in-stream');
      model = v.model.slice(0, 120);
    }
    if (v.usage) usage = v.usage;
    for (const c of v.choices ?? []) {
      const d = c.delta ?? {};
      text += d.content ?? '';
      finish ||= c.finish_reason ?? undefined;
      for (const f of d.tool_calls ?? []) {
        const o = calls.get(f.index) ?? { id: '', type: 'function', function: { name: '', arguments: '' } };
        if (f.id) o.id = f.id;
        if (f.function?.name) o.function.name += f.function.name;
        if (f.function?.arguments) o.function.arguments += f.function.arguments;
        calls.set(f.index, o);
      }
    }
  }
  return { text, calls: [...calls.entries()].sort((a, b) => a[0] - b[0]).map(p => p[1]), finish, model, usage, done };
}
function sse(text: string, calls: ChatCall[], model: string) {
  const data = (v: object) => `data: ${JSON.stringify(v)}\n\n`;
  const tool_calls = calls.map((c, index) => ({ ...c, index }));
  return data({ model, choices: [{ delta: { content: text, tool_calls }, finish_reason: calls.length ? 'tool_calls' : 'stop' }] }) + 'data: [DONE]\n\n';
}

export class Model {
  trace: Exchange[] = [];
  readonly session = crypto.randomUUID();
  signal?: AbortSignal;
  markers: string[] = [];
  mockReply = (_messages: Chat[], _role: string): Completion => ({ text: 'ACK', calls: [] });
  constructor(readonly cfg: ModelConfig) {}

  async request(messages: Chat[], selected: Tool[], role: string, parent?: AbortSignal) {
    if (this.trace.length >= this.cfg.maxCalls) throw new BenchError('case-call-budget', 'failed');
    if (this.cfg.total.calls >= this.cfg.total.cap) throw new BenchError('run-call-budget');
    this.cfg.total.calls++;
    const row: Exchange = { seq: this.trace.length + 1, role, ms: 0, status: 0 };
    if (this.markers.length) {
      const input = JSON.stringify(messages);
      row.fixtureMarkersInInput = this.markers.filter(marker => input.includes(marker));
    }
    this.trace.push(row);
    const start = performance.now();
    const signals = [AbortSignal.timeout(65000), this.signal, parent].filter((x): x is AbortSignal => !!x);
    const signal = AbortSignal.any(signals);
    try {
      let raw: string;
      if (this.cfg.mock) {
        const reply = this.mockReply(messages, role);
        raw = sse(reply.text, reply.calls, this.cfg.model);
        row.status = 200;
      } else {
        // Only the gateway can access the explicitly supplied model endpoint and credential.
        const r = await fetch(this.cfg.endpoint, {
          method: 'POST', redirect: 'error', signal,
          headers: { authorization: `Bearer ${this.cfg.key}`, 'content-type': 'application/json', accept: 'text/event-stream', 'x-opencode-session': `bench-${this.session}`, 'user-agent': 'sous-hub/1.0' },
          body: JSON.stringify({ model: this.cfg.model, messages, ...(selected.length ? { tools: selected } : {}), stream: true, stream_options: { include_usage: true }, max_tokens: this.cfg.output, ...(this.cfg.effort ? { reasoning_effort: this.cfg.effort } : {}) }),
        });
        row.status = r.status;
        if (!r.ok || !r.body) { await r.body?.cancel(); throw new BenchError(`provider-http-${r.status}`, 'failed'); }
        const decoder = new TextDecoder();
        const reader = r.body.getReader();
        raw = '';
        try {
          for (;;) {
            const { done, value } = await reader.read();
            if (done) break;
            row.firstDataMs ??= Math.round(performance.now() - start);
            raw += decoder.decode(value, { stream: true });
            if (raw.length > 8 * 1024 * 1024) throw new BenchError('provider-stream-too-large', 'failed');
          }
          raw += decoder.decode();
        } finally { await reader.cancel().catch(() => {}); }
      }
      row.responseBytes = Buffer.byteLength(raw);
      const parsed = readSSE(raw);
      if (!parsed.done && !parsed.finish) throw new BenchError('provider-incomplete-stream', 'failed');
      row.model = parsed.model;
      row.input = parsed.usage?.prompt_tokens; row.output = parsed.usage?.completion_tokens;
      row.cached = parsed.usage?.prompt_tokens_details?.cached_tokens ?? parsed.usage?.prompt_cache_hit_tokens;
      row.tools = parsed.calls.map(c => (toolNames.has(c.function.name) ? c.function.name : 'outside-contract'));
      const permittedTools = new Set(selected.map(t => t.function.name));
      const rejected = parsed.calls.filter(c => !permittedTools.has(c.function.name) || !permitted(decodeCall(c)));
      if (rejected.length) {
        row.rejected = rejected.map(c => permittedTools.has(c.function.name) ? 'invalid-args' : 'outside-contract');
        for (const c of rejected) c.function = { name: 'bench_invalid_call', arguments: '{}' };
        raw = sse(parsed.text, parsed.calls, parsed.model ?? this.cfg.model);
      }
      return { raw, parsed };
    } catch (e) {
      row.error = e instanceof BenchError ? e.code : signal.aborted ? 'provider-timeout-or-cancel' : 'provider-transport-or-format';
      throw e instanceof BenchError ? e : new BenchError(row.error, 'failed');
    } finally { row.ms = Math.round(performance.now() - start); }
  }

  serve() {
    return Bun.serve({ hostname: '127.0.0.1', port: 0, idleTimeout: 180, fetch: async r => {
      const role = new URL(r.url).pathname;
      if (r.method !== 'POST' || !['/primary/chat/completions', '/compact/chat/completions'].includes(role)) return new Response(null, { status: 404 });
      try {
        const body = await r.json() as { messages: Chat[]; tools?: Tool[]; model: string };
        if (!Array.isArray(body.messages) || body.model !== this.cfg.model) throw new BenchError('hub-provider-config-mismatch');
        const result = await this.request(body.messages, schema(body.tools ?? []), role.startsWith('/compact') ? 'compactor' : 'supervisor', r.signal);
        return new Response(result.raw, { headers: { 'content-type': 'text/event-stream' } });
      } catch (e) {
        return Response.json({ error: e instanceof BenchError ? e.code : 'gateway-error' }, { status: 422 });
      }
    } });
  }
}
