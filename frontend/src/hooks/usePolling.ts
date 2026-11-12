import {useEffect, useEffectEvent} from 'react'
import {startPolling} from '../polling'

// key identifies the resource/revision. Changing it cancels the previous read.
// State used only to apply a result stays fresh without restarting the timer.
export function usePolling(read: (signal: AbortSignal) => Promise<void | boolean>, interval: number, key: string, active = true) {
  const poll = useEffectEvent(read)
  useEffect(() => {
    if (!active) return
    return startPolling(signal => poll(signal), {interval, visibility: document})
  }, [active, interval, key])
}
