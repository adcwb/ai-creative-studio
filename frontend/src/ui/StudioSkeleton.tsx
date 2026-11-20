import {Skeleton} from 'antd'
import './atoms.css'

// Loading placeholders that match the shape of the real content, so arrival does
// not shift the layout. Media keeps a fixed ratio for the same reason.
export function StudioSkeleton({shape = 'text', rows = 3, className}: {shape?: 'text' | 'card' | 'media'; rows?: number; className?: string}) {
  if (shape === 'media') return <div className={`studio-skeleton studio-skeleton-media${className ? ` ${className}` : ''}`} aria-hidden="true" />
  return <div className={`studio-skeleton${className ? ` ${className}` : ''}`} data-shape={shape}>
    <Skeleton active title={shape === 'card'} paragraph={{rows}} />
  </div>
}