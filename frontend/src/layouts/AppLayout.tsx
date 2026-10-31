import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, Outlet, useBlocker, useLocation, useNavigate, useOutletContext } from 'react-router-dom'
import { errorMessage, request, type Space } from '../api'
import { useI18n } from '../i18n'
import { useCapabilities } from '../hooks/useCapabilities'
import { workspaceTitle } from '../workspaceTitle'
import { StudioShell } from '../components/StudioShell'
import { ConfirmDialog } from '../components/ConfirmDialog'
import type { SessionContext, WorkspaceContext } from '../app/workspace'
import { NavigationTaskContext } from '../app/navigationGuard'

export function AppLayout() {
  const {session, logout} = useOutletContext<SessionContext>()
  const {t, locale} = useI18n()
  const provider = useCapabilities()
  const location = useLocation()
  const navigate = useNavigate()
  const [spaces, setSpaces] = useState<Space[]>([])
  const [spaceID, setSpaceID] = useState(() => {
    try { return sessionStorage.getItem('studio.space') ?? '' } catch { return '' }
  })
  const [error, setError] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [revision, setRevision] = useState(0)
  const dirtySources = useRef({workspace: false, image: false, tasks: false})
  const taskSources = useRef(new Set<string>())
  const [dirty, setDirtyState] = useState(false)
  const setDirty = useCallback((value: boolean) => {
    dirtySources.current.workspace = value
    setDirtyState(value || dirtySources.current.image || dirtySources.current.tasks)
  }, [])
  const setImageDirty = useCallback((value: boolean) => {
    dirtySources.current.image = value
    setDirtyState(value || dirtySources.current.workspace || dirtySources.current.tasks)
  }, [])
  const setTaskDirty = useCallback((id: string, value: boolean) => {
    if (value) taskSources.current.add(id)
    else taskSources.current.delete(id)
    dirtySources.current.tasks = taskSources.current.size > 0
    setDirtyState(dirtySources.current.workspace || dirtySources.current.image || dirtySources.current.tasks)
  }, [])
  const [pending, setPending] = useState<{action: () => void} | null>(null)
  const blocker = useBlocker(({currentLocation, nextLocation}) => Object.values(dirtySources.current).some(Boolean) && currentLocation.pathname !== nextLocation.pathname)
  const refreshSpaces = useCallback(() => setRevision(value => value + 1), [])
  const requestLeave = useCallback((action: () => void) => {
    if (Object.values(dirtySources.current).some(Boolean)) setPending({action})
    else action()
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void request<Space[]>('/spaces', {signal: controller.signal}).then(result => {
      if (controller.signal.aborted) return
      setSpaces(result)
      setSpaceID(previous => result.some(item => item.id === previous) ? previous : result[0]?.id ?? '')
      setError(''); setLoaded(true)
    }).catch(reason => { if (!controller.signal.aborted) {setError(errorMessage(reason, t('空间加载失败'))); setLoaded(true)} })
    return () => controller.abort()
    // Language changes must not reset workspace selection or drafts.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [revision])
  useEffect(() => {
    try { sessionStorage.setItem('studio.space', spaceID) } catch { /* Selection still works in memory. */ }
  }, [spaceID])
  useEffect(() => {
    if (!dirty) return
    const warn = (event: BeforeUnloadEvent) => {event.preventDefault(); event.returnValue = ''}
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])
  useEffect(() => {
    window.scrollTo(0, 0)
    document.getElementById('page-content')?.focus({preventScroll: true})
  }, [location.pathname])
  useEffect(() => {
    document.title = `${t(workspaceTitle(location.pathname))} · ${t('映创')}`
  }, [location.pathname, locale, t])

  const space = spaces.find(item => item.id === spaceID)
  const context: WorkspaceContext = {session, space, capability: provider.capability, capabilityError: provider.error, refreshCapability: provider.refresh, refreshSpaces, setDirty, setImageDirty, requestLeave}
  const isPersonalSettings = location.pathname === '/settings/preferences'
  const isOrganization = ['/settings/organization', '/settings/preferences'].includes(location.pathname)
  return <NavigationTaskContext.Provider value={setTaskDirty}><StudioShell session={session} spaces={spaces} space={spaceID} onSpaceChange={next => {
    if (next === spaceID) return
    requestLeave(() => {
      setDirty(false); setImageDirty(false); setSpaceID(next)
      if (location.pathname.startsWith('/projects/')) setTimeout(() => {void navigate('/projects')}, 0)
    })
  }} onLogout={() => requestLeave(logout)} capability={provider.capability} capabilityError={provider.error}>
    {error && !isPersonalSettings ? <div className="error space-error" role="alert">{t('无法加载工作空间：')}{error} <button onClick={refreshSpaces}>{t('重新加载空间')}</button></div>
      : !loaded ? <div className="loading-block" role="status">{t('正在加载工作空间…')}</div>
      : space || isOrganization ? <Outlet key={spaceID} context={context} />
      : <section className="page-empty"><h1>{t('没有可用组织')}</h1><p>{t('请创建组织或联系组织管理员。')}</p><Link to="/settings/organization">{t('组织与成员')}</Link></section>}
    {(pending || blocker.state === 'blocked') && <ConfirmDialog title={t('离开当前编辑？')} message={t('未保存的本地草稿将丢失。如果有请求正在提交或结果尚未确认，服务端可能已保存，离开不会撤销请求。')} confirmLabel={t('放弃本地草稿并继续')} onCancel={() => {setPending(null); if (blocker.state === 'blocked') blocker.reset()}} onConfirm={() => {
      setDirty(false)
      setImageDirty(false)
      taskSources.current.clear(); dirtySources.current.tasks = false; setDirtyState(false)
      if (pending) {const action = pending.action; setPending(null); action()}
      else if (blocker.state === 'blocked') blocker.proceed()
    }} />}
  </StudioShell></NavigationTaskContext.Provider>
}
