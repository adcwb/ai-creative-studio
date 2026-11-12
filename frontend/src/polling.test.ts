import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {ApiError} from './api'
import {startPolling} from './polling'

class Visibility extends EventTarget {
  hidden = false
  set(hidden: boolean) {this.hidden = hidden; this.dispatchEvent(new Event('visibilitychange'))}
}
describe('read polling lifecycle', () => {
  const cleanups: (() => void)[] = []
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => {cleanups.splice(0).forEach(stop => stop()); vi.useRealTimers()})
  it('waits for slow reads and stops after completion', async () => {
    let resolve!: (again: boolean) => void
    const read = vi.fn(() => new Promise<boolean>(done => {resolve = done}))
    cleanups.push(startPolling(read, {interval: 1500, visibility: new Visibility()}))
    await vi.advanceTimersByTimeAsync(10000)
    expect(read).toHaveBeenCalledTimes(1)
    resolve(false)
    await vi.advanceTimersByTimeAsync(10000)
    expect(read).toHaveBeenCalledTimes(1)
  })
  it('pauses hidden reads and resumes once with a fresh signal', async () => {
    const visibility = new Visibility()
    visibility.hidden = true
    const read = vi.fn(async () => {})
    cleanups.push(startPolling(read, {interval: 1500, visibility}))
    await vi.advanceTimersByTimeAsync(10000)
    expect(read).not.toHaveBeenCalled()
    visibility.set(false)
    await vi.advanceTimersByTimeAsync(0)
    expect(read).toHaveBeenCalledTimes(1)
    visibility.set(true)
    await vi.advanceTimersByTimeAsync(10000)
    expect(read).toHaveBeenCalledTimes(1)
    visibility.set(false)
    await vi.advanceTimersByTimeAsync(0)
    expect(read).toHaveBeenCalledTimes(2)
  })
  it('aborts in-flight reads on hide and disposes without rescheduling', async () => {
    const visibility = new Visibility()
    let signal!: AbortSignal
    const read = vi.fn((value: AbortSignal) => {signal = value; return new Promise<void>(resolve => value.addEventListener('abort', () => resolve()))})
    const stop = startPolling(read, {interval: 1500, visibility})
    visibility.set(true)
    expect(signal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(0)
    visibility.set(false)
    expect(read).toHaveBeenCalledTimes(2)
    stop()
    expect(signal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(10000)
    expect(read).toHaveBeenCalledTimes(2)
  })
  it('backs off transient failures and resets after success', async () => {
    const read = vi.fn().mockRejectedValueOnce(new Error('offline')).mockRejectedValueOnce(new ApiError('unavailable', 503)).mockResolvedValue(undefined)
    cleanups.push(startPolling(read, {interval: 1000, visibility: new Visibility()}))
    await vi.advanceTimersByTimeAsync(1999)
    expect(read).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(read).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(4000)
    expect(read).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(1000)
    expect(read).toHaveBeenCalledTimes(4)
  })
  it('stops permission failures, including after visibility changes', async () => {
    const visibility = new Visibility()
    const read = vi.fn().mockRejectedValue(new ApiError('forbidden', 403))
    cleanups.push(startPolling(read, {interval: 1000, visibility}))
    await vi.advanceTimersByTimeAsync(10000)
    visibility.set(true); visibility.set(false)
    expect(read).toHaveBeenCalledTimes(1)
  })
  it('cancels unfinished siblings of a failed batch and caps backoff', async () => {
    let signal!: AbortSignal
    const read = vi.fn(async (value: AbortSignal) => {signal = value; throw new Error('unavailable')})
    cleanups.push(startPolling(read, {interval: 1000, maxDelay: 4000, visibility: new Visibility()}))
    await vi.advanceTimersByTimeAsync(0)
    expect(signal.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(2000)
    expect(read).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(4000)
    expect(read).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(4000)
    expect(read).toHaveBeenCalledTimes(4)
  })
})
