import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { errorMessage, request } from '../api'
import { useAppWorkspace } from '../app/workspace'
import { useI18n } from '../i18n'
import { generationLabels, type DramaProject } from '../drama'
import { Icon } from '../components/Icon'

export function ProjectsPage() {
  const {space} = useAppWorkspace()
  const {t, formatDate} = useI18n()
  const [projects, setProjects] = useState<DramaProject[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    void request<DramaProject[]>(`/projects?${new URLSearchParams({space_id: space!.id})}`, {signal: controller.signal}).then(result => {
      if (!controller.signal.aborted) {setProjects(result); setError(''); setLoading(false)}
    }).catch(reason => {if (!controller.signal.aborted) {setError(errorMessage(reason, t('项目列表读取失败'))); setLoading(false)}})
    return () => controller.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [space?.id, revision])
  return <div className="projects-page"><header className="page-heading"><div><h1>{t('短剧项目')}</h1><p>{t('从故事简报到成片发布，按项目推进每一步。')}</p></div><Link className="button-link primary" to="/projects/new/brief"><Icon name="spark" />{t('新建短剧项目')}</Link></header>
    <div className="project-list-heading"><span>{t('全部项目')} <span className="count">{projects.length}</span></span><span>{t('最近更新')}</span></div>
    {loading && <p className="loading-block" role="status">{t('正在读取项目列表…')}</p>}
    {error && <div className="error" role="alert">{error}<button type="button" onClick={() => {setLoading(true); setRevision(value => value + 1)}}>{t('重新加载项目列表')}</button></div>}
    {!loading && !error && !projects.length && <section className="panel page-empty"><Icon name="script" /><h2>{t('还没有短剧项目')}</h2><p>{t('给故事起个名字，保存第一份简报。')}</p><Link className="button-link" to="/projects/new/brief">{t('新建短剧项目')}</Link></section>}
    <div className="project-grid">{projects.map(project => <Link className="project-card" key={project.id} to={`/projects/${encodeURIComponent(project.id)}/brief`}><div className="project-card-icon"><Icon name="script" /><span>{t('版本')}{project.version}</span></div><h2>{project.title}</h2><p>{project.brief}</p><div className="project-card-footer"><span>{project.latest_generation ? t(generationLabels[project.latest_generation.status]) : t('等待第一份剧本')}</span><time>{formatDate(project.updated_at, {dateStyle: 'medium'})}</time></div></Link>)}</div>
  </div>
}
