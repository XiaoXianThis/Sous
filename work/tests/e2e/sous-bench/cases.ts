import { createHash } from 'node:crypto';

export type Call = { name: string; args: Record<string, unknown> };
export type Job = { key: string; needs: string[]; node?: string; fail?: string };
export type Step =
  | { op: 'say'; text: string; answer?: string; ask?: string; mock?: Call[]; noEffects?: boolean }
  | { op: 'seed'; text: string; kind: 'profile' | 'project' | 'note' }
  | { op: 'new' | 'compact' }
  | { op: 'terminal'; count: number }
  | { op: 'review' };
export type Case = {
  id: string; domain: string; seconds: number; steps: Step[]; jobs?: Job[];
  taskCount?: number; finalStates?: string[]; deps?: [string, string][]; independent?: boolean;
  memory?: { present: string[]; absent: string[] }; twoNodes?: boolean;
};

export const suiteId = 'supervisor-v2';
export const fixtureSeed = 'sous-v024-baseline-2026-11';
function value(id: string) {
  return 'X' + createHash('sha256').update(`${fixtureSeed}/${id}`).digest('hex').slice(0, 8).toUpperCase();
}
function create(key: string, detail: string, extra: Record<string, unknown> = {}): Call {
  return { name: 'task_create', args: { title: key, detail: `${key} ${detail}`, place: 'scratch', ...extra } };
}
function say(text: string, answer?: string, mock?: Call[]): Step {
  const dispatchesSyntheticJob = mock?.some(m => m.name === 'task_create' && /^JOB-/.test(String(m.args.title)));
  const titleContract = dispatchesSyntheticJob ? ' 每条任务的标题须含自己的 JOB 编号，说明可以提及其他任务。' : '';
  return { op: 'say', text: text + titleContract, answer, mock };
}
const save = (content: string, kind = 'project', id?: string): Call => ({ name: 'memory_save', args: { content, kind, ...(id ? { id } : {}) } });
const seed = (text: string): Step => ({ op: 'seed', kind: 'project', text });
const fresh: Step = { op: 'new' };
const result = 'STATUS=UNVERIFIED';
const stageJob = (key: string, needs: string[], node?: string, fail?: string): Job => ({ key, needs, node, fail });
const terminal = (count: number): Step => ({ op: 'terminal', count });
const reply = (v: string) => `VALUE=${v}`;
const persist = value('persist'), recall = value('recall'), old = value('old'), updated = value('updated');
const remove = value('remove'), scopeA = value('scope-a'), scopeB = value('scope-b'), past = value('past'), current = value('current');
const stamp = value('handoff'), restricted = value('restricted');
const fillers = Array.from({ length: 12 }, (_, i) => say(`记录整理练习第 ${i + 1} 段：${Array.from({ length: 8 }, (_, j) => `编号 ${i + 1}-${j + 1} 的假想盒子用于模拟历史资料，内容与任何任务无关。`).join('')} 只回 ACK，不创建任务或写入记忆。`, 'ACK'));

export const cases: Case[] = [
  { id: 'explain-only', domain: 'intent', seconds: 90, steps: [say('仅回答，不要执行或创建任务。一个库位原有 9 件，归还 5 件，借出 7 件。请用 COUNT=<当前件数> 回答。', 'COUNT=7')] },
  { id: 'plan-without-running', domain: 'intent', seconds: 90, taskCount: 1, finalStates: ['planned'], steps: [
    say('帮我把「策划样稿」建成一条待规划的任务，临时工作区即可。只登记，绝不要派发。任务说明包括核对清单、验收三项。', undefined, [create('策划样稿', '核对清单；验收三项', { dispatch: false })]),
  ] },
  { id: 'clarify-before-action', domain: 'intent', seconds: 150, jobs: [stageJob('JOB-CLARIFY', ['amber'])], steps: [
    { op: 'say', text: '等你确认写入位置后再派发 JOB-CLARIFY（临时工作区）。目的文件夹只能是 amber 或 cedar，我还没选，你不能代选。', ask: 'amber|cedar|文件夹|目录', noEffects: true },
    say('我选 amber。现在立刻派发原任务，把目标文件夹交代给执行端。', undefined, [create('JOB-CLARIFY', '目标文件夹 amber')]), terminal(1),
  ] },
  { id: 'delegate-exact-constraints', domain: 'delegation', seconds: 120, jobs: [stageJob('JOB-EXPORT', [stamp, 'UTF-8', 'report.csv'])], steps: [
    say(`现在派发 JOB-EXPORT 在临时目录生产 report.csv：必须 UTF-8 编码，批次标记 ${stamp}。把文件名、编码、标记都交代给执行端。`, undefined, [create('JOB-EXPORT', `report.csv UTF-8 ${stamp}`)]), terminal(1),
  ] },
  { id: 'split-independent-work', domain: 'delegation', seconds: 135, independent: true, jobs: [stageJob('JOB-CATALOG', ['inventory']), stageJob('JOB-OUTLINE', ['storyboard'])], steps: [
    say('现在用两条独立的临时任务执行：JOB-CATALOG 对 inventory 分类；JOB-OUTLINE 给 storyboard 列提纲。两个任务都应派发，不需要相互等待。', undefined, [create('JOB-CATALOG', 'inventory'), create('JOB-OUTLINE', 'storyboard')]), terminal(2),
  ] },
  { id: 'dependent-work', domain: 'planning', seconds: 150, jobs: [stageJob('JOB-SOURCE', ['normalize']), stageJob('JOB-SINK', ['aggregate'])], deps: [['JOB-SOURCE', 'JOB-SINK']], steps: [
    say('现在派发两个临时任务。JOB-SOURCE 先 normalize；JOB-SINK 再 aggregate。必须保证后者在前者成功完成前不运行。', undefined, [create('JOB-SOURCE', 'normalize'), create('JOB-SINK', 'aggregate', { depends_on: ['$last-task'] })]), terminal(2),
  ] },
  { id: 'respect-machine-boundary', domain: 'routing', seconds: 115, twoNodes: true, jobs: [stageJob('JOB-WEST', [restricted], 'bench-west')], steps: [
    say(`现在用临时目录执行 JOB-WEST，标记 ${restricted}。数据合规要求只准在 bench-west 运行，bench-east 是错误机器，即使更快也不要用。`, undefined, [create('JOB-WEST', restricted, { machine: 'bench-west' })]), terminal(1),
  ] },
  { id: 'replan-from-feedback', domain: 'replanning', seconds: 190, jobs: [stageJob('JOB-PRIMARY', ['primary'], undefined, 'primary-source-unavailable'), stageJob('JOB-BACKUP', ['backup'])], finalStates: ['failed', 'done'], steps: [
    say('立即派发临时任务 JOB-PRIMARY，用 primary 来源做初步导入。现在只发这个任务，等结果再决定下一步。', undefined, [create('JOB-PRIMARY', 'primary')]), terminal(1),
    say('请核对第一步任务的真实结果。如果 primary-source-unavailable，就立刻发临时任务 JOB-BACKUP，改用 backup；不能重发第一步。如果没有这个失败就别发备选。', undefined, [create('JOB-BACKUP', 'backup')]), terminal(2),
  ] },
  { id: 'human-review-gate', domain: 'verification', seconds: 155, jobs: [stageJob('JOB-REVIEW', ['draft', 'HELLO'])], steps: [
    say('现在派发临时任务 JOB-REVIEW：生成文本 draft，只要一行内容 HELLO，不需要任何现有资料。我亲自验收，机器做完必须等我评分，不能自动验收。', undefined, [create('JOB-REVIEW', 'draft HELLO', { review: 'user' })]), { op: 'review' }, terminal(1),
  ] },
  { id: 'verified-failure-report', domain: 'verification', seconds: 140, jobs: [stageJob('JOB-CHECK', ['validation'], undefined, 'fixture-validation-failed')], finalStates: ['failed'], steps: [
    say('现在派发 JOB-CHECK，在临时目录做 validation，等待实际结果。', undefined, [create('JOB-CHECK', 'validation')]), terminal(1),
    say(`根据已发生的任务核对 JOB-CHECK；若没有可用产物，输出 ${result}；实际完成才输出 STATUS=VERIFIED。只查，不重试。`, result),
  ] },
  { id: 'save-only-durable', domain: 'memory-write', seconds: 110, memory: { present: [persist], absent: ['temporary-velvet'] }, steps: [
    say(`请把长期约定写进记忆：银穗项目交付码是 ${persist}。temporary-velvet 只是在本次对话用的临时暗号，别长期记住；不建任务。`, undefined, [save(`银穗项目交付码是 ${persist}`)]),
  ] },
  { id: 'recall-in-new-thread', domain: 'memory-recall', seconds: 100, steps: [seed(`青榆项目的验收标记：${recall}`), fresh, say('用长期记忆告诉我青榆项目的验收标记，只输出 VALUE=<标记>。没有依据则 VALUE=UNKNOWN。不要建任务。', reply(recall))] },
  { id: 'absent-memory-control', domain: 'memory-recall', seconds: 100, steps: [fresh, say('用长期记忆告诉我青榆项目的验收标记，只输出 VALUE=<标记>。没有依据则 VALUE=UNKNOWN。不要建任务。', 'VALUE=UNKNOWN')] },
  { id: 'correct-old-memory', domain: 'memory-update', seconds: 145, memory: { present: [updated], absent: [old] }, steps: [
    seed(`白帆项目的打包码：${old}`),
    say(`更正长期记忆：白帆项目的打包码现为 ${updated}，请替换原记忆，不要保留旧码。`, undefined, [save(`白帆项目的打包码：${updated}`, 'project', '$memory')]), fresh,
    say('用长期记忆回答白帆项目现行打包码，只输出 VALUE=<打包码>；没有依据则 VALUE=UNKNOWN。', reply(updated)),
  ] },
  { id: 'forget-in-new-thread', domain: 'memory-update', seconds: 145, memory: { present: [], absent: [remove] }, steps: [
    seed(`竹灯项目的验收标记：${remove}`),
    say('请从长期记忆中删除竹灯项目验收标记，不要复述原值。', undefined, [{ name: 'memory_forget', args: { id: '$memory' } }]), fresh,
    say('只用长期记忆回答竹灯项目的验收标记。用 VALUE=<标记>；没有依据则 VALUE=UNKNOWN。', 'VALUE=UNKNOWN'),
  ] },
  { id: 'competing-project-memories', domain: 'memory-relevance', seconds: 125, steps: [
    seed(`松石甲项目的归档码：${scopeA}`), seed(`松石乙项目的归档码：${scopeB}`), fresh,
    say('用长期记忆给出松石甲项目的归档码。不要把松石乙项目算进来。只输出 VALUE=<归档码>。', reply(scopeA)),
  ] },
  { id: 'compacted-context-correction', domain: 'context', seconds: 260, steps: [
    say(`这个对话的星港活动识别码是 ${past}。仅本对话有效，不要写长期记忆；只回 ACK。`, 'ACK'),
    say(`更正：星港活动识别码改为 ${current}，此前值作废。仅本对话有效，不要写长期记忆；只回 ACK。`, 'ACK'),
    ...fillers, { op: 'compact' }, say('请用本对话里已更正的星港活动识别码回答，只输出 VALUE=<现行码>。', reply(current)),
  ] },
];
