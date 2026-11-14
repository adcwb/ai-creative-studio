import {useEffect,useState} from 'react'
import {Link,useParams} from 'react-router-dom'
import {request,errorMessage} from '../api'
import {useAppWorkspace} from '../app/workspace'
import {useI18n} from '../i18n'
import type {DramaProject} from '../drama'
import WorkflowStudio from '../features/workflows/WorkflowStudio'
export function WorkflowPage(){const {space}=useAppWorkspace(),{projectId}=useParams();return <WorkflowContent key={`${space?.id}:${projectId??''}`} id={projectId}/>}
function WorkflowContent({id}:{id?:string}){
 const {space}=useAppWorkspace(),{t}=useI18n(),[projects,setProjects]=useState<DramaProject[]>([]),[project,setProject]=useState<DramaProject|null>(null),[loading,setLoading]=useState(true),[error,setError]=useState(''),[refresh,setRefresh]=useState(0)
 useEffect(()=>{const abort=new AbortController();const q='?space_id='+space!.id
 void (id?request<DramaProject>(`/projects/${id}${q}`,{signal:abort.signal}).then(v=>{if(!abort.signal.aborted)setProject(v)}):request<DramaProject[]>(`/projects${q}`,{signal:abort.signal}).then(v=>{if(!abort.signal.aborted)setProjects(v)})).then(()=>{if(!abort.signal.aborted){setLoading(false);setError('')}}).catch(e=>{if(!abort.signal.aborted){setLoading(false);setError(errorMessage(e,t('作品读取失败')))}});return()=>abort.abort()},[id,space,t,refresh])
 return <div className="projects-page"><header className="page-heading"><div>{id&&<Link to="/workflows">{t('返回流程作品列表')}</Link>}<h1>{project?.title??t('创作流程')}</h1><p>{t('为同一件作品保存创作步骤，运行真实模型，并保留人工选择。')}</p></div>{!id&&<Link className="button-link" to="/studio/workspaces">{t('新建图像作品')}</Link>}</header>
 {loading&&<p role="status">{t('正在读取作品…')}</p>}{error&&<p role="alert" className="error">{error}<button onClick={()=>setRefresh(v=>v+1)}>{t('重新读取')}</button></p>}
 {!id&&!loading&&!error&&<>{!projects.length&&<section className="panel page-empty"><h2>{t('从一件作品开始搭建流程')}</h2><p>{t('先新建图像作品、短剧项目或数字人作品，再为它安排生成与审片步骤。')}</p></section>}<div className="project-grid">{projects.map(p=><Link className="project-card" key={p.id} to={`/workflows/${p.id}`}><h2>{p.title}</h2><p>{p.brief}</p><span>{t('打开创作流程')}</span></Link>)}</div></>}
 {project&&<WorkflowStudio project={project.id} space={space!.id} projectVersion={project.version}/>}</div>
}
