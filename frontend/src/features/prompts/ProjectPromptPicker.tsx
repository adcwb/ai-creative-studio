import {useEffect, useRef, useState} from 'react'
import {errorMessage, request, type TaskInput} from '../../api'
import {useI18n} from '../../i18n'
import {Icon} from '../../components/Icon'
import {useAppWorkspace} from '../../app/workspace'
import {UsePromptDialog} from './UsePromptDialog'
import {applyPendingKey, readApplyPending, type AppliedPromptInput, type PromptPage, type PromptResult} from './types'
export function ProjectPromptPicker({space, projectId, mediaVersion, disabled, currentPrompt, onApplied}: {space: string; projectId: string; mediaVersion: number; disabled: boolean; currentPrompt: string; onApplied: (value: AppliedPromptInput) => void}) {
  const {t} = useI18n()
  const actor = useAppWorkspace().session.user?.id ?? 'demo-user'
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<PromptResult | null>(null)
  const [search, setSearch] = useState('')
  const [items, setItems] = useState<PromptResult[]>([])
  const [cursor, setCursor] = useState('')
  const [next, setNext] = useState('')
  const [error, setError] = useState('')
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {if (!open) return; const previous = document.activeElement as HTMLElement | null; const current = dialog.current; current?.showModal(); return () => {current?.close(); if (previous?.isConnected) previous.focus()}}, [open])
  useEffect(() => {if (!open) return; const abort = new AbortController(); const timer = setTimeout(() => {void request<PromptPage<PromptResult>>(`/prompt-templates?${new URLSearchParams({space_id: space, q: search, status: 'active', cursor})}`, {signal: abort.signal}).then(result => {if (!abort.signal.aborted) {setItems(previous => cursor ? [...previous, ...result.items] : result.items); setNext(result.next_cursor); setError('')}}).catch(reason => {if (!abort.signal.aborted) setError(errorMessage(reason, t('无法读取我的提示词，请重试。')))});}, 150); return () => {clearTimeout(timer); abort.abort()}}, [space, open, cursor, search, t])
  function choose(item: PromptResult) {if (currentPrompt && !window.confirm(t('用此提示词替换当前画面描述和参数？取消可保留原草稿。'))) return; setOpen(false); setSelected(item)}
  return <><button type="button" disabled={disabled} onClick={() => {const pending = readApplyPending(applyPendingKey(space, actor, projectId)); if (pending) setSelected(pending.item); else setOpen(true)}}>{t('使用保存的提示词')}</button>{open && <dialog ref={dialog} className="detail-dialog prompt-editor" aria-labelledby="project-prompt-title" onCancel={() => setOpen(false)}><div className="dialog-header"><h2 id="project-prompt-title">{t('选择项目提示词')}</h2><button className="icon-button" aria-label={t('关闭项目提示词')} onClick={() => setOpen(false)}><Icon name="close" /></button></div><label>{t('搜索提示词')}<input type="search" value={search} maxLength={100} onChange={event => {setCursor(''); setSearch(event.target.value)}} /></label>{error && <p className="error" role="alert">{error}</p>}<div className="prompt-history">{items.map(item => <button type="button" className="prompt-picker-option" key={item.template.id} onClick={() => choose(item)}><strong>{item.template.name}</strong><span>{t('版本 {number}', {number: item.version.number})} · {t('参考图 {number}', {number: item.version.reference_bindings?.length ?? 0})}</span></button>)}{!items.length && !error && <p>{t('没有可选提示词，可先在图像创作页保存。')}</p>}</div>{next && <button onClick={() => setCursor(next)}>{t('加载更多提示词')}</button>}</dialog>}{selected && <UsePromptDialog space={space} item={selected} version={selected.version} projectId={projectId} mediaVersion={mediaVersion} actor={actor} onClose={() => setSelected(null)} onApply={(value: TaskInput) => {onApplied(value as AppliedPromptInput); setSelected(null)}} />}</>
}
