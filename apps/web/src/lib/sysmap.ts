import type { SysState } from './types'

/** A module's state in words, next to its mark (work/specs/system-map). */
export const STATE_TEXT: Record<SysState, string> = { ok: '正常', busy: '忙', idle: '空闲', degraded: '降级', down: '停止' }
