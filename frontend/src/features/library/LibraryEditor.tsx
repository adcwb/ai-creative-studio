import { useEffect, useRef, useState } from 'react'
import { ApiError, baseURL, errorMessage, request } from '../../api'
import { useNavigationTask } from '../../app/navigationGuard'
import { useI18n } from '../../i18n'
import { savePendingLibrary, clearPendingLibrary } from './pending'
import { cardFields, emptyCard, kindLabels, type LibraryCard, type LibraryKind, type LibraryResult, type LibraryVersion, type LibraryWrite, type Page, type ReferenceInput } from './types'

interface ProjectOption {id: string; title: string}
interface MediaOption {id: string; name: string; kind: string; file_url: string}

export function LibraryEditor({space, initial, kind, readOnly, onSaved, onClose}: {space: string; initial: LibraryResult | null; kind: LibraryKind; readOnly: boolean; onSaved: (r: LibraryResult) => void; onClose: () => void}) {
  const {t, formatDate} = useI18n()
  const [card, setCard] = useState<LibraryCard>(() => structuredClone(initial?.version.card ?? emptyCard(kind)))
  const [constraints, setConstraints] = useState(() => initial?.version.card.prohibited_changes.join('\n') ?? '')
  const [references, setReferences] = useState<ReferenceInput[]>(() => initial?.version.references.map(ref => ({library_version_id: initial.version.id, library_reference_id: ref.id, role: ref.role, view_angle: ref.view_angle})) ?? [])
  const [projects, setProjects] = useState<ProjectOption[]>([])
  const [project, setProject] = useState('')
  const [media, setMedia] = useState<{project: string; items: MediaOption[]}>({project: '', items: []})
  const [asset, setAsset] = useState('')
  const [angle, setAngle] = useState('')
  const [versions, setVersions] = useState<LibraryVersion[]>([])
  const [versionCursor, setVersionCursor] = useState('')
  const [versionNext, setVersionNext] = useState('')
  const [historyVersion, setHistoryVersion] = useState<LibraryVersion | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [uncertain, setUncertain] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [confirmArchive, setConfirmArchive] = useState(false)
  const pending = useRef<{path: string; body: string; key: string} | null>(null)
  const mounted = useRef(true)
  useEffect(() => {mounted.current = true; return () => {mounted.current = false}}, [])
  useNavigationTask(dirty || busy || uncertain)
  const locked = readOnly || initial?.asset.status === 'archived' || busy || uncertain
  const q = new URLSearchParams({space_id: space}).toString()
  useEffect(() => {
    const abort = new AbortController()
    void request<ProjectOption[]>(`/projects?${q}`, {signal: abort.signal}).then(result => {if (!abort.signal.aborted) setProjects(result)}).catch(reason => {if (!abort.signal.aborted) setError(errorMessage(reason, t('项目列表读取失败')))})
    if (initial) void request<Page<LibraryVersion>>(`/library/assets/${initial.asset.id}/versions?${q}&limit=100&cursor=${encodeURIComponent(versionCursor)}`, {signal: abort.signal}).then(result => {if (!abort.signal.aborted) {setVersions(old => [...(versionCursor ? old.filter(v => !result.items.some(next => next.id === v.id)) : []), ...result.items].sort((a, b) => b.number - a.number)); setVersionNext(result.next_cursor)}}).catch(reason => {if (!abort.signal.aborted) setError(errorMessage(reason, t('资产读取失败')))})
    return () => abort.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, initial?.asset.id, versionCursor])
  useEffect(() => {
    if (!project) return
    const abort = new AbortController()
    void request<{assets: MediaOption[]}>(`/projects/${project}/media?${q}`, {signal: abort.signal}).then(result => {if (!abort.signal.aborted) setMedia({project, items: result.assets.filter(item => item.kind === 'image')})}).catch(reason => {if (!abort.signal.aborted) setError(errorMessage(reason, t('资产读取失败')))})
    return () => abort.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [project, q])
  const changeCard = (patch: Partial<LibraryCard>) => {setCard(value => ({...value, ...patch})); setDirty(true)}
  async function submit(archive = false) {
    if (busy || readOnly) return
    if (!pending.current) {
      const body: LibraryWrite = {kind, card: {...card, prohibited_changes: constraints.split('\n').map(value => value.trim()).filter(Boolean)}, references, ...(initial ? {expected_asset_version_id: initial.asset.head_version_id} : {})}
      pending.current = {path: initial ? `/library/assets/${initial.asset.id}/${archive ? 'archive' : 'versions'}` : '/library/assets', body: JSON.stringify(archive ? {expected_asset_version_id: initial!.asset.head_version_id} : body), key: crypto.randomUUID()}
    }
    const current = pending.current
    try {savePendingLibrary(space, current)} catch {pending.current = null; setError(t('无法保存请求恢复记录，请允许此站点使用会话存储后重试。')); return}
    setBusy(true); setError('')
    try {
      const result = await request<LibraryResult>(`${current.path}?${q}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': current.key}, body: current.body})
      if (!mounted.current) return
      clearPendingLibrary(space); pending.current = null; setUncertain(false); setDirty(false); onSaved(result)
    } catch (reason) {
      if (!mounted.current) return
      const definite = reason instanceof ApiError && reason.status < 500 && reason.status !== 401 && reason.status !== 403
      if (definite) {clearPendingLibrary(space); pending.current = null}
      setUncertain(!definite); setError(errorMessage(reason, t('资产保存失败')))
    } finally {if (mounted.current) setBusy(false)}
  }
  return <section className="panel library-editor" aria-labelledby="library-editor-title">
    <header className="section-heading"><h2 id="library-editor-title">{t(initial ? '编辑资产卡' : '建立资产卡')} · {t(kindLabels[kind])}</h2><button type="button" disabled={busy || uncertain} onClick={onClose}>{t('返回资产列表')}</button></header>
    <p className="drama-help">{t('版本固定引用；新版本不会自动改变已有镜头。参考图改善一致性，不保证锁脸或商品细节完全一致。')}</p>
    {initial?.asset.status === 'archived' && <p role="status">{t('已归档，历史版本仍可查看。')}</p>}
    {error && <p className="error" role="alert">{error}</p>}
    {uncertain && <button disabled={busy} onClick={() => void submit()}>{t('使用原请求核对结果')}</button>}
    <form onSubmit={event => {event.preventDefault(); void submit()}}>
      <fieldset disabled={locked} className="library-card-fields"><label>{t('资产名称')}<input required maxLength={80} value={card.name} onChange={event => changeCard({name: event.target.value})} /></label><label>{t('描述')}<textarea maxLength={2000} value={card.description} onChange={event => changeCard({description: event.target.value})} /></label>
        {cardFields[kind].map(([field, label]) => <label key={field}>{t(label)}<textarea maxLength={2000} value={(card[kind] as Record<string, string> | undefined)?.[field] ?? ''} onChange={event => changeCard({[kind]: {...card[kind], [field]: event.target.value}})} /></label>)}
        <label className="library-wide">{t('禁止变化项（每行一项，最多 20 项）')}<textarea value={constraints} maxLength={6020} onChange={event => {setConstraints(event.target.value); setDirty(true)}} /></label>
      </fieldset>
      <fieldset disabled={locked || references.length >= 12} className="library-reference-input"><legend>{t('多角度参考')}</legend><label>{t('来源项目')}<select value={project} onChange={event => {setProject(event.target.value); setAsset('')}}><option value="">{t('选择项目')}</option>{projects.map(p => <option key={p.id} value={p.id}>{p.title}</option>)}</select></label><label>{t('参考图片')}<select value={asset} disabled={!project || media.project !== project} onChange={event => setAsset(event.target.value)}><option value="">{t('选择图片')}</option>{(media.project === project ? media.items : []).map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label>{t('角度或用途')}<input maxLength={80} value={angle} onChange={event => setAngle(event.target.value)} placeholder={t('例如：正面、侧面、包装细节')} /></label><button type="button" disabled={!asset || !project || references.some(ref => ref.asset_id === asset && ref.project_id === project)} onClick={() => {setReferences(items => [...items, {project_id: project, asset_id: asset, role: 'reference', view_angle: angle.trim()}]); setDirty(true); setAsset(''); setAngle('')}}>{t('添加参考')}</button></fieldset>
      <ul className="library-reference-list">{references.map((ref, index) => <li key={`${ref.project_id}:${ref.asset_id}:${index}`}><span>{ref.view_angle || t('参考图片')} · {(ref.asset_id ?? ref.library_reference_id).slice(0, 8)}</span><button type="button" disabled={locked} onClick={() => {setReferences(items => items.filter((_, i) => i !== index)); setDirty(true)}}>{t('移除')}</button></li>)}</ul>
      <div className="media-actions"><button type="submit" disabled={locked || !card.name.trim() || constraints.split('\n').filter(v => v.trim()).length > 20}>{t(initial ? '保存为新版本' : '创建资产卡')}</button>{initial && <button type="button" disabled={locked} onClick={() => setConfirmArchive(true)}>{t('归档资产')}</button>}</div>
    </form>
    {confirmArchive && <div className="editing-note"><p>{t('归档后不能新增版本，已有历史引用保留。')}</p><button disabled={busy || uncertain} onClick={() => void submit(true)}>{t('确认归档')}</button><button onClick={() => setConfirmArchive(false)}>{t('取消')}</button></div>}
    {versionNext && <button onClick={() => setVersionCursor(versionNext)} disabled={versionCursor === versionNext}>{t('加载更多历史版本')}</button>}
    {initial && <><h3>{t('参考版本')}</h3><div className="library-version-list">{versions.map(v => <button type="button" key={v.id} aria-pressed={historyVersion?.id === v.id} onClick={() => setHistoryVersion(v)}>{t('版本')} {v.number} · {formatDate(v.created_at)}</button>)}</div>{(() => {const v = historyVersion ?? initial.version; return <section aria-label={t('历史版本预览')}><h4>{v.card.name} · {t('版本')} {v.number}</h4><p>{v.card.description}</p><dl className="library-details">{cardFields[kind].map(([field, label]) => <div key={field}><dt>{t(label)}</dt><dd>{(v.card[kind] as Record<string, string> | undefined)?.[field] || '—'}</dd></div>)}</dl><ul>{v.card.prohibited_changes.map(item => <li key={item}>{item}</li>)}</ul><div className="library-reference-grid">{v.references.map(ref => <figure key={ref.id}><img src={`${baseURL}/api/v1/library/assets/${initial.asset.id}/versions/${v.id}/references/${ref.id}/file?${q}`} alt={ref.view_angle || v.card.name} /><figcaption>{ref.view_angle || t('参考图片')} · {ref.width} × {ref.height}</figcaption></figure>)}</div></section>})()}</>}
  </section>
}
