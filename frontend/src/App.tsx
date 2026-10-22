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

const blankDraft: TaskInput = {prompt: '', kind: 'poster', ratio: '4:5', style: 'natural'}

export default function App() {
  const provider = useCapabilities()
  const [space, setSpace] = useState('')
  const [spaces, setSpaces] = useState<Space[]>([])
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    void request<Space[]>('/spaces', {signal: controller.signal})
      .then(result => {
        if (controller.signal.aborted) return
        setSpaces(result); setSpace(previous => result.some(item => item.id === previous) ? previous : result[0]?.id ?? '')
        setError(result.length ? '' : '服务未提供可用演示空间。')
      })
      .catch(reason => {if (!controller.signal.aborted) setError(errorMessage(reason, '空间加载失败'))})
    return () => controller.abort()
  }, [revision])
  return <StudioShell spaces={spaces} space={space} capability={provider.capability} capabilityError={provider.error} onSpaceChange={setSpace}>
    {error ? <div className="error space-error" role="alert">无法加载演示空间：{error} <button onClick={() => setRevision(value => value + 1)}>重新加载空间</button></div> : space ? <Workspace key={space} space={space} capability={provider.capability} capabilityError={provider.error} onCapabilityRefresh={provider.refresh} /> : <div className="loading-block" role="status">正在加载演示空间…</div>}
  </StudioShell>
}

function Workspace({space, capability, capabilityError, onCapabilityRefresh}: {space: string; capability: Capabilities | null; capabilityError: string; onCapabilityRefresh: () => void}) {
  const studio = useWorkspace(space)
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
    if (!capability?.ready || capability.mode === 'unavailable') return
    const outcome = await studio.create(draft, capability, editing?.task.id)
    if (outcome === 'provider_changed') onCapabilityRefresh()
    if (outcome === 'created' && editing) {setEditing(null); setDraft(blankDraft)}
  }

  return <>
    <section className="intro"><div><div className="eyebrow">A SPACE FOR YOUR NEXT IDEA</div><h1>把灵感，变成画面<span>。</span></h1><p>一点想法，一些探索。让下一张好画面，从这里开始。</p></div><div className="intro-count"><strong>{String(studio.tasks.length).padStart(2, '0')}</strong><span>次创作探索{running > 0 && <small>{running} 个任务进行中</small>}</span></div></section>
    <ProviderBanner capability={capability} error={capabilityError} onRefresh={onCapabilityRefresh} />
    <nav className="tabs" aria-label="工作室功能"><button type="button" aria-current={tab === 'create' ? 'page' : undefined} onClick={() => setTab('create')}><Icon name="spark" />图像创作</button><button type="button" aria-current={tab === 'assets' ? 'page' : undefined} onClick={() => setTab('assets')}><Icon name="grid" />素材库 <span className="count">{studio.assets.length}</span></button></nav>
    {studio.connectionError && <div role="alert" className="error">无法同步服务：{studio.connectionError} <button onClick={studio.reconnect}>重新连接</button></div>}
    {studio.actionError && !detailId && <div role="alert" className="error">{studio.actionError}</div>}
    {studio.notice && !detailId && <p role="status" className="notice"><Icon name="check" />{studio.notice}</p>}
    {tab === 'create' ? <>
      <TemplateGallery catalog={studio.catalog} error={studio.catalogError} disabled={studio.busy} onChoose={chooseTemplate} onRetry={studio.reconnect} />
      <div className="creation-grid"><Composer catalog={studio.catalogError ? null : studio.catalog} capability={capability} draft={draft} busy={studio.busy} retryTask={editing?.task ?? null} promptRef={promptRef} onChange={setDraft} onSubmit={() => {void submitDraft()}} onCancelEdit={cancelEdit} onNew={startNew} /><TaskList tasks={studio.tasks} catalog={studio.catalog} loading={studio.loading} error={studio.connectionError} savedIds={savedIds} saving={studio.saving} busy={studio.busy} onSave={task => {void studio.save(task)}} onDetail={task => openDetail(task.id)} onEdit={editTask} /></div>
    </> : <AssetLibrary space={space} catalog={studio.catalog} total={studio.assets.length} onCreate={() => setTab('create')} onDetail={openDetail} />}
    {detailId && <TaskDetail key={detailId} id={detailId} space={space} catalog={studio.catalog} saved={savedIds.has(detailId)} saving={studio.saving} busy={studio.busy} actionError={studio.actionError} notice={studio.notice} onClose={() => setDetailId(null)} onUpdate={studio.updateTask} onSave={task => {void studio.save(task)}} onEdit={editTask} />}
  </>
}
