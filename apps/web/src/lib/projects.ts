// Archiving: archived projects leave the workbench for the archive dialog.
import { api } from './api'
import { loadOverview } from './app'
import type { Project } from './types'

/** Archives a project or makes it active again, then reloads the overview. */
export async function setArchived(p: Project, archived: boolean): Promise<void> {
  try {
    await (archived ? api.archiveProject(p.id) : api.activateProject(p.id))
    await loadOverview()
  } catch (e) {
    window.alert((e as Error).message)
  }
}
