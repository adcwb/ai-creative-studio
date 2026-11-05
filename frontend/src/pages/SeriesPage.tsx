import {useEffect,useState} from 'react'
import {Link,useNavigate,useParams} from 'react-router-dom'
import {useAppWorkspace} from '../app/workspace'
import {request,errorMessage} from '../api'
import {useI18n} from '../i18n'
import {SeriesForm,SeriesMoveForm} from '../features/series/SeriesForm'
import {useDirectory} from '../features/series/useDirectory'
import {StoryWorkspace} from '../features/series/StoryWorkspace'
import {directoryURL,episodeStatus,type SeriesEntry} from '../features/series/types'
import '../features/series/series.css'
export function SeriesPage(){const {seriesId}=useParams();const {space}=useAppWorkspace();return seriesId==='new'?<NewSeriesPage key={space?.id}/>:<SeriesWorkspace key={`${space?.id}:${seriesId}`} id={seriesId??''}/>}
function NewSeriesPage(){const {t}=useI18n();const navigate=useNavigate();
 return <div className="projects-page"><Link className="project-back" to="/projects?view=series">{t('返回系列目录')}</Link><header className="page-heading"><div><h1>{t('新的故事系列')}</h1><p>{t('先确定方向，后续可以逐集调整。')}</p></div></header><SeriesForm kind="series" onCancel={()=>void navigate('/projects?view=series')} onSaved={n=>void navigate(`/series/${n.id}`,{replace:true})}/></div>
}
function SeriesWorkspace({id}:{id:string}){
 const {t}=useI18n();const {space,requestLeave}=useAppWorkspace()
 const [root,setRoot]=useState<SeriesEntry|null>(null),[error,setError]=useState(''),[revision,setRevision]=useState(0),[selected,setSelected]=useState('')
 const [mode,setMode]=useState('')
 const seasons=useDirectory(`/series/${id}/seasons`,space!.id,revision)
 useEffect(()=>{
  if(id==='new')return
  const controller=new AbortController()
  void request<SeriesEntry>(directoryURL(`/series/${id}`,space!.id),{signal:controller.signal}).then(n=>{if(!controller.signal.aborted){setRoot(n);setError('')}}).catch(reason=>{if(!controller.signal.aborted)setError(errorMessage(reason,t('系列读取失败')))})
  return()=>controller.abort()
  // Language changes must preserve the draft and selected season.
  // eslint-disable-next-line react-hooks/exhaustive-deps
 },[id,space?.id,revision])
 const refresh=()=>setRevision(v=>v+1)
 const close=()=>setMode('')
 const onSaved=()=>{setMode('');refresh()}
 const season=seasons.items.find(n=>n.id===selected)??seasons.items[0]
 const writable=space?.role!=='viewer'

 return <div className="projects-page series-page"><Link className="project-back" to="/projects?view=series">{t('返回系列目录')}</Link>
 {error&&<div className="error" role="alert">{error}<button onClick={refresh}>{t('重新读取目录')}</button></div>}
 {!root&&!error&&<p className="loading-block" role="status">{t('正在读取系列目录…')}</p>}
 {root&&<><header className="page-heading"><div><span className="eyebrow">{t('系列创作')}</span><h1>{root.title}</h1><p>{root.summary}</p></div>{writable&&<button onClick={()=>requestLeave(()=>setMode('root'))}>{t('系列设置')}</button>}</header>
 <div className="series-overview"><span>{root.status==='archived'?t('已归档'):t('创作中')}</span><span>{t('目标 {p0} 秒 / 集',{p0:root.profile.duration_seconds})}</span><span>{root.profile.output_resolution} · {root.profile.aspect_ratio}</span>{root.audience&&<span>{t('目标观众')} · {root.audience}</span>}</div>
 {root.status==='archived'&&<p className="editing-note">{t('系列已归档，现有作品仍可查看和编辑。恢复后可继续添加季和集。')}</p>}
 <StoryWorkspace key={root.id} root={root.id}/>
 {mode==='root'&&<SeriesForm kind="series" item={root} onCancel={close} onSaved={onSaved} onRefresh={refresh}/>}
 <section className="panel series-seasons"><div className="series-heading"><h2>{t('季与剧集')}</h2>{writable&&root.status==='active'&&<button onClick={()=>requestLeave(()=>setMode('new-season'))}>{t('添加一季')}</button>}</div>
 {seasons.loading&&<p role="status">{t('正在读取…')}</p>}{seasons.error&&<div className="error" role="alert">{seasons.error}<button onClick={refresh}>{t('重新读取目录')}</button></div>}
 {!!seasons.items.length&&<div className="series-season-actions"><label>{t('选择季')}<select value={season?.id??''} onChange={e=>requestLeave(()=>{setSelected(e.target.value);setMode('')})}>{seasons.items.map(n=><option value={n.id} key={n.id}>{t('第 {p0} 季',{p0:n.ordinal??1})}{n.title?` · ${n.title}`:''}{n.status==='archived'?` · ${t('已归档')}`:''}</option>)}</select></label>{writable&&season&&<><button onClick={()=>requestLeave(()=>setMode('season'))}>{t('本季设置')}</button><button onClick={()=>requestLeave(()=>setMode('move-season'))}>{t('调整季号')}</button></>}</div>}
 {seasons.next_cursor&&<button disabled={seasons.more} onClick={()=>void seasons.loadMore()}>{t('加载更多季')}</button>}
 {!seasons.loading&&!seasons.error&&!seasons.items.length&&<div className="page-empty"><h3>{t('先为故事添加第一季')}</h3><p>{t('每一集都有独立的剧本、镜头和剪辑空间。')}</p></div>}
 </section>
 {mode==='new-season'&&<SeriesForm kind="season" parent={root.id} profile={root.profile} onCancel={close} onSaved={n=>{setSelected(n.id);onSaved()}}/>}
 {mode==='season'&&season&&<SeriesForm key={season.id} kind="season" item={season} onCancel={close} onSaved={onSaved} onRefresh={refresh}/>}
 {mode==='move-season'&&season&&<SeriesMoveForm item={season} parent={root} onCancel={close} onSaved={onSaved}/>}
 {season&&<EpisodeDirectory key={`${season.id}:${mode!==''}`} root={root} season={season} revision={revision} refresh={refresh} disabled={mode!==''}/>}
 </>}
 </div>
}
function EpisodeDirectory({root,season,revision,refresh,disabled}:{root:SeriesEntry;season:SeriesEntry;revision:number;refresh:()=>void;disabled:boolean}){
 const {t}=useI18n();const {space,requestLeave}=useAppWorkspace();const navigate=useNavigate()
 const list=useDirectory(`/seasons/${season.id}/episodes`,space!.id,revision)
 const [mode,setMode]=useState(''),[selected,setSelected]=useState('')
 const writable=space?.role!=='viewer'
 const item=list.items.find(n=>n.id===selected)
 const close=()=>setMode('')
 const saved=()=>{setMode('');refresh()}
 return <section className="series-episodes"><div className="series-heading"><div><h2>{t('第 {p0} 季',{p0:season.ordinal??1})}{season.title?` · ${season.title}`:''}</h2>{season.summary&&<p>{season.summary}</p>}</div>{writable&&root.status==='active'&&season.status==='active'&&<button className="primary" disabled={disabled} onClick={()=>requestLeave(()=>setMode('new'))}>{t('添加一集')}</button>}</div>
 {mode==='new'&&!disabled&&<SeriesForm kind="episode" parent={season.id} profile={season.profile} onCancel={close} onSaved={n=>void navigate(`/projects/${n.project_id}/brief`)}/>}
 {mode==='edit'&&item&&!disabled&&<SeriesForm key={item.id} kind="episode" item={item} onCancel={close} onSaved={saved} onRefresh={refresh}/>}
 {mode==='move'&&item&&!disabled&&<SeriesMoveForm key={item.id} item={item} parent={season} onCancel={close} onSaved={saved}/>}
 {list.loading&&<p className="loading-block" role="status">{t('正在读取剧集…')}</p>}{list.error&&<div className="error" role="alert">{list.error}<button onClick={refresh}>{t('重新读取目录')}</button></div>}
 {!list.loading&&!list.error&&!list.items.length&&<section className="panel page-empty"><h3>{t('这一季的故事等待开场')}</h3><p>{t('添加第一集，写下本集的冲突、转折与结尾。')}</p></section>}
 <div className="project-grid">{list.items.map(n=><article className="project-card series-episode-card" key={n.id}><div className="project-card-icon"><span>{t('第 {p0} 集',{p0:n.ordinal??1})}</span><span>{t(episodeStatus[n.status]??'待创作')}</span></div><h3><Link to={`/projects/${n.project_id}/brief`}>{n.title}</Link></h3><p>{n.summary}</p><div className="project-card-footer"><span>{n.profile.duration_seconds}{t('秒')} · {n.profile.output_resolution}</span></div><div className="series-card-actions"><Link className="button-link" to={`/projects/${n.project_id}/brief`}>{t('进入这一集')}</Link>{writable&&<><button disabled={disabled} onClick={()=>requestLeave(()=>{setSelected(n.id);setMode('edit')})}>{t('制作设置')}</button><button disabled={disabled} onClick={()=>requestLeave(()=>{setSelected(n.id);setMode('move')})}>{t('调整集号')}</button></>}</div></article>)}</div>
 {list.next_cursor&&<button disabled={list.more||list.loading} onClick={()=>void list.loadMore()}>{list.more?t('正在读取…'):t('加载更多剧集')}</button>}
 </section>
}
