import { useI18n } from '../i18n'
import { baseURL, kindLabels, type Catalog, type Task } from '../api'
import { resultImageAlt, resultOriginLabel, taskStatusLabel } from '../providerPresentation'
import { Icon } from './Icon'

export function TaskMetadata({task, catalog}: {task: Pick<Task, 'kind' | 'ratio' | 'style'>; catalog: Catalog | null}) {
  const { t } = useI18n()
  return <div className="metadata"><span>{t(kindLabels[task.kind])}</span><span>{task.ratio}</span><span>{t(catalog?.styles.find(style => style.value === task.style)?.label ?? task.style)}</span></div>
}

export function TaskList({tasks, catalog, loading, error, savedIds, saving, busy, onSave, onDetail, onEdit, onResume}: {
  tasks: Task[]; catalog: Catalog | null; loading: boolean; error: string; savedIds: Set<string>; saving: string | null; busy: boolean
  onSave: (task: Task) => void; onDetail: (task: Task) => void; onEdit: (task: Task) => void; onResume: (task: Task) => Promise<void>
}) {
  const { t, formatDate } = useI18n()
  return <section className="panel results" aria-labelledby="results-title">
    <div className="section-heading"><div><span className="eyebrow">YOUR CREATIVE JOURNEY</span><h2 id="results-title">{t("创作记录")}<span className="count">{tasks.length}</span></h2></div><span className="live-label"><span />{t("自动更新")}</span></div>
    {loading ? <div className="loading-block" role="status"><Icon name="refresh" className="spinning" />{t("正在加载任务…")}</div> : !tasks.length && !error ? <div className="empty"><div className="empty-art" aria-hidden="true"><Icon name="spark" /><span /></div><h3>{t("给你的灵感，一张画布")}</h3><p>{t("选用上方模板，或写下自己的描述。")}<br />{t("任务进度与图像结果将在这里出现。")}</p><span className="empty-caption">A LITTLE IDEA. A NEW POSSIBILITY.</span></div> : !tasks.length && error ? <div className="empty"><Icon name="refresh" /><h3>{t("暂时无法读取创作记录")}</h3><p>{t("连接恢复后，你的任务会出现在这里。")}</p></div> : <div className="task-list">{tasks.map(task => <article className="task" key={task.id} data-provider={task.provider}>
      <div className="task-top"><span className={`status ${task.status}`}><span />{taskStatusLabel(task)}</span><time dateTime={task.created_at}>{formatDate(task.created_at, {hour: '2-digit', minute: '2-digit'})}</time></div>
      <p className="task-prompt">{task.prompt}</p><TaskMetadata task={task} catalog={catalog} />
      {task.result_url && <button type="button" className="result-preview" aria-label={task.provider === 'mock-svg' ? t("打开模拟图像详情") : t("打开图像详情")} onClick={() => onDetail(task)}><img src={`${baseURL}${task.result_url}`} alt={resultImageAlt(task)} /><span>{resultOriginLabel(task)}<Icon name="arrow" /></span></button>}
      {(task.status === 'queued' || task.status === 'running') && <div className="progress-block"><progress aria-label={t("任务进度")} max="100" value={task.progress} /><span>{t("任务进度 {progress}% · 请稍候",{progress:task.progress})}</span></div>}
      {task.error && <p className="error task-error">{t(task.error)}</p>}
      {task.retry_of && <p className="retry-reference">{t("重试自")}<code>{task.retry_of}</code></p>}
      <div className="task-bottom"><button type="button" className="text-button" onClick={() => onDetail(task)}>{t("查看详情")}<Icon name="arrow" /></button>{task.status === 'succeeded' && <button type="button" className="save-button" disabled={saving !== null || savedIds.has(task.id)} onClick={() => onSave(task)}>{savedIds.has(task.id) ? <><Icon name="check" />{t("已保存")}</> : saving === task.id ? t("保存中…") : t("保存到素材库")}</button>}{task.status === 'failed' && task.upstream_id && <button type="button" disabled={busy} onClick={() => {void onResume(task)}}>{t("继续查询原任务")}</button>}{task.status === 'failed' && <button type="button" disabled={busy} onClick={() => onEdit(task)}><Icon name="refresh" />{t("编辑后重试")}</button>}</div>
    </article>)}</div>}
  </section>
}
