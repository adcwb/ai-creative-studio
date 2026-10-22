import { afterEach, describe, expect, it, vi } from 'vitest'
import { promptError, request } from './api'
afterEach(() => vi.unstubAllGlobals())
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
    await expect(request('/tasks')).rejects.toThrow('503')
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
    expect(options.headers).toEqual({ 'Content-Type': 'application/json', 'Idempotency-Key': 'stable-attempt-key' })
    expect(options.signal?.aborted).toBe(false)
    controller.abort()
    expect(options.signal?.aborted).toBe(true)
  })
})
