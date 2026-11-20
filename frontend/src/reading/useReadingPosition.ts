import {useCallback, useEffect, useRef} from 'react'
import {useSearchParams} from 'react-router-dom'

// Keeps the current scene in the URL so a reload, a back navigation or a shared
// link lands in the same place. Writes are throttled and use replace, so
// scrolling does not flood the history stack.
export function useReadingPosition(count: number, param = 'scene') {
  const [params, setParams] = useSearchParams()
  const raw = Number(params.get(param))
  const index = Number.isFinite(raw) && raw > 0 ? Math.min(Math.floor(raw) - 1, Math.max(count - 1, 0)) : 0
  const pending = useRef<number | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const latest = useRef(index)
  // setParams changes identity on every URL write. Holding it in a ref keeps
  // publish stable, otherwise observers that depend on it rebuild and refire.
  const write = useRef(setParams)
  useEffect(() => {write.current = setParams}, [setParams])
  useEffect(() => {latest.current = index}, [index])

  const publish = useCallback((next: number) => {
    if (next === latest.current) return
    pending.current = next
    if (timer.current) return
    timer.current = setTimeout(() => {
      timer.current = null
      const value = pending.current
      pending.current = null
      if (value === null || value === latest.current) return
      write.current(previous => {
        const next = new URLSearchParams(previous)
        if (value <= 0) next.delete(param)
        else next.set(param, String(value + 1))
        return next
      }, {replace: true})
    }, 400)
  }, [param])

  useEffect(() => () => {if (timer.current) clearTimeout(timer.current)}, [])
  return {index, publish}
}