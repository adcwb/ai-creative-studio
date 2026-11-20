import {hasPermission} from '../permissions'
import { useEffect, useMemo, useState } from 'react'
import { Link,useSearchParams } from 'react-router-dom'
import {SeriesList} from '../features/series/SeriesList'
import { errorMessage, request } from '../api'
import { useAppWorkspace } from '../app/workspace'
import { useI18n } from '../i18n'
import { type DramaProject } from '../drama'
import {SpaceGuidance} from '../components/SpaceGuidance'
import { Icon } from '../components/Icon'
import {ProjectCard} from '../domain/ProjectCard'
import {StickyActionBar} from '../ui/StickyActionBar'
import {StudioEmpty} from '../ui/StudioEmpty'
import {StudioError} from '../ui/StudioError'
import {StudioSkeleton} from '../ui/StudioSkeleton'
import {StudioButton} from '../ui/StudioButton'
import {StudioInput} from '../ui/StudioInput'
import {StudioSelect} from '../ui/StudioSelect'

export function ProjectsPage() {
  const {space} = useAppWorkspace()
  const [params,setParams]=useSearchParams()
  const seriesView=params.get('view')==='series'
  const {t} = useI18n()
  const [projects, setProjects] = useState<DramaProject[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState('recent')
  useEffect(() => {
    if(seriesView)return
    const controller = new AbortController()
    void request<DramaProject[]>(`/projects?${new URLSearchParams({space_id: space!.id})}`, {signal: controller.signal}).then(result => {
      if (!controller.signal.aborted) {setProjects(result.filter(p => (!p.kind || p.kind === 'drama')&&!p.series_episode_id)); setError(''); setLoading(false)}
    }).catch(reason => {if (!controller.signal.aborted) {setError(errorMessage(reason, t('项目列表读取失败'))); setLoading(false)}})
    return () => controller.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [space?.id, revision,seriesView])
  // GET /projects returns the whole array with no cursor or limit, so search and
  // sort run on what is already loaded. Server-side paging arrives with the
  // long-content work; until then this must not pretend to be a remote query.
  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    const matched = needle ? projects.filter(p => `${p.title} ${p.brief}`.toLowerCase().includes(needle)) : projects
    return [...matched].sort((a, b) => sort === 'title' ? a.title.localeCompare(b.title, 'zh-Hans-CN') : b.updated_at.localeCompare(a.updated_at))
  }, [projects, query, sort])
  function reload() {setLoading(true); setRevision(value => value + 1)}
  return <div className="projects-page"><header className="page-heading"><div><h1>{t('短剧创作')}</h1><p>{t('从故事简报到成片发布，按项目推进每一步。')}</p></div>{hasPermission(space,'workspace.write')&&<Link className="button-link primary" to={seriesView?'/series/new':'/projects/new/brief'}><Icon name="spark" />{t(seriesView?'新建系列':'新建短剧项目')}</Link>}</header>
    {!seriesView && !loading && !error && !projects.length && <SpaceGuidance />}
    <StickyActionBar label={t('项目筛选')} status={<span className="project-count">{t('全部项目')} <span className="count">{seriesView ? '' : visible.length}</span></span>}>
      <nav className="project-steps" aria-label={t('创作目录')}><button aria-pressed={!seriesView} onClick={()=>setParams({})}>{t('独立短剧')}</button><button aria-pressed={seriesView} onClick={()=>setParams({view:'series'})}>{t('系列漫剧')}</button></nav>
      {!seriesView && <div className="project-filters">
        <StudioInput aria-label={t('搜索项目')} placeholder={t('按标题或简报搜索')} value={query} maxLength={100} onChange={setQuery} prefix={<Icon name="search" />} />
        <StudioSelect aria-label={t('排序方式')} value={sort} onChange={setSort} options={[{value:'recent',label:t('最近更新')},{value:'title',label:t('按标题排序')}]} />
      </div>}
    </StickyActionBar>
    {seriesView?<SeriesList/>:<>
    {loading && <div className="project-grid" aria-busy="true">{[0,1,2,3].map(index => <StudioSkeleton key={index} shape="card" rows={3} className="project-card-skeleton" />)}</div>}
    {error && <StudioError message={error} action={<StudioButton onClick={reload}>{t('重新加载项目列表')}</StudioButton>} />}
    {!loading && !error && !projects.length && <StudioEmpty icon="script" title={t('还没有短剧项目')} description={t('给故事起个名字，保存第一份简报。')} action={<Link className="button-link" to="/projects/new/brief">{t('新建短剧项目')}</Link>} />}
    {!loading && !error && projects.length > 0 && !visible.length && <StudioEmpty kind="no-results" icon="search" title={t('没有匹配的项目')} description={t('换个关键词，或清除筛选查看全部项目。')} action={<StudioButton onClick={() => setQuery('')}>{t('清除筛选')}</StudioButton>} />}
    <div className="project-grid">{visible.map(project => <ProjectCard key={project.id} project={project} />)}</div>
  </>}
  </div>
}