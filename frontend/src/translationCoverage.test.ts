import {expect, test} from 'vitest'
import {loadLocale, setActiveLocale, translate} from './i18n'
import {errorGuidance, type ErrorCategory} from './errorPresentation'
import {taskStatusView, creativeJobStatusView, workflowRunStatusView, type WorkflowRunStatus} from './jobStatus'

// scripts/i18n_check.cjs only inspects literal first arguments to t()/translate().
// These two modules translate values held in lookup maps, so their Chinese
// strings are invisible to that gate. This closes the hole deterministically.
const categories: ErrorCategory[] = ['internal', 'interrupted', 'upstream', 'storage', 'configuration', 'access', 'conflict', 'input']
const runStatuses: WorkflowRunStatus[] = ['queued', 'running', 'succeeded', 'failed', 'cancelled', 'waiting_review', 'unknown']

function hasChinese(value: string) { return /[㐀-鿿]/.test(value) }

test('every error guidance string has an English translation', async () => {
  await loadLocale('en'); setActiveLocale('en')
  for (const category of categories) {
    const guidance = errorGuidance(category)
    for (const value of [guidance.title, guidance.next]) {
      expect(hasChinese(value), `${category}: ${value}`).toBe(false)
      expect(value.length).toBeGreaterThan(0)
    }
  }
})

test('every job status label has an English translation', async () => {
  await loadLocale('en'); setActiveLocale('en')
  for (const status of runStatuses) {
    expect(hasChinese(workflowRunStatusView(status).label), status).toBe(false)
  }
  for (const status of ['queued', 'running', 'succeeded', 'failed'] as const) {
    expect(hasChinese(taskStatusView(status).label), status).toBe(false)
  }
  expect(hasChinese(creativeJobStatusView('cancelled').label)).toBe(false)
})

test('the runtime stage wording is translated too', async () => {
  await loadLocale('en'); setActiveLocale('en')
  for (const key of ['已接单', '生成中', '已完成', '阶段进度，非模型生成百分比。', '本次未返回结果，内容已保留。']) {
    expect(hasChinese(translate(key)), key).toBe(false)
  }
})
