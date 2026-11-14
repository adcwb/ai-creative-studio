import {Button} from 'antd'
import {useState} from 'react'
import {useI18n} from '../i18n'
import {baseURL, errorMessage, kindLabels, type Catalog, type Task} from '../api'
import {resultImageAlt, resultOriginLabel, taskStatusLabel} from '../providerPresentation'
import {Icon} from './Icon'
import {DownloadImage, ImageViewer} from './ImageResult'
import {ContinueImage} from '../features/images/ContinueImage'
import {ConfirmDialog} from './ConfirmDialog'

export function TaskMetadata({task, catalog}: {task: Pick<Task, 'kind' | 'ratio' | 'style'>; catalog: Catalog | null}) {
  const { t } = useI18n()
  return <div className="metadata"><span>{t(kindLabels[task.kind])}</span><span>{task.ratio}</span><span>{t(catalog?.styles.find(style => style.value === task.style)?.label ?? task.style)}</span></div>
}

export function TaskList({tasks, loading, error, total, page, pageSize, onPage, onDetail, space, readOnly = false, busy = false, onDelete, onClear}: {
  tasks: Task[]; loading: boolean; error: string; total: number; page: number; pageSize: number
  onPage: (page: number) => void; onDetail: (task: Task) => void; space: string; readOnly?: boolean; busy?: boolean; onDelete: (id: string) => Promise<void>; onClear: () => Promise<void>
}) {
  const {t, formatDate} = useI18n()
  const [viewing, setViewing] = useState<Task | null>(null)
  const [confirmation, setConfirmation] = useState<Task | 'all' | null>(null)
  const [deleteError, setDeleteError] = useState('')
  function askDelete(task: Task | 'all') {setDeleteError(''); setConfirmation(task)}
  async function remove() {
    if (!confirmation || busy) return
    setDeleteError('')
    try {
      if (confirmation === 'all') await onClear(); else await onDelete(confirmation.id)
      setConfirmation(null); setViewing(null)
    } catch (failure) {setDeleteError(errorMessage(failure, t('未能确认删除结果，请刷新创作记录。')))}
  }
  const pages = Math.max(1, Math.ceil(total / pageSize))
  return <section className="panel results" aria-labelledby="results-title" aria-busy={loading}>
    <div className="section-heading"><h2 id="results-title">{t('创作记录')}<span className="count">{total}</span></h2><div className="history-actions"><span className="live-label"><span/>{t('自动更新')}</span>{!readOnly && <Button htmlType="button" disabled={busy || loading || !total} onClick={() => askDelete('all')}>{t('清空历史')}</Button>}</div></div>
    {loading ? <div className="loading-block" role="status"><Icon name="refresh" className="spinning"/>{t('正在加载任务…')}</div> : !tasks.length ? <div className="empty"><Icon name="image"/><h3>{t(error ? '暂时无法读取创作记录' : '给你的灵感，一张画布')}</h3><p>{t(error ? '连接恢复后，你的任务会出现在这里。' : '选用上方模板，或写下自己的描述。')}</p></div> : <div className="task-list task-gallery">{tasks.map(task => <article className="task" key={task.id} data-provider={task.provider}>
      {task.result_url ? <Button htmlType="button" className="result-preview" aria-label={t('放大查看')} onClick={() => setViewing(task)}><img loading="lazy" decoding="async" width={task.result_width || 1024} height={task.result_height || 1024} src={`${baseURL}${task.result_url}`} alt={resultImageAlt(task)}/></Button> : <Button htmlType="button" className="task-placeholder" onClick={() => onDetail(task)}><Icon name={task.status === 'failed' ? 'image' : 'spark'}/><span>{t(task.status === 'failed' ? '未生成图像' : '画面正在准备中')}</span></Button>}
      <div className="task-top"><span className={`status ${task.status}`}><span/>{taskStatusLabel(task)}</span><span className="task-resolution" aria-label={t('分辨率')}>{task.result_width && task.result_height ? `${task.result_width} × ${task.result_height} px` : t(task.status === 'succeeded' ? '分辨率未知' : '暂无分辨率')}</span><time dateTime={task.created_at}>{formatDate(task.created_at, {month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'})}</time></div>
      <p className="task-prompt">{task.prompt}</p>
      {task.provider === 'mock-svg' && <span className="origin-label">{resultOriginLabel(task)}</span>}
      <div className="task-bottom"><Button htmlType="button" className="text-button" onClick={() => onDetail(task)}>{t('查看详情')}<Icon name="arrow"/></Button>{!readOnly && task.status === 'succeeded' && task.result_url && task.result_mime !== 'image/svg+xml' && <ContinueImage task={task} space={space} disabled={busy} onClose={() => setViewing(null)} />}{task.result_url && <DownloadImage task={task}/>}<span className="task-delete">{!readOnly && (task.status === 'succeeded' || task.status === 'failed') && <Button htmlType="button" disabled={busy} aria-label={t('删除创作记录')} onClick={() => askDelete(task)}>{t('删除')}</Button>}</span></div>
    </article>)}</div>}
    {total > 0 && <nav className="task-pagination" aria-label={t('创作记录分页')}><span>{t('共 {total} 条', {total})}</span><div><Button htmlType="button" disabled={busy || loading || page <= 1} onClick={() => onPage(page - 1)}>{t('上一页')}</Button><span role="status">{t('第 {page} / {pages} 页', {page, pages})}</span><Button htmlType="button" disabled={busy || loading || page >= pages} onClick={() => onPage(page + 1)}>{t('下一页')}</Button></div></nav>}
    {confirmation && <ConfirmDialog title={t(confirmation === 'all' ? '清空创作历史？' : '删除这条创作记录？')} message={t(confirmation === 'all' ? '永久清空当前空间已结束的创作记录，无法恢复。进行中的任务、素材库和编辑工作区中的图片将保留。' : '永久删除此创作记录，无法恢复。素材库和编辑工作区中的图片不受影响。')} confirmLabel={t(confirmation === 'all' ? '确认清空' : '确认删除')} cancelLabel={t('取消')} busy={busy} error={deleteError} onCancel={() => setConfirmation(null)} onConfirm={() => void remove()} />}
    {viewing && <ImageViewer task={viewing} onClose={() => setViewing(null)}/>}
  </section>
}
