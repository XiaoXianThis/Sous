/** What a `kind: trigger` message says, split for display. */
export interface TriggerView {
  kind: 'schedule' | 'followup' | 'other'
  /** One short line: the schedule's title, or the follow-up's first clause. */
  title: string
  /** The rest, for the expanded view (without the bracketed head). */
  body: string
}

const schedule = /^【定时任务「([^」]*)」(（[^）]*）)?】\n?/
const followup = /^【跟进】/
// The hub appends a note meant for the model; the user does not need it.
const modelNote = /\n*（这是(用户设定的定时任务| sous 的自动跟进)[\s\S]*）\s*$/

/**
 * Parses a trigger message: 【定时任务「标题」（手动触发）】\n提示词, or
 * 【跟进】正文. Anything else is shown as its first line.
 */
export function triggerView(content: string): TriggerView {
  const s = schedule.exec(content)
  if (s) {
    return { kind: 'schedule', title: s[1] + (s[2] ?? ''), body: content.slice(s[0].length).replace(modelNote, '').trim() }
  }
  if (followup.test(content)) {
    const body = content.replace(followup, '').replace(modelNote, '').trim()
    return { kind: 'followup', title: firstClause(body).replace(/（id [^）]*）/g, ''), body }
  }
  const [head, ...rest] = content.split('\n')
  return { kind: 'other', title: head.replace(/^【|】$/g, ''), body: rest.join('\n').trim() }
}

// The text up to the first sentence break outside 「」 and （）, so the chip reads as a headline.
function firstClause(text: string): string {
  const line = text.split('\n')[0]
  let depth = 0
  for (let i = 0; i < line.length; i++) {
    const c = line[i]
    if (c === '「' || c === '（') depth++
    else if ((c === '」' || c === '）') && depth > 0) depth--
    else if (depth === 0 && i > 0 && '，。：:；'.includes(c)) return line.slice(0, i).trim()
  }
  return line.trim()
}
