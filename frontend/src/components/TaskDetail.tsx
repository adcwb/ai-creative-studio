import {usePolling} from '../hooks/usePolling'
import { useI18n } from '../i18n'
import { useState } from 'react'
import { baseURL, errorMessage, request, type Catalog, type Task } from '../api'
import { resultImageAlt, resultOriginLabel, resultProviderLabel, taskStatusLabel } from '../providerPresentation'
import { Icon } from './Icon'
import { TaskMetadata } from './TaskList'
import { ContinueImage } from '../features/images/ContinueImage'
import {DownloadImage, ImageViewer} from './ImageResult'
import {StudioDialog} from '../ui/StudioDialog'

export function TaskDetail({id, space, catalog, saved, saving, busy, actionError, notice, onClose, onUpdate, onSave, onEdit, onResume, onSavePrompt}: {
  id: string; space: string; catalog: Catalog | null; saved: boolean; saving: string | null; busy: boolean; actionError: string; notice: string
  onSavePrompt?: (task: Task) => void; onClose: () => void; onUpdate: (task: Task) => void; onSave: (task: Task) => void; onEdit: (task: Task) => void; onResume: (task: Task) => Promise<void>
}) {
  const { t, formatDate } = useI18n()
  const [task, setTask] = useState<Task | null>(null)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [viewing, setViewing] = useState(false)
  usePolling(async signal => {
    try {
      const result = await request<Task>(`/tasks/${encodeURIComponent(id)}?${new URLSearchParams({space_id: space})}`, {signal})
      if (signal.aborted) return
      setTask(result); setError(''); onUpdate(result)
      return result.status === 'queued' || result.status === 'running'
    } catch (failure) {
      if (!signal.aborted) setError(errorMessage(failure, t('无法读取任务详情')))
      throw failure
    }
  }, 1500, `${space}:${id}:${revision}`)

  return <StudioDialog title={t("任务详情")} onClose={onClose} closeLabel={t("关闭详情")} width={820} className="detail-dialog-content">
    <div className="studio-dialog-body detail-body">
    {error && <div className="error" role="alert">{t("无法读取任务详情：")}{t(error)} <button onClick={() => setRevision(value => value + 1)}>{t("重新加载详情")}</button></div>}
    {!task && !error && <div className="loading-block" role="status">{t("正在加载任务详情…")}</div>}
    {task && <>
      {task.status === 'succeeded' && task.result_mime !== 'image/svg+xml' && <ContinueImage task={task} space={space} onClose={onClose} />}
      <div className="detail-grid"><div className="detail-result"><div className="detail-preview">{task.result_url ? <button type="button" className="detail-image-button" aria-label={t('放大查看')} onClick={() => setViewing(true)}><img decoding="async" width={task.result_width} height={task.result_height} src={`${baseURL}${task.result_url}`} alt={resultImageAlt(task)} /></button> : <div className="empty"><Icon name={task.status === 'failed' ? 'image' : 'spark'} /><p>{task.status === 'failed' ? t("本地暂无可用结果") : t("画面正在准备中")}</p></div>}<span>{task.result_url ? resultOriginLabel(task) : resultProviderLabel(task)}</span></div>{task.result_url && <div className="detail-image-actions"><button type="button" onClick={() => setViewing(true)}>{t('放大查看')}</button><DownloadImage task={task}/></div>}</div>
      <div className="detail-information" data-provider={task.provider}><span className={`status ${task.status}`}><span />{taskStatusLabel(task)}</span><h3>{t("画面描述")}</h3><p className="detail-prompt">{task.prompt}</p><TaskMetadata task={task} catalog={catalog} />
 {task.prompt_source && <p className="prompt-source-note">{t("来源提示词版本")} <code>{task.prompt_source.version_id}</code> · {t(task.prompt_source.edited ? "已修改后生成" : "按保存版本生成")}</p>}
 {task.status === "succeeded" && onSavePrompt && <button type="button" onClick={() => onSavePrompt(task)}>{t("将此描述存为提示词")}</button>}
        {(task.status === 'queued' || task.status === 'running') && <div className="progress-block"><progress aria-label={t("详情任务进度")} value={task.progress} max="100" /><span>{t("任务进度 {progress}% · 自动更新",{progress:task.progress})}</span></div>}
        {task.error && <p className="error task-error">{t(task.error)}</p>}
        <dl><div><dt>{t("任务编号")}</dt><dd><code>{task.id}</code></dd></div><div><dt>{t("创建时间")}</dt><dd>{formatDate(task.created_at)}</dd></div><div><dt>{t("图像提供方")}</dt><dd>{resultProviderLabel(task)}</dd></div>{task.model && <div><dt>{t("模型")}</dt><dd>{task.model}</dd></div>}{task.result_mime && <div><dt>{t("结果格式")}</dt><dd>{task.result_mime}</dd></div>}{task.result_width && task.result_height && <div><dt>{t("实际尺寸")}</dt><dd>{task.result_width} × {task.result_height} px</dd></div>}{task.error_code && <div><dt>{t("失败代码")}</dt><dd><code>{task.error_code}</code></dd></div>}{task.retry_of && <div><dt>{t("原任务编号")}</dt><dd><code>{task.retry_of}</code></dd></div>}</dl>
      </div></div>
      {actionError && <div className="error" role="alert">{t(actionError)}</div>}{notice && <p className="notice" role="status">{t(notice)}</p>}
      <div className="dialog-actions"><span>{task.provider === 'mock-svg' ? t("模拟流程演示") : t("来源按任务记录保留")}{t("· 结果持久化保存")}</span>{task.status === 'succeeded' && <button type="button" className="primary" disabled={saved || saving !== null} onClick={() => onSave(task)}>{saved ? <><Icon name="check" />{t("已保存")}</> : saving === task.id ? t("保存中…") : t("保存到素材库")}</button>}{task.status === 'failed' && task.upstream_id && <button type="button" disabled={busy} onClick={() => {void onResume(task).then(() => setRevision(value => value + 1))}}>{t("继续查询原任务")}</button>}{task.status === 'failed' && <button type="button" className="primary" disabled={busy} onClick={() => onEdit(task)}><Icon name="refresh" />{t("编辑后重试")}</button>}</div>
      {viewing && <ImageViewer task={task} onClose={() => setViewing(false)}/>}
    </>}
    </div>
  </StudioDialog>
}
