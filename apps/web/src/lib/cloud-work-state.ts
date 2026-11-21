import type { CloudCapacityPhase, CloudWork } from './types'

const descriptions: Record<CloudWork['status'], string> = {
  queued: '已授权，等待可用的云工作机。',
  running: '云工作机已领取，正在准备或执行。',
  cancel_requested: '正在停止，等待云工作机确认回收。',
  cancelled: '已取消。没有发布部分结果。',
  succeeded: '已保存成果，Guest 已回收。任务不会自动标为完成。',
  failed: '命令未成功。可查看已保留的成果。',
  expired: '等待期间授权已过期。请重新确认。',
  stale: '任务状态或内容已经改变。请重新确认。',
  interrupted: '执行状态未能确认。系统不会自动重跑。',
}

const queuedDescriptions: Partial<Record<CloudCapacityPhase, string>> = {
  starting: '已授权，云工作机正在启动并检查运行环境。',
  online: '已授权，等待云工作机领取任务。',
  retiring: '已授权，上一台云工作机正在回收，任务会留在队列中。',
  needs_reconciliation: '已授权，云工作机暂不可用，等待恢复或运维对账。',
}

export function cloudWorkDescription(status: CloudWork['status'], phase?: CloudCapacityPhase) {
  return status === 'queued' && phase ? (queuedDescriptions[phase] ?? descriptions[status]) : descriptions[status]
}
