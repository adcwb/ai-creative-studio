import {useState} from 'react'
import {useI18n} from '../i18n'
import {baseURL, kindLabels, type Catalog, type Task} from '../api'
import {resultImageAlt, resultOriginLabel, taskStatusLabel} from '../providerPresentation'
import {Icon} from './Icon'
import {DownloadImage, ImageViewer} from './ImageResult'

export function TaskMetadata({task, catalog}: {task: Pick<Task, 'kind' | 'ratio' | 'style'>; catalog: Catalog | null}) {
  const { t } = useI18n()
  return <div className="metadata"><span>{t(kindLabels[task.kind])}</span><span>{task.ratio}</span><span>{t(catalog?.styles.find(style => style.value === task.style)?.label ?? task.style)}</span></div>
}

export function TaskList({tasks, loading, error, total, page, pageSize, onPage, onDetail}: {
  tasks: Task[]; loading: boolean; error: string; total: number; page: number; pageSize: number
  onPage: (page: number) => void; onDetail: (task: Task) => void
}) {
  const {t, formatDate} = useI18n()
  const [viewing, setViewing] = useState<Task | null>(null)
  const pages = Math.max(1, Math.ceil(total / pageSize))
  return <section className="panel results" aria-labelledby="results-title" aria-busy={loading}>
    <div className="section-heading"><h2 id="results-title">{t('创作记录')}<span className="count">{total}</span></h2><span className="live-label"><span/>{t('自动更新')}</span></div>
    {loading ? <div className="loading-block" role="status"><Icon name="refresh" className="spinning"/>{t('正在加载任务…')}</div> : !tasks.length ? <div className="empty"><Icon name="image"/><h3>{t(error ? '暂时无法读取创作记录' : '给你的灵感，一张画布')}</h3><p>{t(error ? '连接恢复后，你的任务会出现在这里。' : '选用上方模板，或写下自己的描述。')}</p></div> : <div className="task-list task-gallery">{tasks.map(task => <article className="task" key={task.id} data-provider={task.provider}>
      {task.result_url ? <button type="button" className="result-preview" aria-label={t('放大查看')} onClick={() => setViewing(task)}><img loading="lazy" decoding="async" width={task.result_width || 1024} height={task.result_height || 1024} src={`${baseURL}${task.result_url}`} alt={resultImageAlt(task)}/></button> : <button type="button" className="task-placeholder" onClick={() => onDetail(task)}><Icon name={task.status === 'failed' ? 'image' : 'spark'}/><span>{t(task.status === 'failed' ? '未生成图像' : '画面正在准备中')}</span></button>}
      <div className="task-top"><span className={`status ${task.status}`}><span/>{taskStatusLabel(task)}</span><time dateTime={task.created_at}>{formatDate(task.created_at, {month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit'})}</time></div>
      <p className="task-prompt">{task.prompt}</p>
      {task.provider === 'mock-svg' && <span className="origin-label">{resultOriginLabel(task)}</span>}
      <div className="task-bottom"><button type="button" className="text-button" onClick={() => onDetail(task)}>{t('查看详情')}<Icon name="arrow"/></button>{task.result_url && <DownloadImage task={task}/>}</div>
    </article>)}</div>}
    {total > 0 && <nav className="task-pagination" aria-label={t('创作记录分页')}><span>{t('共 {total} 条', {total})}</span><div><button type="button" disabled={loading || page <= 1} onClick={() => onPage(page - 1)}>{t('上一页')}</button><span role="status">{t('第 {page} / {pages} 页', {page, pages})}</span><button type="button" disabled={loading || page >= pages} onClick={() => onPage(page + 1)}>{t('下一页')}</button></div></nav>}
    {viewing && <ImageViewer task={viewing} onClose={() => setViewing(null)}/>}
  </section>
}
