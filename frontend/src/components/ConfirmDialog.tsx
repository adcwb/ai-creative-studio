import { useI18n } from '../i18n'
import { useEffect, useRef } from 'react'

export function ConfirmDialog({title, message, confirmLabel, onConfirm, onCancel}: {
  title: string; message: string; confirmLabel: string; onConfirm: () => void; onCancel: () => void
}) {
  const {t} = useI18n()
  const ref = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const dialog = ref.current
    const previous = document.activeElement as HTMLElement | null
    dialog?.showModal()
    return () => {dialog?.close(); previous?.focus()}
  }, [])
  return <dialog ref={ref} className="drama-confirm" aria-labelledby="confirmation-title" aria-describedby="confirmation-message" onCancel={event => {event.preventDefault(); onCancel()}} onKeyDown={event => {
    if (event.key !== 'Tab') return
    const buttons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')]
    const first = buttons[0]; const last = buttons.at(-1)
    if (event.shiftKey && document.activeElement === first) {event.preventDefault(); last?.focus()}
    else if (!event.shiftKey && document.activeElement === last) {event.preventDefault(); first?.focus()}
  }}>
    <span className="eyebrow">BEFORE YOU CONTINUE</span><h2 id="confirmation-title">{title}</h2>
    <p id="confirmation-message">{message}</p>
    <div className="drama-confirm-actions"><button type="button" onClick={onCancel} autoFocus>{t('继续编辑')}</button><button type="button" className="primary" onClick={onConfirm}>{confirmLabel}</button></div>
  </dialog>
}
