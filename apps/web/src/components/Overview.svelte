<script lang="ts">
  // The workbench's first screen: projects × machines. Each cell shows who
  // works there now (persona avatars with a status ring) and what the project
  // has on that machine (a checkout, the deploy target).
  import { go, ui, wb } from '../lib/app.svelte'
  import { avatarOf, legend, looks } from '../lib/status'
  import { columns, load, rows } from '../lib/workbench'
  import type { Node } from '../lib/types'
  import Avatar from './Avatar.svelte'
  import Icon from './Icon.svelte'

  const data = $derived(wb.data)
  const cols = $derived(data ? columns(data.nodes) : [])
  const table = $derived(data ? rows(data) : [])
  const nodeIcon = (n: Node) => (n.role === 'server' ? 'server' : 'laptop')
  const openMachines = () => (ui.settings = 'machines')
</script>

<div class="page">
  <header class="head">
    <h1>总览</h1>
    {#if data?.discovered.length}
      <button class="found" onclick={() => go({ view: 'projects', id: '' })}>
        <Icon name="branch" size={15} />发现 {data.discovered.length} 个仓库
      </button>
    {/if}
  </header>

  {#if wb.error && !data}<p class="err">{wb.error}</p>{/if}

  {#if data && data.nodes.length === 0}
    <div class="empty">
      <Icon name="laptop" size={40} />
      <p class="big">还没有机器</p>
      <p>配对一台电脑或服务器，sous 才能派人去干活。</p>
      <button class="btn" onclick={openMachines}>配对机器</button>
    </div>
  {:else if data}
    <!-- Desktop: the matrix. -->
    <div class="matrix" style:--cols={cols.length} role="table" aria-label="项目和机器">
      <div class="corner" role="columnheader"></div>
      {#each cols as n (n.id)}
        {@const l = load(data, n.id)}
        <button class="machine" class:off={!n.online} class:server={n.role === 'server'} role="columnheader" onclick={openMachines} title="{n.name}{n.online ? '' : '（离线）'}">
          <span class="mi"><Icon name={nodeIcon(n)} size={17} /><span class="online" class:on={n.online}></span></span>
          <span class="mname">{n.name}</span>
          <span class="slots" aria-label="工位 {l.busy}/{l.slots}">
            {#each Array(Math.max(l.slots, l.busy)) as _, i}<span class="slot" class:busy={i < l.busy}></span>{/each}
          </span>
        </button>
      {/each}

      {#each table as row (row.project.id)}
        <button class="pname" role="rowheader" onclick={() => go({ view: 'project', id: row.project.id })}>
          {#if row.look}<span class="tone-{looks[row.look].tone}" title={looks[row.look].text}><Icon name={looks[row.look].icon} size={13} /></span>{/if}
          <span class="pn">{row.project.name}</span>
          {#if row.waiting.length}<span class="wait" title="{row.waiting.length} 个任务在等"><Icon name="clock" size={12} />{row.waiting.length}</span>{/if}
        </button>
        {#each cols as n (n.id)}
          {@const c = row.cells[n.id]}
          <div class="cell" class:has={c.place || c.deploy || c.workers.length} class:off={!n.online} role="cell">
            {#each c.workers as w (w.run.id)}
              {@const a = avatarOf(w.persona, w.run.agent)}
              <button class="worker" onclick={() => w.task ? go({ view: 'task', id: w.task.id }) : go({ view: 'project', id: row.project.id })}
                title="{a.title} · {looks[w.look].text}{w.task ? ' · ' + w.task.title : ''}">
                <Avatar glyph={a.glyph} color={a.color} size={26} ring={looks[w.look].tone === 'neutral' ? '' : looks[w.look].tone} />
                <span class="wl tone-{looks[w.look].tone}"><Icon name={looks[w.look].icon} size={11} /></span>
              </button>
            {/each}
            {#if !c.workers.length}
              {#if c.deploy}<span class="mark" title="部署目标"><Icon name="rocket" size={15} /></span>{/if}
              {#if c.place && !c.deploy}<span class="mark" title={c.scan ? '工作空间里有这个仓库' : '登记的仓库位置'}><Icon name="branch" size={14} /></span>{/if}
              {#if !c.place && !c.deploy}<span class="none">·</span>{/if}
            {/if}
          </div>
        {/each}
      {/each}
    </div>

    {#if table.length === 0}
      <div class="noproj">
        <p>还没有项目。登记工作空间里已有的仓库，或者让 sous 新建一个。</p>
        <button class="btn small" onclick={() => go({ view: 'projects', id: '' })}>去项目页</button>
      </div>
    {/if}

    <!-- Phone: one card per project. -->
    <div class="cards">
      {#each table as row (row.project.id)}
        <button class="card" onclick={() => go({ view: 'project', id: row.project.id })}>
          <span class="ct">
            {#if row.look}<span class="tone-{looks[row.look].tone}"><Icon name={looks[row.look].icon} size={14} /></span>{/if}
            <strong>{row.project.name}</strong>
            {#if row.waiting.length}<span class="wait"><Icon name="clock" size={12} />{row.waiting.length}</span>{/if}
          </span>
          <span class="cm">
            {#each cols as n (n.id)}
              {@const c = row.cells[n.id]}
              {#if c.place || c.deploy || c.workers.length}
                <span class="chip" class:off={!n.online}>
                  <Icon name={c.deploy ? 'rocket' : nodeIcon(n)} size={13} />
                  <span class="cn">{n.name}</span>
                  {#each c.workers as w (w.run.id)}
                    {@const a = avatarOf(w.persona, w.run.agent)}
                    <Avatar glyph={a.glyph} color={a.color} size={20} ring={looks[w.look].tone === 'neutral' ? '' : looks[w.look].tone} />
                  {/each}
                </span>
              {/if}
            {/each}
          </span>
        </button>
      {/each}
    </div>

    <footer class="legend" aria-label="图例">
      {#each legend as l}<span><span class="tone-{looks[l].tone}"><Icon name={looks[l].icon} size={13} /></span>{looks[l].text}</span>{/each}
      <span><Icon name="rocket" size={13} />部署目标</span>
      <span><Icon name="branch" size={13} />有仓库</span>
    </footer>
  {/if}
</div>

<style>
  .page { padding: 18px 22px 28px; display: grid; gap: 16px; align-content: start; max-width: 1200px; }
  .head { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
  h1 { font-size: 20px; margin: 0; }
  .found {
    display: inline-flex; align-items: center; gap: 6px; border: 1px dashed var(--line); background: transparent;
    border-radius: 999px; padding: 3px 12px; font-size: 13px; color: var(--muted);
  }
  .found:hover { color: var(--text); border-color: var(--accent); }
  .matrix {
    display: grid; grid-template-columns: minmax(120px, 180px) repeat(var(--cols), minmax(110px, 1fr));
    gap: 6px; align-items: stretch; overflow-x: auto;
  }
  .corner { min-height: 1px; }
  .machine {
    display: grid; grid-template-columns: auto 1fr; grid-template-rows: auto auto; column-gap: 8px; row-gap: 4px; align-items: center;
    border: 1px solid var(--line); background: var(--panel); border-radius: 12px; padding: 8px 10px; text-align: left; min-width: 0;
  }
  .machine:hover { border-color: var(--accent); }
  .machine.server { border-style: dashed; }
  .machine.off { opacity: .55; }
  .mi { position: relative; grid-row: span 2; color: var(--muted); }
  .online { position: absolute; right: -3px; bottom: -2px; width: 8px; height: 8px; border-radius: 50%; background: var(--muted); box-shadow: 0 0 0 2px var(--panel); }
  .online.on { background: var(--ok); }
  .mname { font-weight: 600; font-size: 14px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .slots { display: flex; gap: 3px; }
  .slot { width: 14px; height: 4px; border-radius: 2px; background: var(--line); }
  .slot.busy { background: var(--info); }
  .pname {
    display: flex; align-items: center; gap: 6px; border: 0; background: transparent; text-align: left;
    padding: 6px 4px; border-radius: 10px; min-width: 0; font-weight: 600;
  }
  .pname:hover { background: var(--sunken); }
  .pn { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .wait { display: inline-flex; align-items: center; gap: 2px; font-size: 12px; color: var(--muted); font-weight: 400; }
  .cell {
    min-height: 48px; border-radius: 12px; border: 1px solid transparent; background: var(--sunken);
    display: flex; flex-wrap: wrap; align-items: center; gap: 6px; padding: 8px 10px;
  }
  .cell.has { background: var(--panel); border-color: var(--line); }
  .cell.off { opacity: .55; }
  .worker { position: relative; border: 0; background: none; padding: 0; border-radius: 50%; }
  .wl { position: absolute; right: -5px; bottom: -4px; background: var(--panel); border-radius: 50%; padding: 1px; display: grid; }
  .mark { color: var(--muted); display: grid; }
  .none { color: var(--line); }
  .cards { display: none; gap: 8px; }
  .card {
    display: grid; gap: 8px; text-align: left; border: 1px solid var(--line); background: var(--panel);
    border-radius: 14px; padding: 12px;
  }
  .ct { display: flex; align-items: center; gap: 6px; }
  .cm { display: flex; flex-wrap: wrap; gap: 6px; }
  .chip { display: inline-flex; align-items: center; gap: 5px; border: 1px solid var(--line); border-radius: 999px; padding: 2px 8px 2px 6px; font-size: 12px; color: var(--muted); }
  .chip.off { opacity: .55; }
  .cn { max-width: 90px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .legend { display: flex; flex-wrap: wrap; gap: 14px; font-size: 12px; color: var(--muted); }
  .legend span { display: inline-flex; align-items: center; gap: 4px; }
  .empty { display: grid; justify-items: center; gap: 6px; padding: 12vh 16px; color: var(--muted); text-align: center; }
  .empty .big { font-size: 18px; color: var(--text); font-weight: 600; margin: 6px 0 0; }
  .empty p { margin: 0; }
  .empty .btn { margin-top: 10px; }
  .err { color: var(--danger); }
  .noproj { display: grid; justify-items: start; gap: 8px; color: var(--muted); font-size: 14px; border: 1px dashed var(--line); border-radius: 14px; padding: 14px 16px; }
  .noproj p { margin: 0; }
  .btn.small { padding: 6px 14px; font-size: 14px; }
  @media (max-width: 760px) {
    .page { padding: 14px 16px 24px; }
    .matrix { display: none; }
    .cards { display: grid; }
  }
</style>
