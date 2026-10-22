import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, errorMessage, request } from '../api'
import { generationActive, projectDraftError, scriptDraftError, type DramaProject, type DramaScript, type ProjectDraft, type ScriptGeneration } from '../drama'

type Operation = 'create' | 'brief' | 'script' | 'generate'
interface Attempt { operation: Operation; path: string; body: string; key: string }
const emptyDraft: ProjectDraft = {title: '', brief: ''}

export function useDramaProject(space: string, id: string | null, onProject: (project: DramaProject) => void) {
  const [project, setProject] = useState<DramaProject | null>(null)
  const [baseline, setBaseline] = useState<DramaProject | null>(null)
  const [draft, setDraft] = useState<ProjectDraft>(emptyDraft)
  const [script, setScript] = useState<DramaScript | null>(null)
  const [editingScript, setEditingScript] = useState(false)
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
  const dirty = briefDirty || scriptDirty || uncertain || busy !== null
  const dirtyRef = useRef(false)
  useEffect(() => {dirtyRef.current = briefDirty || scriptDirty}, [briefDirty, scriptDirty])
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
      setBaseline(result); setDraft({title: result.title, brief: result.brief}); setScript(result.script ?? null)
      if (replace) {setEditingScript(false); setConflict(false); setActionError('')}
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
        setLoadError(errorMessage(failure, '项目读取失败')); setLoading(false)
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
      const result = await request<DramaProject | ScriptGeneration>(current.path, {
        method: current.operation === 'create' || current.operation === 'generate' ? 'POST' : 'PUT',
        headers: {'Content-Type': 'application/json', 'Idempotency-Key': current.key}, body: current.body, signal: controller.signal,
      })
      if (!mounted.current || controller.signal.aborted) return
      setUncertain(false); attempt.current = null; setConflict(false)
      if (current.operation === 'generate') {
        const generation = result as ScriptGeneration
        setProject(previous => previous ? {...previous, latest_generation: generation} : previous)
        setNotice('生成请求已保存，状态以生成记录为准。离开工作台不会取消已接受的任务。')
        setRevision(value => value + 1); setHistoryRevision(value => value + 1)
      } else {
        acceptProject(result as DramaProject, true)
        setNotice(current.operation === 'create' ? '项目已创建并保存。' : current.operation === 'brief' ? '项目简报已保存。' : '剧本修改已保存。')
      }
    } catch (failure) {
      if (!mounted.current || controller.signal.aborted) return
      const unknown = !(failure instanceof ApiError) || failure.status >= 500
      setUncertain(unknown)
      setConflict(failure instanceof ApiError && ['version_conflict', 'generation_active', 'script_missing', 'idempotency_conflict'].includes(failure.code ?? ''))
      setActionError(unknown ? '提交结果尚未确认，服务端可能已保存。重试将使用原请求与同一幂等标识：若此前已接受则返回原操作，尚未接受则尝试执行。' : errorMessage(failure, '操作失败'))
    } finally {
      controllers.current.delete(controller); inFlight.current = false
      if (mounted.current) setBusy(null)
    }
  }

  function submit(operation: Operation) {
    if (inFlight.current || uncertain || generationActive(project) || loading || conflict) return
    const error = operation === 'create' || operation === 'brief' ? projectDraftError(draft) : operation === 'script' && script ? scriptDraftError(script) : ''
    if (error) {setActionError(error); return}
    if (operation === 'generate' && (briefDirty || scriptDirty || editingScript)) return
    const query = new URLSearchParams({space_id: space})
    const projectPath = `/projects/${encodeURIComponent(id ?? '')}`
    const path = `${operation === 'create' ? '/projects' : operation === 'generate' ? `${projectPath}/generations` : operation === 'script' ? `${projectPath}/script` : projectPath}?${query}`
    const body = JSON.stringify(operation === 'create' ? {title: draft.title.trim(), brief: draft.brief.trim()} : operation === 'brief' ? {title: draft.title.trim(), brief: draft.brief.trim(), expected_version: baseline?.version} : operation === 'script' ? {script, expected_version: baseline?.version} : {expected_version: baseline?.version})
    if (attempt.current?.path !== path || attempt.current.body !== body) attempt.current = {operation, path, body, key: crypto.randomUUID()}
    void execute(attempt.current)
  }

  function reload(discard = false) {
    replaceOnLoad.current = discard; setLoading(true); setLoadError('')
    if (discard) {attempt.current = null; setUncertain(false)}
    setRevision(value => value + 1)
  }
  function cancelScript() {setScript(baseline?.script ?? null); setEditingScript(false); setActionError('')}
  function cancelBrief() {setDraft({title: baseline?.title ?? '', brief: baseline?.brief ?? ''}); setActionError('')}

  return {project, baseline, draft, setDraft, script, setScript, editingScript, setEditingScript, loading, loadError, actionError, conflict, notice, busy, uncertain, dirty, briefDirty, scriptDirty, historyRevision, submit, reload, cancelScript, cancelBrief,
    retry: () => {if (attempt.current) void execute(attempt.current)}, locked: Boolean(busy) || uncertain || generationActive(project) || loading || conflict}
}
