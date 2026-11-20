import {Tag} from 'antd'
import type {ReactNode} from 'react'
import './atoms.css'

// Status and classification badge. Long labels wrap instead of overflowing the
// panel, which is why every call site goes through here rather than bare Tag.
export function StudioTag({children, tone = 'default', className}: {children: ReactNode; tone?: 'default' | 'success' | 'warning' | 'error'; className?: string}) {
  const color = tone === 'success' ? 'success' : tone === 'warning' ? 'warning' : tone === 'error' ? 'error' : 'default'
  return <Tag className={`studio-tag${className ? ` ${className}` : ''}`} color={color}>{children}</Tag>
}