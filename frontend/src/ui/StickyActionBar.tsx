import type {ReactNode} from 'react'
import './workbench.css'

// Sits directly below the shared shell topbar and stays reachable at any scroll
// position. The offset comes from --shell-topbar-height, which the shell
// redefines at its 760px breakpoint, so no second magic number is needed.
export function StickyActionBar({label, status, children}: {label: string; status?: ReactNode; children: ReactNode}) {
  return <div className="studio-sticky-bar" role="group" aria-label={label}>
    {status && <div className="studio-sticky-status">{status}</div>}
    <div className="studio-sticky-actions">{children}</div>
  </div>
}