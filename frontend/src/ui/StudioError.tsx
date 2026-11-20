import {Alert} from 'antd'
import type {ReactNode} from 'react'
import './atoms.css'

// One error surface. The category-based wording and next-step actions arrive with
// the generation-status work; this is the shared shell they will render into.
export function StudioError({message, code, details, action, className}: {
  message: string
  code?: string
  details?: ReactNode
  action?: ReactNode
  className?: string
}) {
  return <Alert
    className={`studio-error${className ? ` ${className}` : ''}`}
    type="error"
    role="alert"
    showIcon={false}
    title={message}
    description={(code || details || action) ? <div className="studio-error-detail">
      {details}
      {code && <code className="studio-error-code">{code}</code>}
      {action && <div className="studio-error-action">{action}</div>}
    </div> : undefined}
  />
}