import { runtimeFields, runtimeReady, type RuntimeChoice } from '../runtimeSelection'
import { translate } from '../i18n'
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, errorMessage, request } from '../api'
import { generationActive, projectDraftError, scriptDraftError, storyboardDraftError, type StoryboardShot, type StoryboardGeneration, type DramaProject, type DramaScript, type ProjectDraft, type ScriptGeneration } from '../drama'

type Operation = 'create' | 'brief' | 'script' | 'generate' | 'storyboard' | 'generateStoryboard'
interface Attempt { operation: Operation; path: string; body: string; key: string }
const emptyDraft: ProjectDraft = {title: '', brief: ''}

export function useDramaProject(space: string, id: string | null, onProject: (project: DramaProject) => void, expectedProvider?: string, expectedModel?: string, runtimeChoice: RuntimeChoice | null = null, storyboardChoice: RuntimeChoice | null = null) {
  const [project, setProject] = useState<DramaProject | null>(null)
  const [baseline, setBaseline] = useState<DramaProject | null>(null)
  const [draft, setDraft] = useState<ProjectDraft>(emptyDraft)
  const [script, setScript] = useState<DramaScript | null>(null)
  const [editingScript, setEditingScript] = useState(false)
  const [shots, setShots] = useState<StoryboardShot[]>([])
  const [editingStoryboard, setEditingStoryboard] = useState(false)
  const [loading, setLoading] = useState(Boolean(id))
  const [loadError, setLoadError] = useState('')
  const [actionError, setActionError] = useState('')
  const [conflict, setConflict] = useState(false)
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState<Operation | null>(null)
  const [uncertain, setUncertain] = useState(false)
  const [revision, setRevision] = useState(0)
  const [historyRevision, setHistoryRevision] = useState(0)
  const mounted = useRef(true)
  const controllers = useRef(new Set<AbortController>())
  const inFlight = useRef(false)
  const attempt = useRef<Attempt | null>(null)
  const replaceOnLoad = useRef(false)
  const requestEpoch = useRef(0)
  const latestVersion = useRef(0)
  const briefDirty = draft.title !== (baseline?.title ?? '') || draft.brief !== (baseline?.brief ?? '')
  const scriptDirty = editingScript && JSON.stringify(script) !== JSON.stringify(baseline?.script ?? null)
  const storyboardDirty = editingStoryboard && JSON.stringify(shots) !== JSON.stringify(baseline?.storyboard?.shots ?? [])
  const dirty = briefDirty || scriptDirty || storyboardDirty || uncertain || busy !== null
  const dirtyRef = useRef(false)
  useEffect(() => {dirtyRef.current = briefDirty || scriptDirty || storyboardDirty || uncertain || busy !== null}, [briefDirty, scriptDirty, storyboardDirty, uncertain, busy])
  useEffect(() => {
    mounted.current = true
    const pending = controllers.current
    return () => {mounted.current = false; for (const controller of pending) controller.abort()}
  }, [])

  const acceptProject = useCallback((result: DramaProject, replace: boolean) => {
    if (result.version < latestVersion.current) return
    latestVersion.current = result.version
    setProject(result); onProject(result)
    if (replace || !dirtyRef.current) {
      setBaseline(result); setDraft({title: result.title, brief: result.brief}); setScript(result.script ?? null); setShots(result.storyboard?.shots ?? [])
      if (replace) {setEditingScript(false); setEditingStoryboard(false); setConflict(false); setActionError('')}
    }
  }, [onProject])

  useEffect(() => {
    if (!id) return
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    async function load() {
      const epoch = requestEpoch.current
      try {
        const result = await request<DramaProject>(`/projects/${encodeURIComponent(id!)}?${new URLSearchParams({space_id: space})}`, {signal: controller.signal})
        if (controller.signal.aborted || epoch !== requestEpoch.current) return
        acceptProject(result, replaceOnLoad.current); replaceOnLoad.current = false
        setLoading(false); setLoadError('')
        if (generationActive(result)) timer = setTimeout(() => {void load()}, 1200)
        else setHistoryRevision(value => value + 1)
      } catch (failure) {
        if (controller.signal.aborted) return
        setLoadError(errorMessage(failure, translate("项目读取失败"))); setLoading(false)
      }
    }
    void load()
    return () => {controller.abort(); clearTimeout(timer)}
  }, [space, id, revision, acceptProject])

  async function execute(current: Attempt) {
    if (inFlight.current) return
    requestEpoch.current++
    inFlight.current = true; setBusy(current.operation); setActionError(''); setNotice('')
    const controller = new AbortController(); controllers.current.add(controller)
    try {
      const result = await request<DramaProject | ScriptGeneration | StoryboardGeneration>(current.path, {
        method: current.operation === 'create' || current.operation === 'generate' || current.operation === 'generateStoryboard' ? 'POST' : 'PUT',
        headers: {'Content-Type': 'application/json', 'Idempotency-Key': current.key}, body: current.body, signal: controller.signal,
      })
      if (!mounted.current || controller.signal.aborted) return
      setUncertain(false); attempt.current = null; setConflict(false)
      if (current.operation === 'generate' || current.operation === 'generateStoryboard') {
        setProject(previous => previous ? current.operation === 'generate' ? {...previous, latest_generation: result as ScriptGeneration} : {...previous, latest_storyboard_generation: result as StoryboardGeneration} : previous)
        setNotice(translate("生成请求已保存，状态以生成记录为准。离开工作台不会取消已接受的任务。"))
        setRevision(value => value + 1); setHistoryRevision(value => value + 1)
      } else {
        acceptProject(result as DramaProject, true)
        setNotice(current.operation === 'create' ? translate("项目已创建并保存。") : current.operation === 'brief' ? translate("项目简报已保存。") : current.operation === 'storyboard' ? translate("分镜修改已保存。") : translate("剧本修改已保存。"))
      }
    } catch (failure) {
      if (!mounted.current || controller.signal.aborted) return
      const unknown = !(failure instanceof ApiError) || failure.status >= 500
      setUncertain(unknown)
      setConflict(failure instanceof ApiError && ['version_conflict', 'generation_active', 'script_missing', 'storyboard_missing', 'idempotency_conflict'].includes(failure.code ?? ''))
      setActionError(unknown ? translate("提交结果尚未确认，服务端可能已保存。重试将使用原请求与同一幂等标识：若此前已接受则返回原操作，尚未接受则尝试执行。") : errorMessage(failure, translate("操作失败")))
    } finally {
      controllers.current.delete(controller); inFlight.current = false
      if (mounted.current) setBusy(null)
    }
  }

  function submit(operation: Operation) {
    if (inFlight.current || uncertain || generationActive(project) || loading || conflict) return
    const error = operation === 'create' || operation === 'brief' ? projectDraftError(draft) : operation === 'script' && script ? scriptDraftError(script) : operation === 'storyboard' ? storyboardDraftError(shots) : ''
    if (error) {setActionError(error); return}
    if ((operation === 'generate' || operation === 'generateStoryboard') && (briefDirty || scriptDirty || editingScript || editingStoryboard)) return
    if (operation === 'generateStoryboard' && !project?.script) return
    const selected = operation === 'generateStoryboard' ? storyboardChoice : runtimeChoice
    const preview = {operation: operation === 'generateStoryboard' ? 'storyboard' as const : 'script' as const, project_id: id ?? '', input: {expected_version: baseline?.version}}
    if ((operation === 'generate' || operation === 'generateStoryboard') && !runtimeReady(selected, preview)) return
    const query = new URLSearchParams({space_id: space})
    const projectPath = `/projects/${encodeURIComponent(id ?? '')}`
    const path = `${operation === 'create' ? '/projects' : operation === 'generate' ? `${projectPath}/generations` : operation === 'script' ? `${projectPath}/script` : operation === 'storyboard' ? `${projectPath}/storyboard` : operation === 'generateStoryboard' ? `${projectPath}/storyboard-generations` : projectPath}?${query}`
    const body = JSON.stringify(operation === 'create' ? {title: draft.title.trim(), brief: draft.brief.trim()} : operation === 'brief' ? {title: draft.title.trim(), brief: draft.brief.trim(), expected_version: baseline?.version} : operation === 'script' ? {script, expected_version: baseline?.version} : operation === 'storyboard' ? {shots, expected_version: baseline?.version} : {expected_version: baseline?.version, ...(selected ? {...runtimeFields(selected), expected_provider: selected.model.provider, expected_model: selected.model.model} : expectedProvider ? {expected_provider: expectedProvider, expected_model: expectedModel ?? ''} : {})})
    if (attempt.current?.path !== path || attempt.current.body !== body) attempt.current = {operation, path, body, key: crypto.randomUUID()}
    void execute(attempt.current)
  }

  function reload(discard = false) {
    replaceOnLoad.current = discard; setLoading(true); setLoadError('')
    if (discard) {attempt.current = null; setUncertain(false)}
    setRevision(value => value + 1)
  }
  function cancelScript() {setScript(baseline?.script ?? null); setEditingScript(false); setActionError('')}
  function cancelStoryboard() {setShots(baseline?.storyboard?.shots ?? []); setEditingStoryboard(false); setActionError('')}
  function cancelBrief() {setDraft({title: baseline?.title ?? '', brief: baseline?.brief ?? ''}); setActionError('')}

  return {shots, setShots, editingStoryboard, setEditingStoryboard, storyboardDirty, cancelStoryboard, project, baseline, draft, setDraft, script, setScript, editingScript, setEditingScript, loading, loadError, actionError, conflict, notice, busy, uncertain, dirty, briefDirty, scriptDirty, historyRevision, submit, reload, cancelScript, cancelBrief,
    retry: () => {if (attempt.current) void execute(attempt.current)}, locked: Boolean(busy) || uncertain || generationActive(project) || loading || conflict}
}
