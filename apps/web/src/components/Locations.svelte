<script lang="ts">
  // Where a project lives: the development machines, the deploy target and
  // the remote repository. Machines open the machine settings.
  import { ui } from '../lib/app.svelte'
  import type { Location } from '../lib/workbench'
  import Icon from './Icon.svelte'

  let { locations }: { locations: Location[] } = $props()
</script>

<div class="places" aria-label="项目位置">
  {#each locations as l (l.kind + l.id)}
    {#if l.kind === 'remote'}
      {#if l.href}
        <a class="chip remote" href={l.href} target="_blank" rel="noreferrer" title={l.title}>
          <Icon name={l.icon} size={12} />{l.name}
        </a>
      {:else}
        <span class="chip remote" title={l.title}><Icon name={l.icon} size={12} />{l.name}</span>
      {/if}
    {:else}
      <button class="chip" class:off={l.online === false} class:deploy={l.kind === 'deploy'} title={l.title}
        onclick={() => (ui.settings = 'machines')}>
        <Icon name={l.icon} size={12} />{l.name}
      </button>
    {/if}
  {:else}
    <span class="none">不在任何机器上</span>
  {/each}
</div>

<style>
  .places { display: flex; flex-wrap: wrap; gap: 5px; align-items: center; }
  .chip {
    display: inline-flex; align-items: center; gap: 4px; border: 1px solid var(--line); background: var(--bg);
    border-radius: 999px; padding: 1px 8px 1px 6px; font-size: 12px; color: var(--muted); text-decoration: none;
    max-width: 100%; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .chip:hover { color: var(--text); border-color: var(--accent); }
  .chip.off { opacity: .5; }
  .chip.deploy { border-style: dashed; }
  .chip.remote { color: var(--accent); border-color: color-mix(in srgb, var(--accent) 35%, var(--line)); }
  .none { font-size: 12px; color: var(--muted); }
</style>
