import { createContext, useContext, useEffect, useId } from 'react'

export const NavigationTaskContext = createContext<((id: string, dirty: boolean) => void) | undefined>(undefined)

// Independently mounted tools must not clear each other's pending-write guard.
export function useNavigationTask(dirty: boolean) {
  const update = useContext(NavigationTaskContext)
  const id = useId()
  useEffect(() => {
    update?.(id, dirty)
    return () => update?.(id, false)
  }, [id, dirty, update])
}
