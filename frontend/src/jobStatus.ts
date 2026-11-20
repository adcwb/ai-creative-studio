import {translate} from './i18n'

// Three different entities report three different status sets. Collapsing them
// into one pretend-universal enum is how a cancel button ends up on an image
// task that has no cancel endpoint. Each view below states what is actually
// possible for that entity.
export type TaskStatus = 'queued' | 'running' | 'succeeded' | 'failed'
export type CreativeJobStatus = TaskStatus | 'cancelled'
export type WorkflowRunStatus = CreativeJobStatus | 'waiting_review' | 'unknown'

export type StatusTone = 'idle' | 'active' | 'done' | 'failed' | 'stopped' | 'review' | 'unknown'

export interface StatusView {label: string; tone: StatusTone; canCancel: boolean; canRetry: boolean; canVerify: boolean}

const labels: Record<WorkflowRunStatus, string> = {
  queued: '等待执行', running: '正在执行', succeeded: '已完成', failed: '执行失败',
  cancelled: '已停止', waiting_review: '等待人工选择', unknown: '结果待核对',
}

const tones: Record<WorkflowRunStatus, StatusTone> = {
  queued: 'idle', running: 'active', succeeded: 'done', failed: 'failed',
  cancelled: 'stopped', waiting_review: 'review', unknown: 'unknown',
}

function view(status: WorkflowRunStatus, options: {cancel?: boolean; verify?: boolean} = {}): StatusView {
  return {
    label: translate(labels[status]),
    tone: tones[status],
    canCancel: Boolean(options.cancel) && (status === 'queued' || status === 'running' || status === 'waiting_review'),
    canRetry: status === 'failed' || status === 'cancelled',
    canVerify: Boolean(options.verify) && status === 'unknown',
  }
}

// Image tasks expose retry and resume, never cancel: there is no cancel route.
export function taskStatusView(status: TaskStatus): StatusView { return view(status) }
export function creativeJobStatusView(status: CreativeJobStatus): StatusView { return view(status) }
// Only workflow runs have cancel and reconcile endpoints.
export function workflowRunStatusView(status: WorkflowRunStatus): StatusView { return view(status, {cancel: true, verify: true}) }