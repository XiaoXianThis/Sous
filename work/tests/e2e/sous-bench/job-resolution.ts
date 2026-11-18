import type { Job } from './cases';

// The hub presents the assigned title in a task header. A reference to another
// job in the body (dependency, alternative or forbidden action) is not an assignment.
export function assignedJob(jobs: readonly Job[], prompt: string) {
  const title = prompt.match(/^(?:任务|Task)\s*[：:]\s*([^\r\n]*)/im)?.[1];
  if (title === undefined) return { protocolOK: false, job: undefined };
  const matching = jobs.filter(j => new RegExp(`(?<![A-Z0-9-])${j.key}(?![A-Z0-9-])`, 'i').test(title));
  return { protocolOK: true, job: matching.length === 1 ? matching[0] : undefined };
}
