'use strict';
const $ = (id) => document.getElementById(id);
const op = {added:'新增', modified:'修改', removed:'移除'};
let state = {};
let tab = 'run';
let chosen = '';
let preview = null;
let counter = 0;
let active = null;
let cancelling = false;
let nativeReady = false;
const buttons = [...document.querySelectorAll('[data-action]')];

function notify(text, error = false) {
  $('notice').textContent = text;
  $('notice').classList.toggle('error', error);
}
function selectTab(next) {
  tab = next;
  for (const t of ['run','review']) {
    $(`${t}Tab`).setAttribute('aria-selected', String(t === tab));
    $(`${t}Tab`).tabIndex = t === tab ? 0 : -1;
    $(`${t}Panel`).hidden = t !== tab;
  }
}
function dispatch(action, value = '') {
  if (active && action !== 'cancel') return;
  const id = ++counter;
  const payload = {id, action, value, offline:$('offline').checked};
  const native = window.webkit?.messageHandlers?.external;
  if (!native || typeof native.postMessage !== 'function') {
    notify('这个界面只能在 Sous 桌面应用内使用。', true);
    return;
  }
  if (action === 'cancel') { cancelling = true; notify('正在停止并清理 Guest…'); }
  else {
    active = {id, action};
    const notice = {'open-workspace':'正在打开本机工作区…', run:'等待本机确认…', recover:'正在对账…', apply:'等待本机确认…', preview:'正在读取审阅副本…'}[action];
    if (notice) notify(notice);
  }
  native.postMessage(`sous.sandbox:${JSON.stringify(payload)}`);
  controls();
}
function controls() {
  const busy = !!active;
  const running = busy && ['run','recover'].includes(active.action);
  const granted = state.source && state.expires_ms > Date.now();
  for (const button of buttons) {
    const name = button.dataset.action;
    button.disabled = !nativeReady || !state.available || busy || (['pick-source','recover'].includes(name) && !state.ready);
  }
  $('taskSelect').disabled = busy || !state.tasks_connected;
  $('cancel').hidden = !running;
  $('cancel').disabled = cancelling;
  $('runButton').disabled = !nativeReady || !state.ready || !granted || busy || !$('script').value.trim();
  $('clearSource').hidden = !granted;
  $('clearImage').hidden = !state.image;
  $('apply').disabled = busy || !$('log').hidden || !state.review?.can_apply || !preview || preview.binary || preview.truncated || preview.path !== chosen || state.applied?.[chosen] === true;
  $('logs').hidden = !state.review;
  $('reveal').hidden = !state.review;
  $('sourceLabel').textContent = granted ? state.source : '尚未选择文件或目录';
  $('sourceDetail').textContent = granted ? `一次授权 · ${Math.ceil((state.expires_ms-Date.now())/60000)} 分钟后失效` : '仅用于一次运行；选择后 10 分钟内有效。';
}
function renderState() {
  const task = state.task;
  const target = $('taskSelect');
  target.replaceChildren(new Option('独立本机运行', ''), ...(state.tasks || []).map(item => new Option(item.title, item.id)));
  target.value = task?.id || '';
  $('taskHint').textContent = !state.tasks_connected ? '未连接工作台。打开本机工作区，或在下方独立运行。'
    : !state.tasks?.length ? '没有等待本机授权的请求。'
    : '任务说明只供参考；不会作为命令执行。';
  $('taskDetail').hidden = !task?.id;
  $('taskDetail').textContent = task?.id ? (task.detail || '没有额外的任务说明。') : '';
  $('msb').textContent = state.msb || '未选择';
  $('firmware').textContent = state.firmware || '未选择';
  $('image').textContent = state.image || '未选择（可选）';
  const review = state.review;
  $('reviewSummary').textContent = review
    ? `已确认 Guest 销毁 · 退出码 ${review.exit_code} · 导入 ${review.imported_files} 份文件 · ${review.changes.length} 项变更`
    : '完成一次运行，或打开之前保存的审阅目录。';
  $('reviewLayout').hidden = !review;
  $('files').replaceChildren(...(review?.changes || []).map((change) => {
    const item = document.createElement('li');
    const button = document.createElement('button');
    button.type = 'button';
    button.setAttribute('aria-pressed', String(chosen === change.path));
    const badge = document.createElement('span');
    badge.className = 'operation';
    badge.textContent = state.applied?.[change.path] ? '已写回' : op[change.operation];
    button.append(badge, document.createTextNode(change.path));
    button.addEventListener('click', () => {
      if (active) return;
      chosen = change.path;
      for (const entry of $('files').querySelectorAll('button')) entry.setAttribute('aria-pressed', String(entry === button));
      preview = null;
      displayPreview();
      dispatch('preview', chosen);
    });
    item.append(button);
    return item;
  }));
  controls();
}
function fillChanged(el, values, lo, hi) {
  const frag = document.createDocumentFragment();
  frag.append(document.createTextNode(values.slice(0,lo).join('\n') + (lo ? '\n' : '')));
  const lines = values.slice(lo,hi);
  if (lines.length) {
    const mark = document.createElement('span');
    mark.className = 'changed';
    mark.textContent = lines.join('\n');
    frag.append(mark);
  }
  frag.append(document.createTextNode(values.slice(hi).join('\n')));
  el.replaceChildren(frag);
}
function displayPreview() {
  $('log').hidden = true;
  $('path').textContent = chosen || '选择变更文件';
  $('diff').hidden = !preview;
  $('previewWarning').textContent = preview?.binary ? '含二进制数据。请在访达打开结果目录，核对原始产物后从 CLI 写回。'
    : preview?.truncated ? '文本预览已截断。请在访达打开结果目录，核对完整产物后从 CLI 写回。' : '';
  if (preview) {
    const a = preview.before_text?.split('\n') || [];
    const b = preview.after_text?.split('\n') || [];
    let lo = 0, ei = a.length, ej = b.length;
    while (lo < Math.min(ei,ej) && a[lo] === b[lo]) lo++;
    while (ei > lo && ej > lo && a[ei-1] === b[ej-1]) {ei--;ej--;}
    fillChanged($('before'),a,lo,ei);
    fillChanged($('after'),b,lo,ej);
  }
  controls();
}
window.sousSandboxReply = (reply) => {
  if (!active || reply.id !== active.id) return;
  if (reply.progress) { if (!cancelling) notify(reply.progress); return; }
  const action = active.action;
  active = null;
  cancelling = false;
  if (reply.state) {
    if (reply.state.result !== state.result) {
      chosen = ''; preview = null; $('backup').hidden = true;
      displayPreview();
      if (reply.state.result) selectTab('review');
    }
    if (state.task?.id !== reply.state.task?.id) $('script').value = '';
    state = reply.state;
  }
  nativeReady = true;
  renderState();
  if (action === 'ready') $('setup').open = !state.ready;
  if (reply.error) { notify(reply.error, true); return; }
  notify(({run:'', recover:'已完成对账。', apply:'已写回选中的一项。', ready:state.available ? '' : '此构建没有沙盒执行模块。'})[action] || '');
  if (action === 'preview' && reply.data?.path === chosen) { preview = reply.data; displayPreview(); }
  if (action === 'logs' && typeof reply.data === 'string') {
    $('path').textContent = '运行日志';
    $('previewWarning').textContent = '';
    $('log').hidden = false;
    $('diff').hidden = true;
    $('log').textContent = reply.data || '（没有运行输出）';
    controls();
  }
  if (action === 'apply' && reply.data?.local_backup) {
    $('backup').textContent = `原件保留在：${reply.data.local_backup}`;
    $('backup').hidden = false;
  }
};
$('runTab').addEventListener('click', () => selectTab('run'));
$('reviewTab').addEventListener('click', () => selectTab('review'));
$('runTab').parentElement.addEventListener('keydown', (event) => {
  if (['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) {
    event.preventDefault();
    selectTab(event.key === 'Home' ? 'run' : event.key === 'End' ? 'review' : tab === 'run' ? 'review' : 'run');
    $(`${tab}Tab`).focus();
  }
});
for (const button of buttons) button.addEventListener('click', () => dispatch(button.dataset.action));
$('cancel').addEventListener('click', () => dispatch('cancel'));
$('apply').addEventListener('click', () => dispatch('apply', chosen));
$('script').addEventListener('input', controls);
$('taskSelect').addEventListener('change', (event) => dispatch('select-task', event.currentTarget.value));
$('runForm').addEventListener('submit', (event) => { event.preventDefault(); dispatch('run', $('script').value); });
setInterval(controls, 5000);
selectTab('run');
controls();
addEventListener('load', () => {
  // Wails queues ExecJS until its document-ready message arrives. This
  // private view has no Wails service bindings or runtime HTTP bridge.
  window.webkit?.messageHandlers?.external?.postMessage('wails:runtime:ready');
  dispatch('ready');
}, {once:true});
