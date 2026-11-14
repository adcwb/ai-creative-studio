import { useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { baseURL, errorMessage, request, type Task } from '../../api'
import type { DramaProject } from '../../drama'
import { useI18n } from '../../i18n'
import { useNavigationTask } from '../../app/navigationGuard'
import {uploadMediaBlob} from '../uploads/uploadBlob'

export function ContinueImage({task, space, onClose, disabled = false}: {task: Task; space: string; onClose: () => void; disabled?: boolean}) {
  const {t} = useI18n()
  const navigate = useNavigate()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const storageKey = `image.continue:${space}:${task.id}`
  const [initial] = useState(() => {try {const saved = JSON.parse(sessionStorage.getItem(storageKey) ?? 'null') as {key?: string; id?: string; protocol?:string} | null; if (saved?.key) return {key: saved.key, id: saved.id ?? '',protocol:saved.protocol??'raw'}} catch { /* Optional recovery. */ } return {key: crypto.randomUUID(), id: '',protocol:'chunks'}})
  const key = useRef(initial.key)
  const flight = useRef(false)
  const project = useRef(initial.id)
  const releaseNavigation=useNavigationTask(busy)
  async function open() {
    if (disabled || flight.current || !task.result_url?.startsWith('/')) return
    flight.current = true; setBusy(true); setError('')
    try {sessionStorage.setItem(storageKey, JSON.stringify({key: key.current, id: project.current,protocol:initial.protocol}))} catch { /* In-memory recovery. */ }
    try {
      const response = await fetch(`${baseURL}${task.result_url}`, {credentials: 'include'})
      if (!response.ok) throw new Error(t('原图读取失败'))
      const blob = await response.blob()
      if (!['image/png', 'image/jpeg'].includes(blob.type)) throw new Error(t('请选择有效的PNG、JPEG、MP4、WAV或MP3文件'))
      const query = new URLSearchParams({space_id: space})
      if (!project.current) project.current = (await request<DramaProject>(`/projects?${query}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': key.current}, body: JSON.stringify({title: task.prompt.slice(0, 60), brief: task.prompt, kind: 'image'})})).id
      try {sessionStorage.setItem(storageKey, JSON.stringify({key: key.current, id: project.current,protocol:initial.protocol}))} catch { /* In-memory recovery. */ }
      const name=`source-${task.id}.${blob.type === 'image/png' ? 'png' : 'jpg'}`
      // Finish a pre-upgrade ambiguous raw request in its original receipt
      // namespace; switching protocols here could duplicate an accepted asset.
      if(initial.protocol==='raw') {query.set('filename',name);query.set('role','reference');await request(`/projects/${project.current}/media/uploads?${query}`,{method:'POST',headers:{'Content-Type':blob.type,'Idempotency-Key':key.current+'-source'},body:blob})}
      else await uploadMediaBlob(space,project.current,blob,name,'reference',key.current+'-source')
      const id = project.current
      releaseNavigation()
      onClose()
      void navigate(`/studio/workspaces/${id}`)
    } catch (e) {setError(errorMessage(e, t('继续编辑失败')))}
    finally {flight.current = false; setBusy(false)}
  }
  return <span><button type="button" disabled={disabled || busy} onClick={() => void open()}>{t(busy ? '准备编辑工作区…' : '继续编辑')}</button>{error && <span className="error" role="alert">{error}</span>}</span>
}
