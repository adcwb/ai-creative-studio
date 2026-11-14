import { useEffect, useRef, useState } from 'react'
import { ApiError, errorMessage, request } from '../api'
import { useI18n } from '../i18n'
import type { DramaProject, DramaScript, StoryboardPlan } from '../drama'

interface Revision {id: string; kind: 'script' | 'storyboard'; created_at: string; script_version: number; script?: DramaScript; storyboard?: StoryboardPlan}
interface Props {project: DramaProject; space: string; disabled: boolean; onRestore: () => void; onDirtyChange: (dirty: boolean) => void}

// Keep the history mounted while a restore is uncertain, even if collapsed.
export function ProjectHistory(props: Props) {
  const {t} = useI18n()
  const [opened, setOpened] = useState(false)
  return <details className="panel drama-history" onToggle={event => {if (event.currentTarget.open) setOpened(true)}}>
    <summary>{t('版本历史')}</summary>
    {opened && <HistoryContent key={`${props.space}:${props.project.id}`} {...props} />}
  </details>
}

function HistoryContent({project, space, disabled, onRestore, onDirtyChange}: Props) {
  const {t, formatDate} = useI18n()
  const [items, setItems] = useState<Revision[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [revision, setRevision] = useState(0)
  const [confirm, setConfirm] = useState<Revision | null>(null)
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [writeError, setWriteError] = useState('')
  const pending = useRef<{id: string; key: string; version: number} | null>(null)
  const flight = useRef(false)
  const alive = useRef(true)
  const path = `/projects/${encodeURIComponent(project.id)}/history`
  useEffect(() => {alive.current = true; return () => {alive.current = false}}, [])
  useEffect(() => {onDirtyChange(busy || uncertain); return () => onDirtyChange(false)}, [busy, uncertain, onDirtyChange])
  async function restore() {
    if (flight.current || !confirm && !pending.current) return
    flight.current = true; setBusy(true); setWriteError('')
    const attempt = pending.current ?? {id: confirm!.id, key: crypto.randomUUID(), version: project.version}
    pending.current = attempt
    try {
      await request<DramaProject>(`${path}/${encodeURIComponent(attempt.id)}/restore?${new URLSearchParams({space_id: space})}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': attempt.key}, body: JSON.stringify({expected_version: attempt.version})})
      if (alive.current) {pending.current = null; setUncertain(false); setConfirm(null); onRestore()}
    } catch (failure) {
      if (alive.current) {
        const unknown = uncertain || !(failure instanceof ApiError) || failure.status >= 500
        if (!unknown) pending.current = null
        setUncertain(unknown); setWriteError(errorMessage(failure, t('恢复结果尚未确认，请核对原请求。')))
      }
    } finally {flight.current = false; if (alive.current) setBusy(false)}
  }
  useEffect(() => {
    const abort = new AbortController()
    void request<Revision[]>(`${path}?${new URLSearchParams({space_id: space})}`, {signal: abort.signal}).then(value => {if (!abort.signal.aborted) {setItems(value); setError(''); setLoading(false)}}).catch(failure => {if (!abort.signal.aborted) {setError(errorMessage(failure, t('历史读取失败'))); setLoading(false)}})
    return () => abort.abort()
  }, [path, space, project.version, revision, t])
  const locked = disabled || busy || uncertain
  return <>
    <p className="drama-help">{t('恢复会产生新的当前版本，原历史保留；恢复剧本后，旧分镜可能过期。')}</p>
    {loading && <p role="status">{t('正在读取版本历史…')}</p>}
    {error && <div className="error" role="alert">{error}<button type="button" onClick={() => {setLoading(true); setRevision(value => value + 1)}}>{t('重试读取历史')}</button></div>}
    {writeError && <p className="error" role="alert">{writeError}</p>}
    {uncertain && <button type="button" disabled={busy} onClick={() => void restore()}>{t('核对原恢复请求')}</button>}
    <ol>{items.map(item => <li key={item.id}>
      <div><span>{t(item.kind === 'script' ? '剧本' : '分镜')} · {item.script_version}</span><time>{formatDate(item.created_at)}</time></div>
      <details><summary>{t('查看历史内容')}</summary><pre>{JSON.stringify(item.script ?? item.storyboard, null, 2)}</pre></details>
      <button type="button" disabled={locked} onClick={() => setConfirm(item)}>{t('恢复此{kind}版本', {kind: t(item.kind === 'script' ? '剧本' : '分镜')})}</button>
    </li>)}</ol>
    {!loading && !error && !items.length && <p>{t('保存或生成新内容后可在此查看历史。')}</p>}
    {confirm && <div className="editing-note"><p>{t('确认恢复此{kind}？当前内容将被替换，历史保留。', {kind: t(confirm.kind === 'script' ? '剧本' : '分镜')})}</p>
      <button type="button" disabled={busy || uncertain} onClick={() => setConfirm(null)}>{t('取消恢复')}</button>
      <button type="button" disabled={locked} onClick={() => void restore()}>{t('确认恢复历史版本')}</button>
    </div>}
  </>
}
