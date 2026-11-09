// Workbench routes in the URL hash. Push notification links (#approvals,
// #task=<id>, #thread=<id>) are older forms of the same routes.

export type View = 'overview' | 'projects' | 'project' | 'task' | 'inbox'

export interface Route {
  view: View
  id: string
}

export const home: Route = { view: 'overview', id: '' }

/** Parses #/, #/projects, #/project=<id>, #/task=<id>, #/inbox and the push forms. */
export function parseRoute(hash: string): Route & { thread?: string; topic?: string } {
  const h = hash.replace(/^#\/?/, '')
  if (h === '' ) return home
  if (h === 'inbox' || h === 'approvals') return { view: 'inbox', id: '' }
  if (h === 'projects') return { view: 'projects', id: '' }
  const [k, ...rest] = h.split('=')
  const v = decodeURIComponent(rest.join('='))
  if (k === 'project' && v) return { view: 'project', id: v }
  if (k === 'task' && v) return { view: 'task', id: v }
  if (k === 'thread' && v) return { ...home, thread: v }
  if (k === 'topic' && v) return { ...home, topic: v }
  return home
}

export function routeHash(r: Route): string {
  switch (r.view) {
    case 'overview':
      return '#/'
    case 'projects':
    case 'inbox':
      return `#/${r.view}`
    default:
      return `#/${r.view}=${encodeURIComponent(r.id)}`
  }
}
