import {expect, test} from 'vitest'
import {creativeJobStatusView, taskStatusView, workflowRunStatusView, type WorkflowRunStatus} from './jobStatus'

test('image tasks never offer cancel, because there is no cancel route', () => {
  for (const status of ['queued', 'running', 'succeeded', 'failed'] as const) {
    const view = taskStatusView(status)
    expect(view.canCancel, status).toBe(false)
    expect(view.canVerify, status).toBe(false)
    expect(view.label).toMatch(/[㐀-鿿]/)
  }
  expect(taskStatusView('failed').canRetry).toBe(true)
  expect(taskStatusView('succeeded').canRetry).toBe(false)
})

test('creative jobs add cancelled but still cannot be cancelled from the client', () => {
  expect(creativeJobStatusView('cancelled').tone).toBe('stopped')
  expect(creativeJobStatusView('cancelled').canRetry).toBe(true)
  expect(creativeJobStatusView('running').canCancel).toBe(false)
})

test('only workflow runs can cancel, and only an unknown result can be reconciled', () => {
  const all: WorkflowRunStatus[] = ['queued', 'running', 'succeeded', 'failed', 'cancelled', 'waiting_review', 'unknown']
  for (const status of all) {
    const view = workflowRunStatusView(status)
    const cancelable = status === 'queued' || status === 'running' || status === 'waiting_review'
    expect(view.canCancel, status).toBe(cancelable)
    expect(view.canVerify, status).toBe(status === 'unknown')
  }
  expect(workflowRunStatusView('waiting_review').tone).toBe('review')
  expect(workflowRunStatusView('unknown').tone).toBe('unknown')
})