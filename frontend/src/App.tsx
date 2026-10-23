import { translate as t, useI18n } from "./i18n"
import { useEffect, useRef, useState } from 'react'
import { errorMessage, request, type Capabilities, type Space, type Task, type TaskInput, type Template } from './api'
import { AssetLibrary } from './components/AssetLibrary'
import { Composer } from './components/Composer'
import { Icon } from './components/Icon'
import { StudioShell } from './components/StudioShell'
import { TaskDetail } from './components/TaskDetail'
import { TaskList } from './components/TaskList'
import { TemplateGallery } from './components/TemplateGallery'
import { ProviderBanner } from './components/ProviderBanner'
import { useCapabilities } from './hooks/useCapabilities'
import { useWorkspace } from './hooks/useWorkspace'
import { DramaWorkspace } from './components/DramaWorkspace'
import { ConfirmDialog } from './components/ConfirmDialog'
import { AuthGate, type Session } from './components/AuthGate'
import { GovernancePanel } from './components/GovernancePanel'
import { ModelConfigPanel } from './components/ModelConfigPanel'
import { OrganizationPanel } from './components/OrganizationPanel'
import { RuntimeModelPicker } from './components/RuntimeModelPicker'
import { runtimeFields, runtimeReady, type RuntimeChoice } from './runtimeSelection'
import './drama.css'

const blankDraft: TaskInput = {prompt: '', kind: 'poster', ratio: '4:5', style: 'natural'}

export default function App() {
  useI18n()
 return <AuthGate>{(session, logout) => <AuthenticatedApp session={session} logout={logout} />}</AuthGate> }
function AuthenticatedApp({session, logout}: {session: Session; logout: () => void}) {
  useI18n()

  const provider = useCapabilities()
  const [space, setSpace] = useState('')
  const [spaces, setSpaces] = useState<Space[]>([])
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [workbench, setWorkbench] = useState<'image' | 'drama'>('image')
  const [dramaDirty, setDramaDirty] = useState(false)
  const [pendingLeave, setPendingLeave] = useState<{action: () => void} | null>(null)
  function requestLeave(action: () => void) {
    if (dramaDirty) setPendingLeave({action})
    else action()
  }
  useEffect(() => {
    const controller = new AbortController()
    void request<Space[]>('/spaces', {signal: controller.signal})
      .then(result => {
        if (controller.signal.aborted) return
        setSpaces(result); setSpace(previous => result.some(item => item.id === previous) ? previous : result[0]?.id ?? '')
        setError(result.length ? '' : t("没有可用组织，请创建组织或联系组织管理员。"))
      })
      .catch(reason => {if (!controller.signal.aborted) setError(errorMessage(reason, t("空间加载失败")))})
    return () => controller.abort()
  }, [revision])
  return <StudioShell session={session} onLogout={() => requestLeave(logout)} spaces={spaces} space={space} workbench={workbench} onWorkbenchChange={next => {if (next !== workbench) requestLeave(() => setWorkbench(next))}} capability={provider.capability} capabilityError={provider.error} onSpaceChange={next => {if (next !== space) requestLeave(() => setSpace(next))}}>
    {session.mode === 'secure' && <OrganizationPanel userID={session.user?.id} space={spaces.find(item => item.id === space)} onChange={() => setRevision(value => value + 1)} />}
    {session.mode === 'secure' && spaces.filter(item => item.id === space).map(item => <div key={item.id}><GovernancePanel space={item} />{(item.role === 'owner' || item.role === 'admin') && <ModelConfigPanel space={item} />}</div>)}
    {error ? <div className="error space-error" role="alert">{t("无法加载工作空间：")}{error} <button onClick={() => setRevision(value => value + 1)}>{t("重新加载空间")}</button></div> : space ? <><div hidden={workbench !== 'image'}><Workspace key={space} space={space} capability={provider.capability} capabilityError={provider.error} onCapabilityRefresh={provider.refresh} /></div>{workbench === 'drama' && <DramaWorkspace key={space} space={space} onDirtyChange={setDramaDirty} requestLeave={requestLeave} />}</> : <div className="loading-block" role="status">{t("正在加载工作空间…")}</div>}
    {pendingLeave && <ConfirmDialog title={t("离开当前编辑？")} message={t("未保存的本地草稿将丢失。如果有请求正在提交或结果尚未确认，服务端可能已保存，离开不会撤销请求。")} confirmLabel={t("放弃本地草稿并继续")} onCancel={() => setPendingLeave(null)} onConfirm={() => {const action = pendingLeave.action; setPendingLeave(null); action()}} />}
  </StudioShell>
}

function Workspace({space, capability, capabilityError, onCapabilityRefresh}: {space: string; capability: Capabilities | null; capabilityError: string; onCapabilityRefresh: () => void}) {
  useI18n()

  const studio = useWorkspace(space)
  const [runtimeChoice, setRuntimeChoice] = useState<RuntimeChoice | null>(null)
  const effectiveCapability = runtimeChoice ? {provider: runtimeChoice.model.provider as Capabilities['provider'], model: runtimeChoice.model.model, mode: 'real' as const, ready: runtimeReady(runtimeChoice), message: t('模型调用可能计费。取消、超时或结果未知不代表免费；费用按原币种预留，不自动兑换或重试。')} : capability
  const [tab, setTab] = useState<'create' | 'assets'>('create')
  const [draft, setDraft] = useState<TaskInput>(blankDraft)
  const [editing, setEditing] = useState<{task: Task; previous: TaskInput} | null>(null)
  const [detailId, setDetailId] = useState<string | null>(null)
  const promptRef = useRef<HTMLTextAreaElement>(null)
  const savedIds = new Set(studio.assets.map(asset => asset.task_id))
  const running = studio.tasks.filter(task => task.status === 'queued' || task.status === 'running').length

  function focusPrompt() {requestAnimationFrame(() => promptRef.current?.focus())}
  function chooseTemplate(template: Template) {
    studio.newAttempt(); setEditing(null)
    setDraft({prompt: template.prompt, kind: template.kind, ratio: template.ratio, style: template.style})
    focusPrompt()
  }
  function editTask(task: Task) {
    studio.newAttempt(); setDetailId(null); setTab('create')
    setEditing({task, previous: editing?.previous ?? draft})
    setDraft({prompt: task.prompt, kind: task.kind, ratio: task.ratio, style: task.style})
    focusPrompt()
  }
  function cancelEdit() {
    if (editing) setDraft(editing.previous)
    studio.newAttempt(); setEditing(null); focusPrompt()
  }
  function startNew() {studio.newAttempt(); setEditing(null); setDraft(blankDraft); setTab('create'); focusPrompt()}
  function openDetail(id: string) {studio.clearFeedback(); setDetailId(id)}
  async function submitDraft() {
    if (!effectiveCapability?.ready || effectiveCapability.mode === 'unavailable') return
    const outcome = await studio.create({...draft, ...runtimeFields(runtimeChoice)}, effectiveCapability, editing?.task.id)
    if (outcome === 'provider_changed') onCapabilityRefresh()
    if (outcome === 'created' && editing) {setEditing(null); setDraft(blankDraft)}
  }

  return <>
    <section className="intro"><div><div className="eyebrow">A SPACE FOR YOUR NEXT IDEA</div><h1>{t("把灵感，变成画面")}<span>。</span></h1><p>{t("一点想法，一些探索。让下一张好画面，从这里开始。")}</p></div><div className="intro-count"><strong>{String(studio.tasks.length).padStart(2, '0')}</strong><span>{t("次创作探索")}{running > 0 && <small>{running} {t("个任务进行中")}</small>}</span></div></section>
    <ProviderBanner capability={capability} error={capabilityError} onRefresh={onCapabilityRefresh} />
    <nav className="tabs" aria-label={t("工作室功能")}><button type="button" aria-current={tab === 'create' ? 'page' : undefined} onClick={() => setTab('create')}><Icon name="spark" />{t("图像创作")}</button><button type="button" aria-current={tab === 'assets' ? 'page' : undefined} onClick={() => setTab('assets')}><Icon name="grid" />{t("素材库")}<span className="count">{studio.assets.length}</span></button></nav>
    {studio.connectionError && <div role="alert" className="error">{t("无法同步服务：")}{studio.connectionError} <button onClick={studio.reconnect}>{t("重新连接")}</button></div>}
    {studio.actionError && !detailId && <div role="alert" className="error">{studio.actionError}</div>}
    {studio.notice && !detailId && <p role="status" className="notice"><Icon name="check" />{studio.notice}</p>}
    {tab === 'create' ? <>
      <TemplateGallery catalog={studio.catalog} error={studio.catalogError} disabled={studio.busy} onChoose={chooseTemplate} onRetry={studio.reconnect} />
      <RuntimeModelPicker space={space} capability="image" value={runtimeChoice} onChange={setRuntimeChoice} disabled={studio.busy} />
      <div className="creation-grid"><Composer catalog={studio.catalogError ? null : studio.catalog} capability={effectiveCapability} draft={draft} busy={studio.busy} retryTask={editing?.task ?? null} promptRef={promptRef} onChange={setDraft} onSubmit={() => {void submitDraft()}} onCancelEdit={cancelEdit} onNew={startNew} /><TaskList tasks={studio.tasks} catalog={studio.catalog} loading={studio.loading} error={studio.connectionError} savedIds={savedIds} saving={studio.saving} busy={studio.busy} onSave={task => {void studio.save(task)}} onDetail={task => openDetail(task.id)} onEdit={editTask} onResume={studio.resume} /></div>
    </> : <AssetLibrary space={space} catalog={studio.catalog} total={studio.assets.length} onCreate={() => setTab('create')} onDetail={openDetail} />}
    {detailId && <TaskDetail key={detailId} id={detailId} space={space} catalog={studio.catalog} saved={savedIds.has(detailId)} saving={studio.saving} busy={studio.busy} actionError={studio.actionError} notice={studio.notice} onClose={() => setDetailId(null)} onUpdate={studio.updateTask} onSave={task => {void studio.save(task)}} onEdit={editTask} onResume={studio.resume} />}
  </>
}
