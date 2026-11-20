import {Button} from 'antd'
import type {ReactNode} from 'react'

// The only button entry point for pages. Variants map onto antd types so the
// appearance stays in theme tokens; the extra class is for business layout only.
export function StudioButton({children, variant = 'default', size, block, loading, disabled, htmlType = 'button', icon, className, onClick, ariaLabel, title}: {
  children?: ReactNode
  variant?: 'primary' | 'default' | 'danger' | 'text'
  size?: 'small' | 'middle' | 'large'
  block?: boolean
  loading?: boolean
  disabled?: boolean
  htmlType?: 'button' | 'submit'
  icon?: ReactNode
  className?: string
  onClick?: () => void
  ariaLabel?: string
  title?: string
}) {
  return <Button
    className={`studio-button${className ? ` ${className}` : ''}`}
    type={variant === 'danger' ? 'primary' : variant === 'text' ? 'text' : variant === 'primary' ? 'primary' : 'default'}
    danger={variant === 'danger'}
    size={size}
    block={block}
    loading={loading}
    disabled={disabled}
    htmlType={htmlType}
    icon={icon}
    onClick={onClick}
    aria-label={ariaLabel}
    title={title}
  >{children}</Button>
}