// Words for the talk between the supervisor and a task's agents.

/** Who said it, in the chat's words; an agent is named by the caller. */
export function talkWho(who: string): string {
  switch (who) {
    case 'supervisor':
      return '主管'
    case 'user':
      return '你'
    case 'reviewer':
      return '验收'
    case 'sous':
      return 'sous'
  }
  return who
}
