import { useOutletContext } from 'react-router-dom'
import type { Capabilities, Space } from '../api'
import type { Session } from '../components/AuthGate'

export interface SessionContext {session: Session; logout: () => void}
export interface WorkspaceContext {
  session: Session
  space: Space | undefined
  capability: Capabilities | null
  capabilityError: string
  refreshCapability: () => void
  refreshSpaces: () => void
  setDirty: (dirty: boolean) => void
  setImageDirty: (dirty: boolean) => void
  requestLeave: (action: () => void) => void
}
export function useAppWorkspace() { return useOutletContext<WorkspaceContext>() }
