import { useEffect, useRef, useState } from 'react'
import { useAppWorkspace } from '../app/workspace'
import { ApiError, errorMessage, request } from '../api'
import { useI18n } from '../i18n'
import { LibraryEditor } from '../features/library/LibraryEditor'
import { kindLabels, type LibraryAsset, type LibraryKind, type LibraryResult, type Page } from '../features/library/types'
import '../features/library/library.css'
import { readPendingLibrary, clearPendingLibrary } from '../features/library/pending'
import { useNavigationTask } from '../app/navigationGuard'

export function LibraryPage() {
  const {space, requestLeave} = useAppWorkspace()
  const {t, formatDate} = useI18n()
  const [kind, setKind] = useState<LibraryKind>('character')
  const [status, setStatus] = useState('active')
  const [items, setItems] = useState<LibraryAsset[]>([])
  const [cursor, setCursor] = useState('')
  const [next, setNext] = useState('')
  const [revision, setRevision] = useState(0)
  const [loaded, setLoaded] = useState('')
  const [error, setError] = useState('')
  const [editor, setEditor] = useState<{initial: LibraryResult | null; key: string} | null>(null)
  const [opening, setOpening] = useState(false)
  const [recovery, setRecovery] = useState(() => readPendingLibrary(space!.id))
  const mounted = useRef(true)
  useEffect(() => {mounted.current = true; return () => {mounted.current = false}}, [])
  useNavigationTask(opening || !!recovery)
  const query = new URLSearchParams({space_id: space!.id, kind, status, ...(cursor ? {cursor} : {})}).toString()
  const loadKey = `${query}:${revision}`
  useEffect(() => {
    const abort = new AbortController()
    void request<Page<LibraryAsset>>(`/library/assets?${query}`, {signal: abort.signal}).then(result => {if (!abort.signal.aborted) {setItems(result.items); setNext(result.next_cursor); setLoaded(loadKey); setError('')}}).catch(reason => {if (!abort.signal.aborted) {setError(errorMessage(reason, t('资产读取失败'))); setLoaded(loadKey)}})
    return () => abort.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, loadKey])
  async function open(asset: LibraryAsset) {
    setOpening(true); setError('')
    try {const result = await request<LibraryResult>(`/library/assets/${asset.id}?${new URLSearchParams({space_id: space!.id})}`); if (mounted.current) setEditor({initial: result, key: `${asset.id}:${result.asset.head_version_id}`})} catch (reason) {if (mounted.current) setError(errorMessage(reason, t('资产读取失败')))} finally {if (mounted.current) setOpening(false)}
  }
  async function recover() {
    if (!recovery || opening || space?.role === 'viewer') return
    setOpening(true); setError('')
    try {
      const result = await request<LibraryResult>(`${recovery.path}?${new URLSearchParams({space_id: space!.id})}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': recovery.key}, body: recovery.body})
      clearPendingLibrary(space!.id)
      if (mounted.current) {setRecovery(null); setEditor({initial: result, key: `${result.asset.id}:${result.asset.head_version_id}:${result.asset.status}`}); setRevision(v => v + 1)}
    } catch (reason) {if (mounted.current) {setError(errorMessage(reason, t('资产保存失败'))); if (reason instanceof ApiError && reason.status < 500 && reason.status !== 401 && reason.status !== 403) {clearPendingLibrary(space!.id); setRecovery(null)}}} finally {if (mounted.current) setOpening(false)}
  }
  return <div className="library-page"><header className="page-heading"><div><h1>{t('创作资产库')}</h1><p>{t('建立角色、商品和场景的可复用参考，保留每一次版本选择。')}</p></div></header>
    {recovery && <div className="editing-note"><p>{t('发现未确认的资产保存请求，请先核对结果。')}</p><button disabled={opening || space?.role === 'viewer'} onClick={() => void recover()}>{t('使用原请求核对结果')}</button></div>}
    {editor ? <LibraryEditor key={editor.key} space={space!.id} initial={editor.initial} kind={editor.initial?.asset.kind ?? kind} readOnly={space?.role === 'viewer'} onClose={() => requestLeave(() => setEditor(null))} onSaved={result => {setEditor({initial: result, key: `${result.asset.id}:${result.asset.head_version_id}:${result.asset.status}`}); setRevision(v => v + 1)}} /> : <>
      <div className="library-toolbar"><label>{t('资产类型')}<select value={kind} onChange={event => {setKind(event.target.value as LibraryKind); setCursor('')}}>{Object.entries(kindLabels).map(([value, label]) => <option key={value} value={value}>{t(label)}</option>)}</select></label><label>{t('状态')}<select value={status} onChange={event => {setStatus(event.target.value); setCursor('')}}><option value="active">{t('可用')}</option><option value="archived">{t('已归档')}</option></select></label><button disabled={!!recovery || space?.role === 'viewer'} onClick={() => setEditor({initial: null, key: 'new'})}>{t('建立资产卡')}</button></div>
      {loaded !== loadKey && <p role="status">{t('正在读取资产…')}</p>}{error && <div className="error" role="alert">{error}<button onClick={() => setRevision(v => v + 1)}>{t('重试')}</button></div>}
      {loaded === loadKey && !error && <><div className="project-grid">{items.map(asset => <button type="button" className="project-card library-identity" key={asset.id} disabled={opening || !!recovery} onClick={() => void open(asset)}><span>{t(kindLabels[asset.kind])}</span><h2>{asset.name}</h2><p>{t(asset.status === 'archived' ? '已归档' : '可用')}</p><time>{formatDate(asset.updated_at)}</time></button>)}</div>{items.length === 0 && <section className="panel page-empty"><h2>{t('还没有此类资产')}</h2><p>{t('先建立资产卡，再添加项目中的多角度参考图。')}</p></section>}<div className="media-actions">{cursor && <button onClick={() => setCursor('')}>{t('返回第一页')}</button>}{next && <button onClick={() => setCursor(next)}>{t('下一页')}</button>}</div></>}
    </>}
  </div>
}
