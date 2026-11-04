<script lang="ts">
  import type { PersonaColor } from '../lib/types'

  let { glyph, color = 'gray', title = '', size = 26, ring = '' }: {
    glyph: string; color?: PersonaColor; title?: string; size?: number
    /** A status ring: accent, warning, success, danger or ''. */
    ring?: string
  } = $props()
</script>

<span class="av c-{color} {ring ? 'ring-' + ring : ''}" style:width="{size}px" style:height="{size}px" style:font-size="{Math.round(size * 0.46)}px" {title} aria-label={title || glyph}>{glyph}</span>

<style>
  .av {
    display: inline-grid; place-items: center; border-radius: 50%; font-weight: 600; flex: none; line-height: 1;
    background: var(--p-bg); color: var(--p-fg); user-select: none;
  }
  .ring-accent { box-shadow: 0 0 0 2px var(--panel), 0 0 0 3.5px var(--accent); }
  .ring-warning { box-shadow: 0 0 0 2px var(--panel), 0 0 0 3.5px var(--warm); }
  .ring-success { box-shadow: 0 0 0 2px var(--panel), 0 0 0 3.5px var(--ok); }
  .ring-danger { box-shadow: 0 0 0 2px var(--panel), 0 0 0 3.5px var(--danger); }
</style>
