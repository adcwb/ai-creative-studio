import { useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { baseURL, errorMessage, request, type Task } from '../../api'
import type { DramaProject } from '../../drama'
import { useI18n } from '../../i18n'
import { useNavigationTask } from '../../app/navigationGuard'

export function ContinueImage({task, space, onClose}: {task: Task; space: string; onClose: () => void}) {
  const {t} = useI18n()
  const navigate = useNavigate()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const storageKey = `image.continue:${space}:${task.id}`
  const [initial] = useState(() => {try {const saved = JSON.parse(sessionStorage.getItem(storageKey) ?? 'null') as {key?: string; id?: string} | null; if (saved?.key) return {key: saved.key, id: saved.id ?? ''}} catch { /* Optional recovery. */ } return {key: crypto.randomUUID(), id: ''}})
  const key = useRef(initial.key)
  const flight = useRef(false)
  const project = useRef(initial.id)
  useNavigationTask(busy)
  async function open() {
    if (flight.current || !task.result_url?.startsWith('/')) return
    flight.current = true; setBusy(true); setError('')
    try {sessionStorage.setItem(storageKey, JSON.stringify({key: key.current, id: project.current}))} catch { /* In-memory recovery. */ }
    try {
      const response = await fetch(`${baseURL}${task.result_url}`, {credentials: 'include'})
      if (!response.ok) throw new Error(t('原图读取失败'))
      const blob = await response.blob()
      if (!['image/png', 'image/jpeg'].includes(blob.type) || blob.size > 32 * 1024 * 1024) throw new Error(t('继续编辑需要32MiB以内的PNG或JPEG原图。'))
      const query = new URLSearchParams({space_id: space})
      if (!project.current) project.current = (await request<DramaProject>(`/projects?${query}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': key.current}, body: JSON.stringify({title: task.prompt.slice(0, 60), brief: task.prompt, kind: 'image'})})).id
      try {sessionStorage.setItem(storageKey, JSON.stringify({key: key.current, id: project.current}))} catch { /* In-memory recovery. */ }
      query.set('filename', `source-${task.id}.${blob.type === 'image/png' ? 'png' : 'jpg'}`); query.set('role', 'reference')
      await request(`/projects/${project.current}/media/uploads?${query}`, {method: 'POST', headers: {'Content-Type': blob.type, 'Idempotency-Key': key.current + '-source'}, body: blob})
      const id = project.current
      onClose()
      setTimeout(() => {void navigate(`/studio/workspaces/${id}`)}, 0)
    } catch (e) {setError(errorMessage(e, t('继续编辑失败')))}
    finally {flight.current = false; setBusy(false)}
  }
  return <span><button type="button" disabled={busy} onClick={() => void open()}>{t(busy ? '准备编辑工作区…' : '继续编辑')}</button>{error && <span className="error" role="alert">{error}</span>}</span>
}
