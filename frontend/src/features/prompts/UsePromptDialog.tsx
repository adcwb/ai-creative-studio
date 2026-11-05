import {useEffect, useRef, useState} from 'react'
import {ApiError, errorMessage, request, type TaskInput} from '../../api'
import {Icon} from '../../components/Icon'
import {useI18n} from '../../i18n'
import type {PromptResult, PromptVersion} from './types'
export function UsePromptDialog({space, item, version, onApply, onClose}: {space: string; item: PromptResult; version: PromptVersion; onApply: (value: TaskInput) => void; onClose: () => void}) {
  const {t} = useI18n()
  const dialog = useRef<HTMLDialogElement>(null)
  const [values, setValues] = useState<Record<string, string>>(() => Object.fromEntries((version.variables ?? []).map(v => [v.name, v.default ?? ''])))
  const [preview, setPreview] = useState<{prompt: string; defaults: TaskInput; fingerprint: string} | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const fingerprint = JSON.stringify(values)
  const active = useRef(true)
  useEffect(() => {const current = dialog.current; const previous = document.activeElement as HTMLElement | null; active.current = true; current?.showModal(); return () => {active.current = false; current?.close(); previous?.focus()}}, [])
  async function compile() {
    if (busy) return
    setBusy(true); setError('')
    try {const result = await request<{prompt: string; defaults: TaskInput}>(`/prompt-templates/${item.template.id}/preview?${new URLSearchParams({space_id: space})}`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({version_id: version.id, variables: values})}); if (active.current) setPreview({...result, fingerprint})} catch (reason) {if (active.current) setError(errorMessage(reason, t('提示词预览失败，请检查变量。')) + (reason instanceof ApiError && reason.field ? ` (${reason.field})` : ''))} finally {if (active.current) setBusy(false)}
  }
  return <dialog ref={dialog} className="detail-dialog prompt-editor" aria-labelledby="prompt-use-title" onCancel={event => {event.preventDefault(); if (!busy) onClose()}}><div className="dialog-header"><div><h2 id="prompt-use-title">{t('填写提示词变量')}</h2><p>{item.template.name} · {t('版本 {number}', {number: version.number})}</p></div><button className="icon-button" aria-label={t('关闭变量预览')} disabled={busy} onClick={onClose}><Icon name="close" /></button></div><p className="composer-help">{t('先预览完整描述，再填入创作区。此处不会调用模型。')}</p>{error && <p role="alert" className="error">{error}</p>}<form onSubmit={event => {event.preventDefault(); void compile()}}><fieldset className="prompt-fields" disabled={busy}><legend className="sr-only">{t('变量取值')}</legend>{(version.variables ?? []).map(v => <label key={v.name}>{v.label || v.name}{v.required && ` · ${t('必填')}`}{v.type === 'enum' ? <select aria-label={v.label || v.name} value={values[v.name] ?? ''} onChange={event => setValues(previous => ({...previous, [v.name]: event.target.value}))}><option value="">{t('请选择')}</option>{v.options?.map(option => <option key={option} value={option}>{option}</option>)}</select> : <input aria-label={v.label || v.name} value={values[v.name] ?? ''} maxLength={v.max_length} required={v.required} onChange={event => setValues(previous => ({...previous, [v.name]: event.target.value}))} />}</label>)}</fieldset><button type="submit" disabled={busy}>{t(busy ? '正在预览…' : '预览完整提示词')}</button></form>{preview && preview.fingerprint === fingerprint && <label className="prompt-preview-result">{t('展开后的完整描述')}<textarea aria-label={t('展开后的完整描述')} readOnly value={preview.prompt} rows={5} /></label>}<div className="dialog-actions"><button disabled={busy} onClick={onClose}>{t('取消')}</button><button className="primary" disabled={busy || !preview || preview.fingerprint !== fingerprint} onClick={() => {if (preview) onApply({prompt: preview.prompt, kind: preview.defaults.kind, ratio: preview.defaults.ratio, style: preview.defaults.style, prompt_source: {template_id: item.template.id, version_id: version.id, variables: values, edited: true}})}}>{t('填入创作区')}</button></div></dialog>
}
