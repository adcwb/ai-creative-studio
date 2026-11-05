import {useCallback, useEffect, useRef, useState} from 'react'
import {Link} from 'react-router-dom'
import {errorMessage, request} from '../../api'
import {useAppWorkspace} from '../../app/workspace'
import {useNavigationTask} from '../../app/navigationGuard'
import {translate, useI18n} from '../../i18n'
import type {DramaProject} from '../../drama'
import {useDirectory} from '../series/useDirectory'
import {useSeriesWrite} from '../series/useSeriesWrite'
import {directoryURL, type SeriesEntry} from '../series/types'
import '../series/series.css'

interface MigrationInput {
 source_episode_number:number; target_season_id:string; target_ordinal:number
 expected_project_version:number;expected_media_version:number;expected_season_version:number
 output_mode:'series'|'preserve';storyboard_policy:'include'|'omit'
 shot_assignments:Record<string,number>;character_assignments:Record<string,string>
}
interface Issue {code:string;subject?:string;message:string}
interface Plan {
 digest:string;input:MigrationInput;issues:Issue[];warnings:Issue[]
 script?:{scenes:unknown[]};storyboard?:{shots:unknown[]}
 assets:{source:{id:string;name:string;character:string}}[];retained_source_exports:string[]
}
interface MigrationJob {id:string;target_project_id:string;row_version:number;state:string;phase:string;error?:string;source_episode_number:number;target_ordinal:number;copied_assets:number;total_assets:number}
interface SourceSummary {project_version:number;script_version:number;media_version:number;episodes:number[];characters:{id:string;name:string}[];unassigned_shots:{id:string;label:string}[];historical_exports:number}
interface JobPage {items:MigrationJob[];next_cursor:string}

export function EpisodeMigrationPanel({space,project,disabled}:{space:string;project:DramaProject;disabled:boolean}){
 const {t}=useI18n();const [open,setOpen]=useState(false)
 if(project.series_episode_id)return project.migration_source?<p className="drama-help">{t('本集来自旧项目第 {p0} 集。',{p0:project.migration_source.episode_number})} <Link to={`/projects/${project.migration_source.project_id}/media`}>{t('查看原项目与旧成片')}</Link></p>:null
 return <details className="panel project-tools" onToggle={event=>setOpen(event.currentTarget.open)}><summary>{t('将旧项目分集复制到系列')}</summary>{open&&<MigrationWorkspace key={`${space}:${project.id}`} space={space} project={project} disabled={disabled}/>}</details>
}
function MigrationWorkspace({space,project,disabled}:{space:string;project:DramaProject;disabled:boolean}){
 const {t}=useI18n();const {session}=useAppWorkspace()
 const readonly=session.spaces?.find(s=>s.id===space)?.role==='viewer'
 const base=`/projects/${project.id}/episode-migrations`
 const [revision,setRevision]=useState(0),[source,setSource]=useState<SourceSummary|null>(null),[sourceError,setSourceError]=useState('')
 const [root,setRoot]=useState(''),[season,setSeason]=useState<SeriesEntry|null>(null),[ep,setEp]=useState(0),[ordinal,setOrdinal]=useState('')
 const [output,setOutput]=useState<'series'|'preserve'>('series'),[board,setBoard]=useState<'include'|'omit'>('include')
 const [assignments,setAssignments]=useState<Record<string,number>>({}),[characters,setCharacters]=useState<Record<string,string>>({})
 const [preview,setPreview]=useState<Plan|null>(null),[fresh,setFresh]=useState(false),[error,setError]=useState(''),[busy,setBusy]=useState(false)
 const [jobs,setJobs]=useState<JobPage>({items:[],next_cursor:''}),[jobError,setJobError]=useState(''),[queued,setQueued]=useState<MigrationJob|null>(null)
 const jobsEpoch=useRef(0)
 const flight=useRef(false),alive=useRef(true),controller=useRef<AbortController|null>(null)
 const roots=useDirectory('/series?status=active',space,revision)
 const onCreated=useCallback((job:MigrationJob)=>{jobsEpoch.current++;setJobs(old=>({...old,items:[job,...old.items.filter(j=>j.id!==job.id)]}));setQueued(job)},[])
 const write=useSeriesWrite<MigrationJob>(`episode-migration:${project.id}`,base,'POST',onCreated)
 useNavigationTask(busy)
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;controller.current?.abort()}},[])
 useEffect(()=>{
  const abort=new AbortController();const epoch=jobsEpoch.current
  void request<SourceSummary>(directoryURL(`/projects/${project.id}/migration-source`,space),{signal:abort.signal}).then(s=>{if(!abort.signal.aborted){setSource(s);setSourceError('');setEp(old=>old||s.episodes[0]||1);setOrdinal(old=>old||String(s.episodes[0]||1))}}).catch(e=>{if(!abort.signal.aborted)setSourceError(errorMessage(e,t('迁移来源读取失败')))})
  void request<JobPage>(directoryURL(base,space),{signal:abort.signal}).then(p=>{if(!abort.signal.aborted&&epoch===jobsEpoch.current){setJobs(p);setJobError('')}}).catch(e=>{if(!abort.signal.aborted)setJobError(errorMessage(e,t('迁移记录读取失败')))})
  return()=>abort.abort()
  // Refresh metadata without discarding the creator's choices on language changes.
  // eslint-disable-next-line react-hooks/exhaustive-deps
 },[base,space,project.id,project.version,revision])
 const run=useCallback(async(job:MigrationJob,cancel=false)=>{
  if(flight.current)return
  flight.current=true;setBusy(true);setError('');const abort=new AbortController();controller.current=abort
  try{
   let current=await request<MigrationJob>(directoryURL(`${base}/${job.id}`,space),{signal:abort.signal})
   while(!abort.signal.aborted&&!['completed','cancelled'].includes(current.state)){
    current=await request<MigrationJob>(directoryURL(`${base}/${job.id}/${cancel?'cancel':'continue'}`,space),{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({expected_version:current.row_version}),signal:abort.signal})
    if(!alive.current)return
    setJobs(old=>({...old,items:old.items.map(j=>j.id===current.id?current:j)}))
    if(cancel)break
   }
   if(alive.current)setRevision(n=>n+1)
  }catch(e){if(!abort.signal.aborted&&alive.current){setError(errorMessage(e,translate('迁移结果尚未确认，请刷新记录后继续同一任务。')));setRevision(n=>n+1)}}
  finally{flight.current=false;if(alive.current)setBusy(false)}
 },[base,space])
 useEffect(()=>{if(!queued)return;const timer=setTimeout(()=>{setQueued(null);void run(queued)},0);return()=>clearTimeout(timer)},[queued,run])
 function changed(){setFresh(false);setError('')}
 async function loadPreview(){
  if(flight.current||!source||!season)return
  flight.current=true;setBusy(true);setError('');const abort=new AbortController();controller.current=abort
  try{
   const latest=await request<SourceSummary>(directoryURL(`/projects/${project.id}/migration-source`,space),{signal:abort.signal})
   const parent=await request<SeriesEntry>(directoryURL(`/seasons/${season.id}`,space),{signal:abort.signal})
   const input:MigrationInput={source_episode_number:ep,target_season_id:parent.id,target_ordinal:Number(ordinal),expected_project_version:latest.project_version,expected_media_version:latest.media_version,expected_season_version:parent.row_version,output_mode:output,storyboard_policy:board,shot_assignments:assignments,character_assignments:characters}
   if(alive.current&&!abort.signal.aborted){setSource(latest);setSeason(parent)}
   const p=await request<Plan>(directoryURL(`/projects/${project.id}/episode-migration-preview`,space),{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:JSON.stringify(input),signal:abort.signal})
   if(!abort.signal.aborted&&alive.current){setSource(latest);setSeason(parent);setPreview(p);setFresh(true)}
  }catch(e){if(!abort.signal.aborted&&alive.current){setFresh(false);setError(errorMessage(e,t('迁移预览失败，源项目保留。')))}}
  finally{flight.current=false;if(alive.current)setBusy(false)}
 }
 async function moreJobs(){
  if(flight.current)return
  flight.current=true;setBusy(true)
  const abort=new AbortController();controller.current=abort
  try{const p=await request<JobPage>(directoryURL(base,space,jobs.next_cursor),{signal:abort.signal});if(alive.current&&!abort.signal.aborted)setJobs(old=>({items:[...old.items,...p.items.filter(j=>!old.items.some(v=>v.id===j.id))],next_cursor:p.next_cursor}))}
  catch(e){if(alive.current&&!abort.signal.aborted)setJobError(errorMessage(e,t('迁移记录读取失败')))}finally{flight.current=false;if(alive.current)setBusy(false)}
 }
 const locked=disabled||busy||write.busy||write.uncertain||readonly
 const characterIssues=preview?.issues.filter(i=>i.code==='character_assignment_required')??[]
 return <div className="episode-migration">
  <p>{t('每集复制为独立项目。原剧本、素材和旧成片保留；不会调用模型，也不会自动发布内容。')}</p>
  <p className="drama-help">{t('离开本页会暂停后续复制，已保存的进度可回来继续。修改源项目后，需要停止旧迁移并重新预览。')}</p>
  {sourceError&&<p className="error" role="alert">{sourceError}</p>}
  <button type="button" disabled={busy} onClick={()=>setRevision(n=>n+1)}>{t('刷新迁移记录')}</button>
  <fieldset disabled={locked||!source} className="series-form">
   <div className="migration-grid"><label>{t('原项目集数')}<select value={ep} onChange={e=>{setEp(Number(e.target.value));setOrdinal(e.target.value);setAssignments({});setCharacters({});changed()}}>{source?.episodes.map(n=><option key={n} value={n}>{t('第 {p0} 集',{p0:n})}</option>)}</select></label>
    <label>{t('复制到系列')}<select value={root} onChange={e=>{setRoot(e.target.value);setSeason(null);changed()}}><option value="">{t('请选择系列')}</option>{roots.items.map(r=><option key={r.id} value={r.id}>{r.title}</option>)}</select></label></div>
   {roots.error&&<p role="alert" className="error">{roots.error}</p>}{roots.next_cursor&&<button type="button" disabled={roots.more} onClick={()=>void roots.loadMore()}>{t('加载更多系列')}</button>}
   {!roots.loading&&!roots.items.length&&!roots.error&&<p>{t('先建立系列与季，再回来复制旧项目。')} <Link to="/series/new">{t('新建系列')}</Link></p>}
   {root&&<MigrationSeasons key={root} root={root} space={space} revision={revision} selected={season?.id??''} onSelect={n=>{setSeason(n);changed()}}/>}
   <div className="migration-grid"><label>{t('目标集号')}<input type="number" min="1" value={ordinal} onChange={e=>{setOrdinal(e.target.value);changed()}}/></label>
    <label>{t('剪辑输出设置')}<select value={output} onChange={e=>{setOutput(e.target.value as 'series'|'preserve');changed()}}><option value="series">{t('使用预览时的系列设置')}</option><option value="preserve">{t('保留旧剪辑的清晰度与画幅')}</option></select></label></div>
   <label>{t('旧分镜处理')}<select value={board} onChange={e=>{setBoard(e.target.value as 'include'|'omit');setAssignments({});setCharacters({});changed()}}><option value="include">{t('按明确归属复制分镜')}</option><option value="omit">{t('分镜留在原项目，目标剪辑解除旧镜头关联')}</option></select></label>
   {board==='include'&&source?.unassigned_shots.map(shot=><label key={shot.id}>{t('镜头归属')} · {shot.label}<select aria-label={`${t('镜头归属')} ${shot.id}`} value={assignments[shot.id]??''} onChange={e=>{setAssignments(old=>({...old,[shot.id]:Number(e.target.value)}));changed()}}><option value="" disabled>{t('请选择归属')}</option><option value="0">{t('留在原项目')}</option><option value={ep}>{t('复制到本次选择的集')}</option></select></label>)}
   {characterIssues.map(issue=><label key={issue.subject}>{t('素材角色归属')} · {preview?.assets.find(a=>a.source.id===issue.subject)?.source.name??issue.subject}<select value={characters[issue.subject!]??'__choose'} onChange={e=>{setCharacters(old=>({...old,[issue.subject!]:e.target.value}));changed()}}><option value="__choose" disabled>{t('请选择归属')}</option><option value="">{t('解除角色关联，保留素材')}</option>{source?.characters.map(c=><option key={c.id} value={c.id}>{c.name}</option>)}</select></label>)}
   <button type="button" disabled={!season||!Number.isSafeInteger(Number(ordinal))||Number(ordinal)<1} onClick={()=>void loadPreview()}>{t('预览本集复制内容')}</button>
  </fieldset>
  {preview&&<section className="editing-note" aria-label={t('迁移预览')}>
   <p>{t('本集包含 {p0} 个场次、{p1} 个镜头、{p2} 份素材。',{p0:preview.script?.scenes.length??0,p1:preview.storyboard?.shots.length??0,p2:preview.assets.length})}</p>
   <p>{t('旧成片继续留在原项目，不会重新编码或复制发布任务。')}</p>
   {preview.issues.map((issue,i)=><p key={i} className="error">{t(issue.message)}</p>)}
   {preview.warnings.map((issue,i)=><p key={i}>{t(issue.message)}</p>)}
   {!fresh&&<p>{t('选择已变化，请重新预览。')}</p>}
   <button type="button" className="primary" disabled={locked||!fresh||Boolean(preview.issues.length)} onClick={()=>void write.save({input:preview.input,preview_digest:preview.digest})}>{t('确认复制本集')}</button>
  </section>}
  {write.uncertain&&<div className="editing-note"><p>{t('创建结果尚未确认。请用原请求核对，避免重复创建。')}</p><button type="button" disabled={busy||write.busy||readonly} onClick={()=>void write.save({})}>{t('核对并重试保存')}</button></div>}
  {write.storageNotice&&<p role="status">{write.storageNotice}</p>}{(error||write.error)&&<div className="error" role="alert">{error||write.error}</div>}
  {busy&&<p role="status">{t('正在处理迁移，请等待当前步骤完成。')} <button type="button" onClick={()=>controller.current?.abort()}>{t('暂停本页处理')}</button></p>}
  <h3>{t('已保存的迁移进度')}</h3>{jobError&&<p className="error" role="alert">{jobError}</p>}
  {!jobs.items.length&&!jobError&&<p className="drama-help">{t('还没有迁移任务。先预览一集，确认后开始复制。')}</p>}
  {jobs.items.map(j=><article className="migration-job" key={j.id}>
   <h4>{t('第 {p0} 集',{p0:j.source_episode_number})} → {t('第 {p0} 集',{p0:j.target_ordinal})}</h4>
   <p>{j.state==='completed'?t('复制完成，可独立编辑'):j.state==='cancelled'?t('已停止，未完成目标已移除'):t('已保留进度，可继续')}</p>
   {j.total_assets>0?<><progress aria-label={t('已复制素材')} value={j.copied_assets} max={j.total_assets}/><span>{j.copied_assets} / {j.total_assets} {t('份素材')}</span></>:<p className="drama-help">{t('本集无需复制媒体素材。')}</p>}
   {j.error&&<p className="error">{t(j.error)}</p>}
   {j.state==='completed'?<Link to={`/projects/${j.target_project_id}/brief`}>{t('打开独立剧集')}</Link>:j.state!=='cancelled'&&<div className="media-actions"><button type="button" disabled={locked} onClick={()=>void run(j)}>{t('继续迁移')}</button><button type="button" disabled={locked} onClick={()=>void run(j,true)}>{t('停止并移除未完成目标')}</button></div>}
  </article>)}
  {jobs.next_cursor&&<button type="button" disabled={busy} onClick={()=>void moreJobs()}>{t('加载更多迁移记录')}</button>}
 </div>
}
function MigrationSeasons({root,space,revision,selected,onSelect}:{root:string;space:string;revision:number;selected:string;onSelect:(n:SeriesEntry|null)=>void}){
 const {t}=useI18n();const seasons=useDirectory(`/series/${root}/seasons?status=active`,space,revision)
 return <><label>{t('目标季')}<select value={selected} disabled={seasons.loading} onChange={e=>onSelect(seasons.items.find(n=>n.id===e.target.value)??null)}><option value="">{t('请选择季')}</option>{seasons.items.map(n=><option key={n.id} value={n.id}>{t('第 {p0} 季',{p0:n.ordinal??1})}{n.title?` · ${n.title}`:''}</option>)}</select></label>{seasons.error&&<p className="error" role="alert">{seasons.error}</p>}{seasons.next_cursor&&<button type="button" disabled={seasons.more} onClick={()=>void seasons.loadMore()}>{t('加载更多季')}</button>}{!seasons.loading&&!seasons.items.length&&!seasons.error&&<Link to={`/series/${root}`}>{t('去系列目录添加一季')}</Link>}</>
}
