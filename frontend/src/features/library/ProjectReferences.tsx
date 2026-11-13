import { useEffect, useRef, useState } from 'react'
import { ApiError, errorMessage, request } from '../../api'
import { useNavigationTask } from '../../app/navigationGuard'
import { useAppWorkspace } from '../../app/workspace'
import { useI18n } from '../../i18n'
import { kindLabels, type LibraryAsset, type LibraryVersion, type Page } from './types'
import { readPendingBinding, savePendingBinding, clearPendingBinding } from './pending'

interface Binding {id: string; name: string; kind: keyof typeof kindLabels; library_asset_id: string; asset_version_id: string; shot_id: string; media_asset_ids: string[]}
interface VersionCheck {binding_id:string;pinned_version_id:string;current_version_id?:string;status:'current'|'outdated'|'archived'|'unavailable'}
interface BindingView {items: Binding[]; media_version: number}
export function ProjectReferences({space, projectId, shots, disabled}: {space: string; projectId: string; shots: {id: string; description: string}[]; disabled: boolean}) {
  const {t} = useI18n()
  const workspace = useAppWorkspace()
  const readOnly = disabled || workspace.space?.role === 'viewer'
  const [assets, setAssets] = useState<LibraryAsset[]>([])
  const [assetCursor, setAssetCursor] = useState('')
  const [assetNext, setAssetNext] = useState('')
  const [assetId, setAssetId] = useState('')
  const [versions, setVersions] = useState<{assetId: string; items: LibraryVersion[]}>({assetId: '', items: []})
  const [versionId, setVersionId] = useState('')
  const [versionCursor, setVersionCursor] = useState('')
  const [versionNext, setVersionNext] = useState('')
  const [shot, setShot] = useState('')
  const [view, setView] = useState<BindingView | null>(null)
  const [checks,setChecks]=useState<{items:VersionCheck[];media_version:number}|null>(null)
  const [checking,setChecking]=useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [pending, setPending] = useState(() => readPendingBinding(space, projectId))
  const mounted = useRef(true)
  useEffect(() => {mounted.current = true; return () => {mounted.current = false}}, [])
  const [revision, setRevision] = useState(0)
  const q = new URLSearchParams({space_id: space}).toString()
  useNavigationTask(busy || !!pending)
  useEffect(() => {
    const abort = new AbortController()
    void Promise.all([request<Page<LibraryAsset>>(`/library/assets?${q}&status=active&limit=100&cursor=${encodeURIComponent(assetCursor)}`, {signal: abort.signal}), request<BindingView>(`/projects/${projectId}/library-bindings?${q}`, {signal: abort.signal})]).then(([library, bindings]) => {if (!abort.signal.aborted) {setAssets(old => [...(assetCursor ? old.filter(a => !library.items.some(next => next.id === a.id)) : []), ...library.items]); setAssetNext(library.next_cursor); setView(bindings)}}).catch(reason => {if (!abort.signal.aborted) setError(errorMessage(reason, t('资产读取失败')))})
    return () => abort.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q, projectId, revision, assetCursor])
  useEffect(() => {
    if (!assetId) return
    const abort = new AbortController()
    void request<Page<LibraryVersion>>(`/library/assets/${assetId}/versions?${q}&limit=100&cursor=${encodeURIComponent(versionCursor)}`, {signal: abort.signal}).then(result => {if (!abort.signal.aborted) {setVersions(old => ({assetId, items: [...(versionCursor && old.assetId === assetId ? old.items.filter(v => !result.items.some(next => next.id === v.id)) : []), ...result.items].sort((a, b) => b.number - a.number)})); setVersionNext(result.next_cursor)}}).catch(reason => {if (!abort.signal.aborted) setError(errorMessage(reason, t('资产读取失败')))})
    return () => abort.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [assetId, q, versionCursor])
  async function bind() {
    if (!view || busy || readOnly) return
    const selected = assets.find(a => a.id === assetId)
    if (!pending && !selected) return
    const p = pending ?? {key: crypto.randomUUID(), body: JSON.stringify({expected_media_version: view.media_version, library_asset_id: assetId, asset_version_id: versionId, usage: selected!.kind, shot_id: shot})}
    try {savePendingBinding(space, projectId, p)} catch {setError(t('无法保存请求恢复记录，请允许此站点使用会话存储后重试。')); return}
    setPending(p); setBusy(true); setError('')
    try {
      await request(`/projects/${projectId}/library-bindings?${q}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': p.key}, body: p.body})
      clearPendingBinding(space, projectId)
      if (mounted.current) {setPending(null); setRevision(v => v + 1)}
    } catch (reason) {if (mounted.current) {setError(errorMessage(reason, t('资产保存失败'))); if (reason instanceof ApiError && reason.status < 500 && reason.status !== 401 && reason.status !== 403) {clearPendingBinding(space, projectId); setPending(null); setRevision(v => v + 1)}}} finally {if (mounted.current) setBusy(false)}
  }
  async function checkVersions(){setChecking(true);setError('');try{setChecks(await request(`/projects/${projectId}/library-bindings/check?${q}`))}catch(e){setError(errorMessage(e,t('参考版本核对失败')))}finally{setChecking(false)}}
  const statusLabels={current:'与库中当前版本一致',outdated:'库中已有新版本，请人工复查',archived:'库资产已归档，历史固定参考保留',unavailable:'来源版本不可用，请核对后重新绑定'}
  const currentVersions = versions.assetId === assetId ? versions.items : []
  const selectedVersion = currentVersions.find(v => v.id === versionId)
  return <section className="panel"><h2>{t('项目参考资产')}</h2><p>{t('选择明确版本后复制到本项目。以后更新库资产不会自动替换这些参考。')}</p>{error && <p role="alert" className="error">{error}</p>}
    <fieldset disabled={readOnly || busy || !!pending} className="media-controls"><label>{t('创作资产')}<select value={assetId} onChange={event => {setAssetId(event.target.value); setVersionId(''); setVersionCursor(''); setVersionNext('')}}><option value="">{t('选择资产')}</option>{assets.map(a => <option key={a.id} value={a.id}>{t(kindLabels[a.kind])} · {a.name}</option>)}</select></label><label>{t('参考版本')}<select value={versionId} onChange={event => setVersionId(event.target.value)}><option value="">{t('选择版本')}</option>{currentVersions.map(v => <option key={v.id} value={v.id}>{t('版本')} {v.number} · {v.card.voice?t(v.card.voice.native_preset?'已保存原生音色':'声音意图，尚无原生音色'):`${v.references.length} ${t('张参考图')}`}</option>)}</select></label><label>{t('关联镜头')}<select value={shot} onChange={event => setShot(event.target.value)}><option value="">{t('整个项目')}</option>{shots.map(s => <option key={s.id} value={s.id}>{s.id} · {s.description.slice(0, 40)}</option>)}</select></label></fieldset>
    {selectedVersion && <div className="editing-note"><strong>{selectedVersion.card.name}</strong><p>{selectedVersion.card.description}</p><ul>{selectedVersion.card.prohibited_changes.map(item => <li key={item}>{item}</li>)}</ul></div>}
    <div className="media-actions">{assetNext && <button disabled={busy || !!pending || assetCursor === assetNext} onClick={() => setAssetCursor(assetNext)}>{t('加载更多资产')}</button>}{versionNext && <button disabled={busy || !!pending || versionCursor === versionNext} onClick={() => setVersionCursor(versionNext)}>{t('加载更多历史版本')}</button>}</div>
    <div className="media-actions"><button disabled={readOnly || busy || !view || (!pending && !versionId)} onClick={() => void bind()}>{t(pending ? '使用原请求核对结果' : '绑定此版本')}</button>
    <button disabled={checking||busy} onClick={()=>void checkVersions()}>{t(checking?'正在核对…':'核对参考版本')}</button></div>{checks&&<div className="editing-note"><p>{t('素材版本 {version} 的核对结果，不会自动替换参考或恢复授权。',{version:checks.media_version})}</p>{checks.items.map(c=><p key={c.binding_id}>{view?.items.find(b=>b.id===c.binding_id)?.name} · {t(statusLabels[c.status])}<br/>{t('固定版本')} {c.pinned_version_id.slice(0,12)}{c.current_version_id&&<> · {t('库中当前版本')} {c.current_version_id.slice(0,12)}</>}</p>)}</div>}
    <ul className="drama-history">{view?.items.map(b => <li key={b.id}><strong>{b.name}</strong> · {t(kindLabels[b.kind])} · {b.shot_id || t('整个项目')}<p>{t('固定版本')} {b.asset_version_id.slice(0, 12)} · {b.kind==='voice'?t('声音版本已固定，使用时核对原生音色模型'):b.media_asset_ids.length?`${b.media_asset_ids.length} ${t('张参考图')}`:t('文字意图版本已固定')}</p></li>)}</ul>
    <p className="drama-help">{t('绑定后在制作工作区明确选择传入方式。人物与风格可使用文字或参考图；声音需匹配已配置的原生音色。绑定本身不调用模型。')}</p>
  </section>
}
