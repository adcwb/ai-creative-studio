import {Button, Modal} from 'antd'
import {createContext, lazy, Suspense, useCallback, useContext, useEffect, useId, useRef, useState, type ReactNode} from 'react'
import {useI18n} from '../i18n'
import {Icon} from '../components/Icon'
import {useMobile} from './useMobile'

const MobileSheet = lazy(() => import('./MobileSheet'))
const Layer = createContext(1000)

export function StudioDialog({title, children, onClose, busy = false, className = '', width = 760, closeLabel, initialFocus = 'input', returnFocusTo}: {
  title: string; children: ReactNode; onClose: () => void; busy?: boolean; className?: string; width?: number; closeLabel?: string; initialFocus?: 'input' | 'cancel'; returnFocusTo?: HTMLElement | null
}) {
  const {t} = useI18n()
  const viewportMobile = useMobile()
  // Keep the mounted editor and its draft when the viewport changes while open.
  const [mobile] = useState(viewportMobile)
  const layer = useContext(Layer) + 100
  const titleID = useId()
  const [anchor, setAnchor] = useState<HTMLSpanElement | null>(null)
  const body = useRef<HTMLDivElement>(null)
  const interacted = useRef(false)
  // An async opener may disable its own trigger while loading, which blurs it
  // before this mounts. Callers in that situation pass the trigger explicitly.
  const previousFocus = useRef(returnFocusTo ?? (document.activeElement as HTMLElement | null))
  useEffect(() => {
    const previous = previousFocus.current
    const {scrollX, scrollY} = window
    return () => {previous?.focus({preventScroll:true}); window.scrollTo({left:scrollX,top:scrollY,behavior:'instant'})}
  }, [])
  useEffect(() => {
    if (!mobile) return
    function closeAfterFocusLoss(event: KeyboardEvent) {
      if (event.key !== 'Escape' || event.defaultPrevented || event.target !== document.body) return
      const dialogs = [...document.querySelectorAll('.studio-mobile-sheet > [role="dialog"]')]
      const dialog = body.current?.closest('[role="dialog"]')
      if (!dialog || dialog !== dialogs.at(-1)) return
      event.preventDefault()
      if (!busy) onClose()
    }
    document.addEventListener('keydown', closeAfterFocusLoss)
    return () => document.removeEventListener('keydown', closeAfterFocusLoss)
  }, [mobile, busy, onClose])
  const focusContent = useCallback(() => {
    // An opening animation may finish after the user has already chosen a field.
    if (interacted.current) return
    const selector = initialFocus === 'cancel' ? '[data-dialog-cancel]' : 'input:not(:disabled), textarea:not(:disabled), [role="combobox"]:not([disabled]):not([aria-disabled="true"])'
    const control = body.current?.querySelector<HTMLElement>(selector) ?? body.current?.querySelector<HTMLElement>('button:not(:disabled)')
    control?.focus({preventScroll:true})
  }, [initialFocus])
  const content = <Layer.Provider value={layer}><div ref={body} className={`studio-dialog-content ${className}`} aria-busy={busy} onPointerDownCapture={() => {interacted.current = true}} onKeyDownCapture={() => {interacted.current = true}} onKeyDown={event => {
    if (event.key === 'Escape' && mobile) {event.stopPropagation(); event.preventDefault(); if (!busy) onClose()}
    if (event.key !== 'Tab') return
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), a[href], [tabindex="0"]')].filter(element => element.getClientRects().length && !element.closest('[aria-hidden="true"]'))
    const first = controls[0], last = controls.at(-1)
    if (event.shiftKey && document.activeElement === first) {event.preventDefault(); last?.focus()}
    else if (!event.shiftKey && document.activeElement === last) {event.preventDefault(); first?.focus()}
  }}><header className="studio-dialog-header"><h2 id={titleID}>{title}</h2><Button type="text" aria-label={closeLabel ?? t('关闭')} className="studio-dialog-close" disabled={busy} onClick={onClose} icon={<Icon name="close" />} /></header>{children}</div></Layer.Provider>
  const container = anchor?.closest<HTMLElement>('dialog, .user-workspace') ?? document.body
  return <><span ref={setAnchor} hidden />{anchor && (mobile ? <Suspense fallback={null}><MobileSheet container={container} zIndex={layer} onShow={focusContent}><section role="dialog" aria-modal="true" aria-labelledby={titleID}>{content}</section></MobileSheet></Suspense> : <Modal open title={title} getContainer={container} width={width} footer={null} closable={false} keyboard={!busy} mask={{closable:false}} onCancel={() => {if (!busy) onClose()}} zIndex={layer} className="studio-modal" classNames={{container:'studio-modal-container'}} styles={{header:{display:'none'},body:{padding:0}}} afterOpenChange={open => {if (open) focusContent()}}>{content}</Modal>)}</>
}
