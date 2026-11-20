import {useId, type ReactNode} from 'react'
import './atoms.css'

// Label + control + hint + validation in one place, so pages stop inventing
// their own label markup and forget the describedby wiring.
export function StudioField({label, hint, error, required, children, className}: {
  label: string
  hint?: string
  error?: string
  required?: boolean
  children: (props: {id: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean; 'aria-required'?: boolean}) => ReactNode
  className?: string
}) {
  const id = useId()
  const hintId = hint ? `${id}-hint` : undefined
  const errorId = error ? `${id}-error` : undefined
  const describedBy = [hintId, errorId].filter(Boolean).join(' ') || undefined
  return <div className={`studio-field${className ? ` ${className}` : ''}`}>
    <label htmlFor={id}>{label}{required && <span className="studio-field-required" aria-hidden="true">*</span>}</label>
    {children({id, 'aria-describedby': describedBy, 'aria-invalid': error ? true : undefined, 'aria-required': required})}
    {hint && <p className="studio-field-hint" id={hintId}>{hint}</p>}
    {error && <p className="studio-field-error" id={errorId} role="alert">{error}</p>}
  </div>
}