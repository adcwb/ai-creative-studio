import {useEffect, useRef, useState} from 'react'
import {ApiError, errorMessage, request, type TaskInput} from '../../api'
import {useNavigationTask} from '../../app/navigationGuard'
import {Icon} from '../../components/Icon'
import {useI18n} from '../../i18n'
import {applyPendingKey, readApplyPending, type ApplyPending, type PromptApplication, type PromptResult, type PromptVersion} from './types'
export function UsePromptDialog({space, item, version, onApply, onClose, projectId, mediaVersion, actor = 'demo-user'}: {space: string; item: PromptResult; version: PromptVersion; onApply: (value: TaskInput) => void; onClose: () => void; projectId?: string; mediaVersion?: number; actor?: string}) {
  const {t} = useI18n()
  const dialog = useRef<HTMLDialogElement>(null)
  const storageKey = projectId ? applyPendingKey(space, actor, projectId) : ''
  const [recovery] = useState(() => storageKey ? readApplyPending(storageKey) : null)
  const pending = useRef<ApplyPending | null>(recovery)
  const [values, setValues] = useState<Record<string, string>>(() => {try {if (recovery) return (JSON.parse(recovery.body) as {variables?: Record<string, string>}).variables ?? {}} catch { /* Invalid recovery is rejected by the server. */ } return Object.fromEntries((version.variables ?? []).map(v => [v.name, v.default ?? '']))})
  const [preview, setPreview] = useState<{prompt: string; defaults: TaskInput; fingerprint: string} | null>(null)
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(Boolean(recovery))
  const [error, setError] = useState('')
  const fingerprint = JSON.stringify(values)
  const active = useRef(true)
  useNavigationTask(busy || uncertain)
  useEffect(() => {const current = dialog.current; const previous = document.activeElement as HTMLElement | null; active.current = true; current?.showModal(); return () => {active.current = false; current?.close(); previous?.focus()}}, [])
  function message(reason: unknown) {return errorMessage(reason, t('提示词预览失败，请检查变量。')) + (reason instanceof ApiError && reason.field ? ` (${reason.field})` : '')}
  async function compile() {
    if (busy || uncertain) return
    setBusy(true); setError('')
    try {const result = await request<{prompt: string; defaults: TaskInput}>(`/prompt-templates/${item.template.id}/preview?${new URLSearchParams({space_id: space})}`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({version_id: version.id, variables: values})}); if (active.current) setPreview({...result, fingerprint})} catch (reason) {if (active.current) setError(message(reason))} finally {if (active.current) setBusy(false)}
  }
  async function apply() {
    if (busy) return
    if (!projectId) {if (preview && preview.fingerprint === fingerprint) onApply({prompt: preview.prompt, kind: preview.defaults.kind, ratio: preview.defaults.ratio, style: preview.defaults.style, prompt_source: {template_id: item.template.id, version_id: version.id, variables: values, edited: true}}); return}
    if (!pending.current) pending.current = {key: crypto.randomUUID(), item: {...item, version}, body: JSON.stringify({template_id: item.template.id, version_id: version.id, variables: values, expected_media_version: mediaVersion})}
    const operation = pending.current
    try {sessionStorage.setItem(storageKey, JSON.stringify(operation))} catch {setError(t('无法保存请求恢复记录，请允许此站点使用会话存储后重试。')); return}
    setBusy(true); setError('')
    try {
      const result = await request<PromptApplication>(`/projects/${projectId}/prompt-applications?${new URLSearchParams({space_id: space})}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': operation.key}, body: operation.body})
      sessionStorage.removeItem(storageKey); pending.current = null
      if (active.current) {setUncertain(false); const source = result.prompt_source; onApply({prompt: result.prompt, ...result.defaults, prompt_source: {template_id: source.template_id, version_id: source.version_id, variables: source.variables, application_id: result.id, edited: true}, reference_asset_ids: result.reference_asset_ids, application_id: result.id} as TaskInput)}
    } catch (reason) {
      if (!active.current) return
      if (reason instanceof ApiError && reason.status >= 400 && reason.status < 500) {sessionStorage.removeItem(storageKey); pending.current = null; setUncertain(false)} else setUncertain(true)
      setError(message(reason))
    } finally {if (active.current) setBusy(false)}
  }
  const ready = Boolean(preview && preview.fingerprint === fingerprint)
  return <dialog ref={dialog} className="detail-dialog prompt-editor" aria-labelledby="prompt-use-title" onCancel={event => {event.preventDefault(); if (!busy) onClose()}}><div className="dialog-header"><div><h2 id="prompt-use-title">{t(projectId ? '应用项目配方' : '填写提示词变量')}</h2><p>{item.template.name} · {t('版本 {number}', {number: version.number})}</p></div><button className="icon-button" aria-label={t('关闭变量预览')} disabled={busy} onClick={onClose}><Icon name="close" /></button></div><p className="composer-help">{t('先预览完整描述，再填入创作区。此处不会调用模型。')}</p>{projectId && <p className="composer-help">{t('应用会将最终描述和固定版本参考图保存到当前项目，项目成员可查看。不会生成或覆盖已有成片。')}</p>}{version.reference_bindings?.length ? <p>{t('此配方包含 {number} 张有序参考图；实际模型必须支持此组合。', {number: version.reference_bindings.length})}</p> : null}{uncertain && <p className="notice" role="status">{t('项目应用结果尚未确认，请重试原请求进行核对。')}</p>}{error && <p role="alert" className="error">{error}</p>}<form onSubmit={event => {event.preventDefault(); void compile()}}><fieldset className="prompt-fields" disabled={busy || uncertain}><legend className="sr-only">{t('变量取值')}</legend>{(version.variables ?? []).map(v => <label key={v.name}>{v.label || v.name}{v.required && ` · ${t('必填')}`}{v.type === 'enum' ? <select aria-label={v.label || v.name} value={values[v.name] ?? ''} onChange={event => setValues(previous => ({...previous, [v.name]: event.target.value}))}><option value="">{t('请选择')}</option>{v.options?.map(option => <option key={option} value={option}>{option}</option>)}</select> : <input aria-label={v.label || v.name} value={values[v.name] ?? ''} maxLength={v.max_length} required={v.required} onChange={event => setValues(previous => ({...previous, [v.name]: event.target.value}))} />}</label>)}</fieldset><button type="submit" disabled={busy || uncertain}>{t(busy ? '正在预览…' : '预览完整提示词')}</button></form>{ready && <label className="prompt-preview-result">{t('展开后的完整描述')}<textarea aria-label={t('展开后的完整描述')} readOnly value={preview!.prompt} rows={5} /></label>}<div className="dialog-actions"><button disabled={busy} onClick={onClose}>{t('取消')}</button><button className="primary" disabled={busy || (!ready && !uncertain)} onClick={() => {void apply()}}>{t(uncertain ? '重试项目应用' : projectId ? '应用到项目制作' : '填入创作区')}</button></div></dialog>
}
