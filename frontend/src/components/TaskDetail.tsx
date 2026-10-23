import { useI18n } from '../i18n'
import { useEffect, useRef, useState } from 'react'
import { baseURL, errorMessage, request, type Catalog, type Task } from '../api'
import { resultImageAlt, resultOriginLabel, resultProviderLabel, taskStatusLabel } from '../providerPresentation'
import { Icon } from './Icon'
import { TaskMetadata } from './TaskList'

export function TaskDetail({id, space, catalog, saved, saving, busy, actionError, notice, onClose, onUpdate, onSave, onEdit, onResume}: {
  id: string; space: string; catalog: Catalog | null; saved: boolean; saving: string | null; busy: boolean; actionError: string; notice: string
  onClose: () => void; onUpdate: (task: Task) => void; onSave: (task: Task) => void; onEdit: (task: Task) => void; onResume: (task: Task) => Promise<void>
}) {
  const { t, formatDate } = useI18n()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [task, setTask] = useState<Task | null>(null)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const dialog = dialogRef.current
    const previousFocus = document.activeElement as HTMLElement | null
    dialog?.showModal()
    return () => {dialog?.close(); previousFocus?.focus()}
  }, [])
  useEffect(() => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    async function load() {
      try {
        const result = await request<Task>(`/tasks/${encodeURIComponent(id)}?${new URLSearchParams({space_id: space})}`, {signal: controller.signal})
        if (controller.signal.aborted) return
        setTask(result); setError(''); onUpdate(result)
        if (result.status === 'queued' || result.status === 'running') timer = setTimeout(() => {void load()}, 1500)
      } catch (failure) {if (!controller.signal.aborted) setError(errorMessage(failure, "无法读取任务详情"))}
    }
    void load()
    return () => {controller.abort(); clearTimeout(timer)}
  }, [id, space, revision, onUpdate])

  return <dialog ref={dialogRef} className="detail-dialog" aria-labelledby="detail-title" onCancel={event => {event.preventDefault(); onClose()}} onKeyDown={event => {
    if (event.key !== 'Tab') return
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), [href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]')]
    const first = controls[0]
    const last = controls.at(-1)
    if (!first) {event.preventDefault(); return}
    if (event.shiftKey && (document.activeElement === first || document.activeElement === event.currentTarget)) {event.preventDefault(); last?.focus()}
    else if (!event.shiftKey && (document.activeElement === last || document.activeElement === event.currentTarget)) {event.preventDefault(); first.focus()}
  }}>
    <div className="dialog-header"><div><span className="eyebrow">CREATION DETAILS</span><h2 id="detail-title">{t("任务详情")}</h2></div><button type="button" className="icon-button" aria-label={t("关闭详情")} onClick={onClose} autoFocus><Icon name="close" /></button></div>
    {error && <div className="error" role="alert">{t("无法读取任务详情：")}{t(error)} <button onClick={() => setRevision(value => value + 1)}>{t("重新加载详情")}</button></div>}
    {!task && !error && <div className="loading-block" role="status">{t("正在加载任务详情…")}</div>}
    {task && <>
      <div className="detail-grid"><div className="detail-preview">{task.result_url ? <img src={`${baseURL}${task.result_url}`} alt={resultImageAlt(task)} /> : <div className="empty"><Icon name={task.status === 'failed' ? 'image' : 'spark'} /><p>{task.status === 'failed' ? t("本地暂无可用结果") : t("画面正在准备中")}</p></div>}<span>{task.result_url ? resultOriginLabel(task) : resultProviderLabel(task)}</span></div>
      <div className="detail-information" data-provider={task.provider}><span className={`status ${task.status}`}><span />{taskStatusLabel(task)}</span><h3>{t("画面描述")}</h3><p className="detail-prompt">{task.prompt}</p><TaskMetadata task={task} catalog={catalog} />
        {(task.status === 'queued' || task.status === 'running') && <div className="progress-block"><progress aria-label={t("详情任务进度")} value={task.progress} max="100" /><span>{t("任务进度 {progress}% · 自动更新",{progress:task.progress})}</span></div>}
        {task.error && <p className="error task-error">{t(task.error)}</p>}
        <dl><div><dt>{t("任务编号")}</dt><dd><code>{task.id}</code></dd></div><div><dt>{t("创建时间")}</dt><dd>{formatDate(task.created_at)}</dd></div><div><dt>{t("图像提供方")}</dt><dd>{resultProviderLabel(task)}</dd></div>{task.model && <div><dt>{t("模型")}</dt><dd>{task.model}</dd></div>}{task.result_mime && <div><dt>{t("结果格式")}</dt><dd>{task.result_mime}</dd></div>}{task.result_width && task.result_height && <div><dt>{t("实际尺寸")}</dt><dd>{task.result_width} × {task.result_height} px</dd></div>}{task.error_code && <div><dt>{t("失败代码")}</dt><dd><code>{task.error_code}</code></dd></div>}{task.retry_of && <div><dt>{t("原任务编号")}</dt><dd><code>{task.retry_of}</code></dd></div>}</dl>
      </div></div>
      {actionError && <div className="error" role="alert">{t(actionError)}</div>}{notice && <p className="notice" role="status">{t(notice)}</p>}
      <div className="dialog-actions"><span>{task.provider === 'mock-svg' ? t("模拟流程演示") : t("来源按任务记录保留")}{t("· 结果持久化保存")}</span>{task.status === 'succeeded' && <button type="button" className="primary" disabled={saved || saving !== null} onClick={() => onSave(task)}>{saved ? <><Icon name="check" />{t("已保存")}</> : saving === task.id ? t("保存中…") : t("保存到素材库")}</button>}{task.status === 'failed' && task.upstream_id && <button type="button" disabled={busy} onClick={() => {void onResume(task).then(() => setRevision(value => value + 1))}}>{t("继续查询原任务")}</button>}{task.status === 'failed' && <button type="button" className="primary" disabled={busy} onClick={() => onEdit(task)}><Icon name="refresh" />{t("编辑后重试")}</button>}</div>
    </>}
  </dialog>
}
