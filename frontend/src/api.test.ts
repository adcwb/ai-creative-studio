import { afterEach, describe, expect, it, vi } from 'vitest'
import { promptError, request, setCSRFToken } from './api'
afterEach(() => {vi.unstubAllGlobals(); setCSRFToken('')})
describe('input and API errors', () => {
  it('counts Unicode code points and trims whitespace', () => {
    expect(promptError('  海报设计  ')).toBeNull()
    expect(promptError('😀😀')).not.toBeNull()
    expect(promptError('x'.repeat(1001))).not.toBeNull()
    expect(promptError('😀'.repeat(1000))).toBeNull()
    expect(promptError('  😀😀😀  ')).toBeNull()
  })
  it('surfaces structured API failures', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({error: {message: '任务尚未完成', code: 'provider_changed'}}), {status: 409})))
    await expect(request('/assets')).rejects.toMatchObject({ message: '任务尚未完成', status: 409, code: 'provider_changed' })
  })
  it('handles a non-JSON server failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('unavailable', {status: 503})))
    await expect(request('/tasks')).rejects.toMatchObject({status: 503})
  })
  it('preserves mutation body and idempotency key while combining caller cancellation', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 'child-task', retry_of: 'failed-parent' })))
    vi.stubGlobal('fetch', fetch)
    const controller = new AbortController()
    const body = JSON.stringify({ prompt: 'synthetic retry', kind: 'general', ratio: '1:1', style: 'natural' })
    const result = await request('/tasks/failed-parent/retry?space_id=personal-demo', {
      method: 'POST', signal: controller.signal,
      headers: { 'Content-Type': 'application/json', 'Idempotency-Key': 'stable-attempt-key' }, body,
    })
    expect(result).toEqual({ id: 'child-task', retry_of: 'failed-parent' })
    const [, options] = fetch.mock.calls[0] as [string, RequestInit]
    expect(options.body).toBe(body)
    expect(new Headers(options.headers).get('Idempotency-Key')).toBe('stable-attempt-key')
    expect(new Headers(options.headers).get('Content-Type')).toBe('application/json')
    expect(options.credentials).toBe('include')
    expect(options.signal?.aborted).toBe(false)
    controller.abort()
    expect(options.signal?.aborted).toBe(true)
  })
})

it('sends in-memory CSRF only with mutations and preserves caller headers', async () => {
  const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response('{}')))
  vi.stubGlobal('fetch', fetch); setCSRFToken('test-csrf')
  await request('/projects', {method: 'POST', headers: {'Idempotency-Key': 'test-key'}})
  await request('/projects')
  expect(new Headers(fetch.mock.calls[0][1].headers).get('X-CSRF-Token')).toBe('test-csrf')
  expect(new Headers(fetch.mock.calls[0][1].headers).get('Idempotency-Key')).toBe('test-key')
  expect(new Headers(fetch.mock.calls[1][1].headers).has('X-CSRF-Token')).toBe(false)
})
