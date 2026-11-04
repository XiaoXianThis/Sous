<script lang="ts">
  import { api, ApiError } from '../lib/api'
  import type { ConfigFile } from '../lib/types'
  import Sheet from './Sheet.svelte'

  let { nodeId, agent, file, title, onClose }: { nodeId: string; agent: string; file: string; title: string; onClose: () => void } = $props()

  let loaded = $state<ConfigFile | null>(null)
  let text = $state('')
  let error = $state('')
  let notice = $state('')
  let conflict = $state(false)
  let saving = $state(false)

  const dirty = $derived(loaded !== null && text !== loaded.content)

  async function load() {
    try {
      loaded = await api.readFile(nodeId, agent, file)
      text = loaded.content
      error = ''
      conflict = false
    } catch (e) {
      error = (e as Error).message
    }
  }
  load()

  async function reload() {
    if (dirty && !confirm('放弃你的修改，重新读取文件？')) return
    notice = ''
    await load()
  }

  async function save() {
    if (!loaded) return
    if (loaded.format === 'json' && text.trim()) {
      try {
        JSON.parse(text)
      } catch (e) {
        error = `JSON 格式不对：${(e as Error).message}`
        return
      }
    }
    saving = true
    try {
      const out = await api.writeFile(nodeId, agent, file, text, loaded.sha256)
      loaded = out
      text = out.content
      error = ''
      notice = out.backup ? `已保存。原来的内容备份在 ${out.backup}` : '已保存。'
    } catch (e) {
      conflict = e instanceof ApiError && e.code === 'conflict'
      error = (e as Error).message
    } finally {
      saving = false
    }
  }

  function close() {
    if (dirty && !confirm('还有没保存的修改，确定关闭？')) return
    onClose()
  }

  const formatName: Record<string, string> = { json: 'JSON', jsonc: 'JSON（可以写注释）', toml: 'TOML', yaml: 'YAML', text: '文本' }
</script>

<Sheet {title} onClose={close} wide>
  {#if loaded}
    <p class="sub">
      <code>{loaded.path}</code> · {formatName[loaded.format] ?? loaded.format}{loaded.exists ? '' : ' · 文件还不存在，保存后创建'}
    </p>
    {#if agent === 'sous'}
      <p class="sub">设备令牌和 API Key 显示成 <code>***</code>，保存时保持原值；hub_url、node_id、remote_admin 只能在这台电脑上改。保存后立即生效，并发数要重启 sous 后生效。</p>
    {/if}
    <textarea bind:value={text} spellcheck="false" autocapitalize="off" autocomplete="off" aria-label="文件内容"
      onkeydown={(e) => (e.metaKey || e.ctrlKey) && e.key === 's' && (e.preventDefault(), save())}></textarea>
    <div class="row">
      <button class="btn small" disabled={!dirty || saving} onclick={save}>{saving ? '正在保存…' : '保存'}</button>
      <button class="link" onclick={reload}>重新读取</button>
      {#if notice}<span class="sub">{notice}</span>{/if}
    </div>
  {:else if !error}
    <p class="sub">正在读取…</p>
  {/if}
  {#if error}
    <p class="error" role="alert">{error}{#if conflict} <button class="link" onclick={reload}>重新读取</button>{/if}</p>
  {/if}
</Sheet>

<style>
  textarea {
    width: 100%; min-height: 50vh; resize: vertical; box-sizing: border-box;
    font: 13px/1.5 ui-monospace, Menlo, Consolas, monospace; tab-size: 2;
    border: 1px solid var(--line); border-radius: 10px; background: var(--sunken); color: var(--text); padding: 10px;
  }
  .row { display: flex; gap: 14px; align-items: center; flex-wrap: wrap; }
  .btn.small { padding: 7px 16px; font-size: 14px; }
  .sub { color: var(--muted); font-size: 13px; margin: 0; overflow-wrap: anywhere; }
  .sub code { font-size: 12px; }
  .link { border: 0; background: none; color: var(--muted); padding: 0; font-size: 13px; }
  .link:hover { color: var(--text); }
  .error { color: var(--danger); font-size: 13px; margin: 0; }
</style>
