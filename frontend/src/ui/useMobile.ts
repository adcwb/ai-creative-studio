import {useCallback, useSyncExternalStore} from 'react'

export function useViewportAtMost(width: number) {
  const query = `(max-width: ${width}px)`
  const subscribe = useCallback((callback: () => void) => {
    const media = window.matchMedia(query)
    media.addEventListener('change', callback)
    return () => media.removeEventListener('change', callback)
  }, [query])
  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches, () => false)
}

export function useMobile() {
  return useViewportAtMost(760)
}
