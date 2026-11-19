import type {ReactNode} from 'react'
import {useI18n} from '../i18n'
import './workbench.css'

// A bottom-sticky dock for the action that moves the project forward. It used to
// sit at the end of a page that can grow without bound, which put it out of
// reach on long scripts. Sticky rather than fixed, so it never covers another
// page and never needs a spacer element.
export function NextStepDock({hint, children}: {hint?: ReactNode; children: ReactNode}) {
  const {t} = useI18n()
  return <div className="studio-next-step" role="group" aria-label={t('下一步操作')}>
    {hint && <p className="studio-next-step-hint">{hint}</p>}
    <div className="studio-next-step-actions">{children}</div>
  </div>
}