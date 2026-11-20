import {Input} from 'antd'
import type {ReactNode} from 'react'
import './atoms.css'

interface CommonProps {
  id?: string
  value?: string
  onChange?: (value: string) => void
  placeholder?: string
  disabled?: boolean
  maxLength?: number
  className?: string
  prefix?: ReactNode
  'aria-label'?: string
  'aria-describedby'?: string
  'aria-invalid'?: boolean
  'aria-required'?: boolean
}

export function StudioInput({id, value, onChange, placeholder, disabled, maxLength, className, prefix, 'aria-label': ariaLabel, 'aria-describedby': describedBy, 'aria-invalid': invalid, 'aria-required': required}: CommonProps) {
  return <Input id={id} className={`studio-input${className ? ` ${className}` : ''}`} value={value} onChange={event => onChange?.(event.target.value)} placeholder={placeholder} disabled={disabled} maxLength={maxLength} prefix={prefix} aria-label={ariaLabel} aria-describedby={describedBy} aria-invalid={invalid} aria-required={required} status={invalid ? 'error' : undefined} />
}

// Reading-facing multiline input. Line height comes from the reading scale, not
// from a per-page override.
export function StudioTextArea({id, value, onChange, placeholder, disabled, maxLength, className, rows = 4, showCount, 'aria-label': ariaLabel, 'aria-describedby': describedBy, 'aria-invalid': invalid, 'aria-required': required}: CommonProps & {rows?: number; showCount?: boolean}) {
  return <Input.TextArea id={id} className={`studio-input studio-textarea${className ? ` ${className}` : ''}`} value={value} onChange={event => onChange?.(event.target.value)} placeholder={placeholder} disabled={disabled} maxLength={maxLength} rows={rows} showCount={showCount} aria-label={ariaLabel} aria-describedby={describedBy} aria-invalid={invalid} aria-required={required} status={invalid ? 'error' : undefined} />
}