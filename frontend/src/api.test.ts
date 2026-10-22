import { afterEach, describe, expect, it, vi } from 'vitest'
import { promptError, request } from './api'
afterEach(() => vi.unstubAllGlobals())
describe('input and API errors', () => {
  it('counts Unicode code points and trims whitespace', () => {
    expect(promptError('  海报设计  ')).toBeNull()
    expect(promptError('😀😀')).not.toBeNull()
    expect(promptError('x'.repeat(1001))).not.toBeNull()
  })
  it('surfaces structured API failures', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({error: {message: '任务尚未完成'}}), {status: 409})))
    await expect(request('/assets')).rejects.toThrow('任务尚未完成')
  })
  it('handles a non-JSON server failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('unavailable', {status: 503})))
    await expect(request('/tasks')).rejects.toThrow('503')
  })
})
