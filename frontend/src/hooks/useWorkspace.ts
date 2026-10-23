import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, errorMessage, request, type Asset, type Capabilities, type Catalog, type Task, type TaskInput } from '../api'

export function useWorkspace(space: string) {
  const [catalog, setCatalog] = useState<Catalog | null>(null)
  const [catalogError, setCatalogError] = useState('')
  const [tasks, setTasks] = useState<Task[]>([])
  const [assets, setAssets] = useState<Asset[]>([])
  const [loading, setLoading] = useState(true)
  const [connectionError, setConnectionError] = useState('')
  const [actionError, setActionError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [saving, setSaving] = useState<string | null>(null)
  const [revision, setRevision] = useState(0)
  const query = `?${new URLSearchParams({space_id: space})}`
  const active = useRef(true)
  const pending = useRef(new Set<AbortController>())
  const inFlight = useRef(false)
  const saveInFlight = useRef(false)
  const mutation = useRef(0)
  const resumeAttempts = useRef(new Map<string, string>())
  const attempt = useRef<{fingerprint: string; key: string} | null>(null)

  useEffect(() => {
    active.current = true
    const controllers = pending.current
    return () => { active.current = false; controllers.forEach(controller => controller.abort()) }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void request<Catalog>('/templates', {signal: controller.signal})
      .then(value => {if (!controller.signal.aborted) {setCatalog(value); setCatalogError('')}})
      .catch(error => {if (!controller.signal.aborted) setCatalogError(errorMessage(error, '模板加载失败'))})
    return () => controller.abort()
  }, [revision])

  useEffect(() => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    async function load() {
      const version = mutation.current
      try {
        const [newTasks, newAssets] = await Promise.all([
          request<Task[]>(`/tasks${query}`, {signal: controller.signal}),
          request<Asset[]>(`/assets${query}`, {signal: controller.signal}),
        ])
        if (!controller.signal.aborted && mutation.current === version) {
          setTasks(newTasks); setAssets(newAssets); setConnectionError('')
        }
      } catch (error) {
        if (!controller.signal.aborted) setConnectionError(errorMessage(error, '服务连接失败'))
      } finally {
        if (!controller.signal.aborted) {setLoading(false); timer = setTimeout(() => {void load()}, 1500)}
      }
    }
    void load()
    return () => {controller.abort(); clearTimeout(timer)}
  }, [query, revision])

  const clearFeedback = useCallback(() => {setActionError(''); setNotice('')}, [])
  const newAttempt = useCallback(() => {attempt.current = null; clearFeedback()}, [clearFeedback])
  const updateTask = useCallback((task: Task) => {
    if (active.current) setTasks(previous => previous.map(item => item.id === task.id ? task : item))
  }, [])

  function submission(input: TaskInput, retryOf?: string) {
    const body = JSON.stringify({...input, prompt: input.prompt.trim()})
    const path = retryOf ? `/tasks/${encodeURIComponent(retryOf)}/retry${query}` : `/tasks${query}`
    const fingerprint = `${path}:${body}`
    return {body, path, fingerprint}
  }

  async function create(input: TaskInput, capability: Capabilities, retryOf?: string): Promise<'created' | 'provider_changed' | 'failed'> {
    if (inFlight.current || !capability.ready || capability.provider === 'unavailable' || capability.mode === 'unavailable') return 'failed'
    const {body, path, fingerprint} = submission(input, retryOf)
    if (attempt.current?.fingerprint !== fingerprint) attempt.current = {fingerprint, key: crypto.randomUUID()}
    const controller = new AbortController()
    pending.current.add(controller)
    inFlight.current = true; setBusy(true); clearFeedback()
    try {
      const task = await request<Task>(path, {
        method: 'POST', signal: controller.signal,
        headers: {'Content-Type': 'application/json', 'Idempotency-Key': attempt.current.key},
        body: JSON.stringify({...JSON.parse(body), expected_provider: capability.provider, expected_model: capability.mode === 'mock' ? '' : capability.model ?? ''}),
      })
      if (!active.current || controller.signal.aborted) return 'failed'
      mutation.current += 1
      setTasks(previous => [task, ...previous.filter(item => item.id !== task.id)])
      setNotice(retryOf ? '重试任务已提交，原任务保留。相同提交不会重复创建。' : '任务已提交。相同设定重复点击会返回同一任务；可点击“开始新创作”另建任务。')
      return 'created'
    } catch (error) {
      if (active.current && !controller.signal.aborted && error instanceof ApiError && error.code === 'provider_changed') {
        setActionError('图像服务配置已变化。本次未创建任务；请查看更新后的服务与费用提示，再次点击提交。')
        return 'provider_changed'
      }
      if (active.current && !controller.signal.aborted) setActionError(errorMessage(error, '提交失败，请重试'))
      return 'failed'
    } finally {
      pending.current.delete(controller)
      inFlight.current = false
      if (active.current && !controller.signal.aborted) setBusy(false)
    }
  }

  async function resume(task: Task) {
    if (inFlight.current || !task.upstream_id || task.status !== 'failed') return
    const key = resumeAttempts.current.get(task.id) ?? crypto.randomUUID()
    resumeAttempts.current.set(task.id, key)
    const controller = new AbortController(); pending.current.add(controller)
    inFlight.current = true; setBusy(true); clearFeedback()
    try {
      const result = await request<Task>(`/tasks/${encodeURIComponent(task.id)}/resume${query}`, {method: 'POST', signal: controller.signal, headers: {'Content-Type': 'application/json', 'Idempotency-Key': key}, body: '{}'})
      if (!active.current || controller.signal.aborted) return
      resumeAttempts.current.delete(task.id); mutation.current += 1
      updateTask(result); setNotice('继续查询原上游任务，不重新提交图像生成。')
    } catch (error) {
      if (active.current && !controller.signal.aborted) setActionError(`继续查询未确认：${errorMessage(error, '请重试')}。再次点击将复用同一请求标识。`)
    } finally {pending.current.delete(controller); inFlight.current = false; if (active.current) setBusy(false)}
  }

  async function save(task: Task) {
    if (saveInFlight.current) return
    const controller = new AbortController()
    pending.current.add(controller)
    saveInFlight.current = true; setSaving(task.id); clearFeedback()
    try {
      const asset = await request<Asset>(`/assets${query}`, {
        method: 'POST', signal: controller.signal, headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({task_id: task.id}),
      })
      if (!active.current || controller.signal.aborted) return
      mutation.current += 1
      setAssets(previous => [asset, ...previous.filter(item => item.id !== asset.id)])
      setNotice('已保存到当前空间的素材库。')
    } catch (error) {
      if (active.current && !controller.signal.aborted) setActionError(errorMessage(error, '保存失败，请重试'))
    } finally {
      pending.current.delete(controller)
      saveInFlight.current = false
      if (active.current && !controller.signal.aborted) setSaving(null)
    }
  }

  return {catalog, catalogError, tasks, assets, loading, connectionError, actionError, notice, busy, saving,
    create, resume, save, newAttempt, clearFeedback, updateTask, reconnect: () => setRevision(value => value + 1)}
}
