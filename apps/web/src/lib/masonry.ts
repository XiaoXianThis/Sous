// Masonry for the project card grid: cards are stacked in columns, each new
// card goes to the currently shortest one. Heights are estimated from the
// card's contents, which is enough for balance and never overlaps.
import type { ProjectCard } from './workbench'

/** How many columns fit in the given width. */
export function columnCount(width: number, min = 330, gap = 14): number {
  if (!width) return 1
  return Math.max(1, Math.min(4, Math.floor((width + gap) / (min + gap))))
}

/** Distributes items into cols stacks by the shortest estimated height. */
export function distribute<T>(items: T[], cols: number, heightOf: (item: T) => number): T[][] {
  const n = Math.max(1, cols)
  const out: T[][] = Array.from({ length: n }, () => [])
  const heights = new Array(n).fill(0)
  for (const item of items) {
    let pick = 0
    for (let i = 1; i < n; i++) if (heights[i] < heights[pick]) pick = i
    out[pick].push(item)
    heights[pick] += heightOf(item)
  }
  return out
}

/** A rough card height, used only to balance the columns. */
export function estimateHeight(c: ProjectCard): number {
  let h = 74 // header, locations, footer and gaps
  if (c.project.notes) h += 24
  h += c.agents.length * 96
  h += c.tasks.length * 48
  h += c.tasks.filter((t) => t.state === 'active' || t.state === 'attention').length * 9
  if (c.hiddenTasks + c.hiddenAgents > 0) h += 20
  if (c.counts.attention > 0) h += 8
  return h
}
