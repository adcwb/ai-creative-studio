import {afterEach, describe, expect, it, vi} from 'vitest'
import {ReadCache} from './runtimeCatalogCache'

afterEach(() => vi.useRealTimers())
describe('runtime catalog reads', () => {
  it('shares in-flight work and isolates account, space and feature keys', async () => {
    const cache = new ReadCache<number>()
    const load = vi.fn(async () => 1)
    const key = JSON.stringify(['account-a', 'space-a', 'image'])
    const first = cache.read(key, load)
    expect(cache.read(key, load)).toBe(first)
    await first
    await cache.read(key, load)
    await cache.read(JSON.stringify(['account-b', 'space-a', 'image']), load)
    await cache.read(JSON.stringify(['account-a', 'space-b', 'image']), load)
    await cache.read(JSON.stringify(['account-a', 'space-a', 'asr']), load)
    expect(load).toHaveBeenCalledTimes(4)
  })
  it('expires successful reads and does not cache failures', async () => {
    vi.useFakeTimers()
    const cache = new ReadCache<number>(1000)
    const load = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue(2)
    await expect(cache.read('key', load)).rejects.toThrow('offline')
    await expect(cache.read('key', load)).resolves.toBe(2)
    await vi.advanceTimersByTimeAsync(1000)
    await cache.read('key', load)
    expect(load).toHaveBeenCalledTimes(3)
  })
  it('invalidates outstanding reads and notifies all mounted consumers', async () => {
    const cache = new ReadCache<number>()
    let signal!: AbortSignal
    let resolve!: (value: number) => void
    const old = cache.read('key', value => {signal = value; return new Promise<number>(done => {resolve = done})})
    await Promise.resolve()
    const listener = vi.fn()
    const unsubscribe = cache.subscribe(listener)
    cache.clear()
    expect(signal.aborted).toBe(true)
    expect(listener).toHaveBeenCalledTimes(1)
    const fresh = cache.read('key', async () => 2)
    resolve(1)
    await old
    await expect(fresh).resolves.toBe(2)
    expect(cache.read('key', async () => 3)).toBe(fresh)
    unsubscribe(); cache.clear()
    expect(listener).toHaveBeenCalledTimes(1)
  })
})
