import {hasPermission} from '../permissions'
import { Suspense, lazy, useEffect, useRef, useState } from 'react'
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom'
import { ApiError, errorMessage, request } from '../api'
import { useAppWorkspace } from '../app/workspace'
import { useI18n } from '../i18n'
import type { DramaProject } from '../drama'
import { NotFoundPage } from './NotFoundPage'
const Avatar3DStudio=lazy(()=>import('../features/avatar3d/Avatar3DStudio').then(module=>({default:module.Avatar3DStudio})))
const DigitalHumanStudio=lazy(()=>import('../components/DigitalHumanStudio').then(module=>({default:module.DigitalHumanStudio})))
const MediaStudio=lazy(()=>import('../components/MediaStudio').then(module=>({default:module.MediaStudio})))
const SocialPublishCenter=lazy(()=>import('../components/SocialPublishCenter').then(module=>({default:module.SocialPublishCenter})))
const ProductionBoard=lazy(()=>import('../features/projects/ProductionBoard').then(module=>({default:module.ProductionBoard})))

type CreationAttempt = {body: string; key: string}
function recoverCreation(key: string): CreationAttempt | null {
  try {const value = JSON.parse(sessionStorage.getItem(key) ?? 'null') as CreationAttempt | null; return value && typeof value.body === 'string' && typeof value.key === 'string' ? value : null} catch {return null}
}

export function WorkspacesPage() {
  const {pathname} = useLocation()
  const {space} = useAppWorkspace()
  return <WorkspaceContent key={`${space?.id}:${pathname}`} />
}

function WorkspaceContent() {
  const {space, setDirty, requestLeave} = useAppWorkspace()
  const {pathname} = useLocation()
  const {workspaceId, mode} = useParams()
  const navigate = useNavigate()
  const {t, formatDate} = useI18n()
  const publishing = pathname.startsWith('/publish')
  const digital = pathname.startsWith('/digital-humans')
  const threeD=digital&&mode==='3d'
  const kind = digital ? 'digital-human' : 'image'
  const root = publishing ? '/publish' : digital ? `/digital-humans/${threeD?'3d':'video'}` : '/studio/workspaces'
  const [projects, setProjects] = useState<DramaProject[]>([])
  const [project, setProject] = useState<DramaProject | null>(null)
  const recoveryKey = `workspace.create:${space!.id}:${kind}`
  const pending = useRef<CreationAttempt | null>(recoverCreation(recoveryKey))
  const [title, setTitle] = useState(() => {try {return pending.current ? (JSON.parse(pending.current.body) as {title: string}).title : ''} catch {return ''}})
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)
  const [tab, setTab] = useState('create')
  const [revision, setRevision] = useState(0)
  const flight = useRef(false)
  const query = new URLSearchParams({space_id: space!.id}).toString()
  useEffect(() => {
    const controller = new AbortController()
    const promise = workspaceId ? request<DramaProject>(`/projects/${encodeURIComponent(workspaceId)}?${query}`, {signal: controller.signal}).then(setProject) : request<DramaProject[]>(`/projects?${query}`, {signal: controller.signal}).then(result => setProjects(result.filter(p => publishing || (p.kind ?? 'drama') === kind)))
    void promise.then(() => {if (!controller.signal.aborted) {setError(''); setLoading(false)}}).catch(e => {if (!controller.signal.aborted) {setError(errorMessage(e, t('作品读取失败'))); setLoading(false)}})
    return () => controller.abort()
  }, [workspaceId, query, kind, publishing, revision, t])
  useEffect(() => {if (!workspaceId) setDirty(!!title.trim() || busy); return () => {if (!workspaceId) setDirty(false)}}, [workspaceId, title, busy, setDirty])
  async function create() {
    if (flight.current || title.trim().length < 1) return
    flight.current = true; setBusy(true); setError('')
    const attempt = pending.current ?? {body: JSON.stringify({title: title.trim(), brief: title.trim() + ' · ' + t(digital ? threeD?'网页三维角色作品':'视频型数字人作品' : '图像创作作品'), kind}), key: crypto.randomUUID()}
    pending.current = attempt
    try {sessionStorage.setItem(recoveryKey, JSON.stringify(attempt))} catch { /* In-memory recovery remains available. */ }
    try {
      const result = await request<DramaProject>(`/projects?${query}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': attempt.key}, body: attempt.body})
      pending.current = null; try {sessionStorage.removeItem(recoveryKey)} catch { /* Optional storage. */ } setTitle(''); setDirty(false)
      setTimeout(() => {void navigate(`${root}/${result.id}`)}, 0)
    } catch (e) {setError(errorMessage(e, t('创建作品失败'))); if (e instanceof ApiError && e.status < 500) {pending.current = null; try {sessionStorage.removeItem(recoveryKey)} catch { /* Optional storage. */ }}}
    finally {flight.current = false; setBusy(false)}
  }
  if (digital && mode !== 'video'&&mode!=='3d') {
    if (mode !== 'interactive') return <NotFoundPage />
    return <section className="workspace-unavailable"><h1>{t('交互型数字人')}</h1><p>{t('实时对话、知识配置与会话部署。')}</p><div className="panel"><h2>{t('尚未接入制作引擎')}</h2><p>{t('视频型数字人和网页三维口播可用；此入口尚不提供实时会话。')}</p><Link to="/digital-humans/video">{t('进入视频型数字人')}</Link></div></section>
  }
  if (loading) return <p role="status">{t('正在读取作品…')}</p>
  if (workspaceId && project && !publishing && (project.kind ?? 'drama') !== kind) return <NotFoundPage />
  return <div className="creative-workspace"><header className="page-heading"><div>{workspaceId && <Link to={root}>{t('返回作品列表')}</Link>}<h1>{workspaceId ? project?.title : t(publishing ? '发布中心' : digital ? threeD?'3D 数字人':'视频型数字人' : '图像作品')}</h1><p>{t(publishing ? '选择作品，检查成品并准备发布。' : digital ? threeD?'已授权GLB角色与配音，在网页中播放。':'独立管理形象、声音、口播视频和成片。' : '保存参考、编辑过程与候选版本，持续打磨同一张图。')}</p></div>{!digital && !publishing && <Link to="/studio">{t('快速图像生成')}</Link>}</header>
    {error && <div role="alert" className="error">{error}<button onClick={() => setRevision(v => v + 1)}>{t('重新加载')}</button></div>}
    {!workspaceId && <>
      {!publishing && hasPermission(space,'workspace.write') && <form className="workspace-create" onSubmit={e => {e.preventDefault(); void create()}}><label>{t('作品名称')}<input value={title} maxLength={80} disabled={busy || pending.current !== null} onChange={e => setTitle(e.target.value)} placeholder={t(digital ? '例如：产品介绍口播' : '例如：人物定妆与海报')} /></label><button className="primary" disabled={busy || !title.trim()}>{t(pending.current ? '核对创建结果' : '新建作品')}</button></form>}
      {!projects.length && <p className="panel">{t(publishing ? '还没有可交付作品，请先完成创作。' : '还没有作品，从新建作品开始。')}</p>}
      <div className="project-grid">{projects.map(p => <Link className="project-card" key={p.id} to={`${root}/${p.id}`}><h2>{p.title}</h2><p>{p.brief}</p><time>{formatDate(p.updated_at)}</time></Link>)}</div>
    </>}
    {workspaceId&&project&&!publishing&&<p><Link className="button-link" to={`/workflows/${project.id}`}>{t('创作流程')}</Link></p>}
    {workspaceId && project && <Suspense fallback={<p role="status">{t('正在读取作品…')}</p>}>{(publishing ? <SocialPublishCenter key={project.id} projectId={project.id} space={space!.id} /> : <>
      {digital && <nav className="workspace-tabs"><button className={tab === 'create' ? 'active' : ''} onClick={() => requestLeave(() => setTab('create'))}>{t(threeD?'三维角色与配音':'形象与口播制作')}</button><button className={tab === 'edit' ? 'active' : ''} onClick={() => requestLeave(() => setTab('edit'))}>{t(threeD?'已有素材与剪辑':'剪辑与交付')}</button>{!threeD&&<Link to={`/publish/${project.id}`}>{t('准备发布')}</Link>}</nav>}
      {digital ? tab === 'create' ? threeD?<Avatar3DStudio project={project} space={space!.id} disabled={!hasPermission(space,'workspace.write')} onDirtyChange={setDirty}/>:<DigitalHumanStudio project={project} space={space!.id} disabled={!hasPermission(space,'workspace.write')} onDirtyChange={setDirty} /> : <MediaStudio project={project} space={space!.id} onDirtyChange={setDirty} /> : <ProductionBoard project={project} space={space!.id} disabled={!hasPermission(space,'workspace.write')} onDirtyChange={setDirty} />}
    </>)}</Suspense>}
  </div>
}
