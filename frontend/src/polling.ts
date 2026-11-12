import {ApiError} from './api'

export interface VisibilitySource extends EventTarget { readonly hidden: boolean }
export interface PollingOptions {
  interval: number
  visibility: VisibilitySource
  maxDelay?: number
}

// Read-only scheduling. Return false to finish; throw to apply retry policy.
// Callers must check signal.aborted before accepting an asynchronous result.
export function startPolling(read: (signal: AbortSignal) => Promise<void | boolean>, {interval, visibility, maxDelay = 30000}: PollingOptions) {
  let disposed = false
  let finished = false
  let failures = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let controller: AbortController | undefined

  async function run() {
    if (disposed || finished || visibility.hidden || controller) return
    const current = new AbortController()
    controller = current
    let delay = interval
    try {
      const again = await read(current.signal)
      if (!current.signal.aborted) {failures = 0; finished = again === false}
    } catch (error) {
      if (!current.signal.aborted) {
        failures++
        delay = Math.min(maxDelay, interval * 2 ** Math.min(failures, 10))
        if (error instanceof ApiError && error.status >= 400 && error.status < 500 && ![408, 429].includes(error.status)) finished = true
      }
    } finally {
      const cancelled = current.signal.aborted
      // Promise.all may reject while another request in the same batch is still
      // running. Close that read before scheduling the next batch.
      current.abort()
      controller = undefined
      if (!disposed && !finished && !visibility.hidden) {
        timer = setTimeout(() => {void run()}, cancelled ? 0 : delay)
      }
    }
  }
  function changed() {
    clearTimeout(timer)
    if (visibility.hidden) controller?.abort()
    else void run()
  }
  visibility.addEventListener('visibilitychange', changed)
  void run()
  return () => {
    disposed = true
    clearTimeout(timer)
    controller?.abort()
    visibility.removeEventListener('visibilitychange', changed)
  }
}
