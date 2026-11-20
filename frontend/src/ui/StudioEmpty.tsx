import type {ReactNode} from 'react'
import {Icon, type IconName} from '../components/Icon'
import './atoms.css'

// One empty state for the whole product: no data, no search results, or no
// permission. Pages used to spell this eight different ways.
export function StudioEmpty({title, description, action, icon = 'spark', kind = 'empty'}: {
  title: string
  description?: string
  action?: ReactNode
  icon?: IconName
  kind?: 'empty' | 'no-results' | 'no-access'
}) {
  return <div className="studio-empty" data-kind={kind}>
    <Icon name={icon} />
    <h3>{title}</h3>
    {description && <p>{description}</p>}
    {action && <div className="studio-empty-action">{action}</div>}
  </div>
}