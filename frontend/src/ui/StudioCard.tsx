import type {ReactNode} from 'react'
import './atoms.css'

// The product's panel. It stays a business section rather than antd Card because
// sixty call sites depend on the .panel layout contract; the header is the part
// that used to be hand-rolled differently on every page.
export function StudioCard({children, title, extra, eyebrow, className, as: Tag = 'section', ariaLabel}: {
  children: ReactNode
  title?: string
  extra?: ReactNode
  eyebrow?: string
  className?: string
  as?: 'section' | 'article' | 'div'
  ariaLabel?: string
}) {
  return <Tag className={`panel studio-card${className ? ` ${className}` : ''}`} aria-label={ariaLabel}>
    {(title || extra) && <div className="studio-card-header">
      <div>{eyebrow && <span className="eyebrow">{eyebrow}</span>}{title && <h2>{title}</h2>}</div>
      {extra && <div className="studio-card-extra">{extra}</div>}
    </div>}
    {children}
  </Tag>
}