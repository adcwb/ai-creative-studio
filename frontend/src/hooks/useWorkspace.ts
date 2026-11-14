import {usePolling} from './usePolling'
import {creationPath} from '../creationGateway'
import { translate } from '../i18n'
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, errorMessage, request, type Asset, type Capabilities, type Catalog, type Task, type TaskInput } from '../api'

export function useWorkspace(space: string, reading = true) {
  const [catalog, setCatalog] = useState<Catalog | null>(null)
  const [catalogError, setCatalogError] = useState('')
  const [tasks, setTasks] = useState<Task[]>([])
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const [running, setRunning] = useState(0)
  const pageSize = 4
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
    if (!reading) return
    const controller = new AbortController()
    void request<Catalog>('/templates', {signal: controller.signal})
      .then(value => {if (!controller.signal.aborted) {setCatalog(value); setCatalogError('')}})
      .catch(error => {if (!controller.signal.aborted) setCatalogError(errorMessage(error, translate("模板加载失败")))})
    return () => controller.abort()
  }, [revision, reading])

  usePolling(async signal => {
    const version = mutation.current
    try {
      const [newTasks, newAssets] = await Promise.all([
        request<{items: Task[]; total: number; page: number; running: number}>(`/tasks${query}&page=${page}&page_size=${pageSize}`, {signal}),
        request<Asset[]>(`/assets${query}`, {signal}),
      ])
      if (!signal.aborted && mutation.current === version) {
        setTasks(newTasks.items); setTotal(newTasks.total); setPage(newTasks.page); setRunning(newTasks.running); setAssets(newAssets); setConnectionError('')
      }
    } catch (error) {
      if (!signal.aborted) setConnectionError(errorMessage(error, translate('服务连接失败')))
      throw error
    } finally {if (!signal.aborted) setLoading(false)}
  }, 1500, `${query}:${revision}:${page}`, reading && !!space)

  const clearFeedback = useCallback(() => {setActionError(''); setNotice('')}, [])
  const newAttempt = useCallback(() => {attempt.current = null; clearFeedback()}, [clearFeedback])
  const updateTask = useCallback((task: Task) => {
    if (active.current) setTasks(previous => previous.map(item => item.id === task.id ? task : item))
  }, [])

  function submission(input: TaskInput, retryOf?: string) {
    const source = input.prompt_source
    const body = JSON.stringify({...input, prompt: input.prompt.trim(), ...(source ? {prompt_source: {template_id: source.template_id, version_id: source.version_id, edited: source.edited, ...(source.variables ? {variables: source.variables} : {})}} : {})})
    const path = creationPath(retryOf ? `/tasks/${encodeURIComponent(retryOf)}/retry${query}` : `/tasks${query}`)
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
      await request<Task>(path, {
        method: 'POST', signal: controller.signal,
        headers: {'Content-Type': 'application/json', 'Idempotency-Key': attempt.current.key},
        body: JSON.stringify({...JSON.parse(body), expected_provider: capability.provider, expected_model: capability.mode === 'mock' ? '' : capability.model ?? ''}),
      })
      if (!active.current || controller.signal.aborted) return 'failed'
      mutation.current += 1
      setLoading(true); setPage(1); setRevision(value => value + 1)
      setNotice(retryOf ? translate("重试任务已提交，原任务保留。相同提交不会重复创建。") : translate("任务已提交。相同设定重复点击会返回同一任务；可点击“开始新创作”另建任务。"))
      return 'created'
    } catch (error) {
      if (active.current && !controller.signal.aborted && error instanceof ApiError && error.code === 'provider_changed') {
        setActionError(translate("图像服务配置已变化。本次未创建任务；请查看更新后的服务与费用提示，再次点击提交。"))
        return 'provider_changed'
      }
      if (active.current && !controller.signal.aborted) setActionError(errorMessage(error, translate("提交失败，请重试")))
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
      updateTask(result); setNotice(translate("继续查询原上游任务，不重新提交图像生成。"))
    } catch (error) {
      if (active.current && !controller.signal.aborted) setActionError(translate("继续查询未确认：{p0}。再次点击将复用同一请求标识。", {p0: errorMessage(error, '请重试')}))
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
      setNotice(translate("已保存到当前空间的素材库。"))
    } catch (error) {
      if (active.current && !controller.signal.aborted) setActionError(errorMessage(error, translate("保存失败，请重试")))
    } finally {
      pending.current.delete(controller)
      saveInFlight.current = false
      if (active.current && !controller.signal.aborted) setSaving(null)
    }
  }

  function changePage(next: number) {setLoading(true); setPage(next)}
  async function deleteHistory(id?: string) {
    if (inFlight.current || saveInFlight.current) return
    const controller = new AbortController()
    pending.current.add(controller)
    inFlight.current = true; setBusy(true); clearFeedback(); mutation.current += 1
    try {
      await request<{deleted: number}>(`/tasks${id ? '/' + encodeURIComponent(id) : ''}/history${query}`, {method: 'DELETE', signal: controller.signal})
      if (active.current && !controller.signal.aborted) setNotice(translate(id ? '创作记录已删除。' : '已清空已结束的创作记录，进行中的任务已保留。'))
    } catch (error) {
      if (active.current && !controller.signal.aborted) setActionError(errorMessage(error, translate('未能确认删除结果，请刷新创作记录。')))
      throw error
    } finally {
      pending.current.delete(controller); inFlight.current = false
      if (active.current && !controller.signal.aborted) {setBusy(false); setRevision(value => value + 1)}
    }
  }
  return {catalog, catalogError, tasks, assets, loading, connectionError, actionError, notice, busy, saving, page, pageSize, total, running, changePage,
    create, resume, save, deleteHistory, newAttempt, clearFeedback, updateTask, reconnect: () => setRevision(value => value + 1)}
}
