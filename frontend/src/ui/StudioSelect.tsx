import {Select} from 'antd'
import {Children, isValidElement, type ReactNode} from 'react'

export interface StudioOption {value: string; label: string; disabled?: boolean}
export function StudioSelect({id, value, options, children, onChange, disabled, label, required, 'aria-label': ariaLabel, 'aria-describedby': describedBy, 'aria-invalid': invalid}: {
  id?: string; value?: string | number; options?: StudioOption[]; children?: ReactNode; onChange: (value: string) => void; disabled?: boolean; label?: string; required?: boolean; 'aria-label'?: string; 'aria-describedby'?: string; 'aria-invalid'?: boolean | 'true' | 'false'
}) {
  const choices = options ?? Children.toArray(children).filter(isValidElement<{value?: string | number; children?: ReactNode; disabled?: boolean}>).map(child => ({value:String(child.props.value ?? child.props.children ?? ''),label:Children.toArray(child.props.children).join(''),disabled:child.props.disabled}))
  return <Select id={id} value={String(value ?? '')} data-selected-value={String(value ?? '')} options={choices} optionRender={option => <span data-select-value={option.value}>{option.label}</span>} virtual={false} onChange={onChange} disabled={disabled} aria-label={ariaLabel ?? label} aria-describedby={describedBy} aria-invalid={invalid} aria-required={required} status={invalid ? 'error' : undefined} className="studio-select" popupMatchSelectWidth showSearch={{optionFilterProp:'label'}} />
}
