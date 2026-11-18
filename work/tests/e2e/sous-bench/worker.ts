// Trusted fixture ACP program. No shell or arbitrary paths are accepted.
import { appendFileSync, realpathSync, writeFileSync } from 'node:fs';
import { resolve, sep } from 'node:path';
import { createInterface } from 'node:readline';
import type { Job } from './cases';
import { assignedJob } from './job-resolution';

const [config, node] = process.argv.slice(2);
const f: { jobs: Job[]; ledger: string; root: string } = await Bun.file(config).json();
let cwd = '';
const send = (v: Record<string, unknown>) => process.stdout.write(JSON.stringify({ jsonrpc: '2.0', ...v }) + '\n');
for await (const line of createInterface({ input: process.stdin })) {
  const m = JSON.parse(line);
  if (!m.method || m.id == null) continue;
  const done = (result = {}) => send({ id: m.id, result });
  const error = (message: string) => send({ id: m.id, error: { code: -32603, message } });
  if (m.method === 'initialize') done({ protocolVersion: 1, agentInfo: { name: 'bench-worker', version: '1' }, agentCapabilities: {} });
  else if (m.method === 'session/new') {
    cwd = realpathSync(m.params.cwd);
    if (!cwd.startsWith(realpathSync(f.root) + sep)) { error('fixture cwd escaped'); continue; }
    done({ sessionId: crypto.randomUUID() });
  } else if (m.method === 'session/prompt' && cwd) {
    const text = m.params.prompt.map((p: { text?: string }) => p.text ?? '').join('\n');
    const { protocolOK, job } = assignedJob(f.jobs, text);
    const missing = job?.needs.filter(s => !text.toLowerCase().includes(s.toLowerCase())) ?? [];
    const badRoute = !!(job?.node && job.node !== node);
    const failure = !protocolOK ? 'fixture-job-header-missing' : !job ? 'unrecognized-or-multiple-jobs' : badRoute ? 'wrong-node' : missing.length ? 'missing-constraint' : job.fail;
    const artifact = failure ? null : { key: job!.key, values: job!.needs, node };
    // Synthetic artifact, fixed filename and private sandbox. The ledger is the external oracle.
    if (artifact) writeFileSync(resolve(cwd, 'bench-result.json'), JSON.stringify(artifact), { flag: 'wx' });
    appendFileSync(f.ledger, `${JSON.stringify({ key: job?.key ?? null, node, missing, failure: failure ?? null, artifact, cwd, at: Date.now() })}\n`);
    if (failure) { error(failure); continue; }
    // An ACP tool-completion notification allows scratch work to count as actual work.
    send({ method: 'session/update', params: { sessionId: m.params.sessionId, update: { sessionUpdate: 'tool_call', toolCallId: 'fixture-write', kind: 'edit', title: 'Wrote bench-result.json', status: 'completed' } } });
    send({ method: 'session/update', params: { sessionId: m.params.sessionId, update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: `已写入 bench-result.json，工作项 ${job!.key}；验证字段：${job!.needs.join(', ')}。` } } } });
    done({ stopReason: 'end_turn' });
  } else error('unsupported fixture ACP method');
}
