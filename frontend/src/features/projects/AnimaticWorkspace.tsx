import {useCallback,useEffect,useState} from 'react'
import {Link} from 'react-router-dom'
import {ApiError,baseURL,errorMessage,request} from '../../api'
import {useI18n} from '../../i18n'
import {useAppWorkspace} from '../../app/workspace'
import type {DramaProject} from '../../drama'
import {adoptedPreviewChoices,eligiblePreviewShots,type PreviewChoice,type PreviewDetail,type PreviewInput,type PreviewMedia,type PreviewSummary,type PreviewSource,type PreviewPlan} from '../../animatic'
import {readSeriesRecovery,useSeriesWrite} from '../series/useSeriesWrite'
import {DirectorDirection} from './DirectorWorkspace'

const previewStatus:Record<string,string>={queued:'等待合成',running:'正在合成',succeeded:'预演已完成',failed:'预演未完成',cancelled:'已取消'}
// A CAS rejection is authoritative only for this replay-first endpoint. Other
// failures, including revoked membership, leave the original request uncertain.
function definitivePreviewRejection(e:unknown){return e instanceof ApiError&&e.status===409&&e.code==='version_conflict'}
export function AnimaticWorkspace({project,space,disabled,onDirtyChange}:{project:DramaProject;space:string;disabled:boolean;onDirtyChange:(dirty:boolean)=>void}){
 const {t}=useI18n(),{session}=useAppWorkspace(),path=`/projects/${project.id}/media/previews`,identity=`animatic:${project.id}`
 const [open,setOpen]=useState(()=>Boolean(readSeriesRecovery(session.user?.id??'demo-user',space,identity,path,'POST'))),[revision,setRevision]=useState(0)
 const saved=useCallback(()=>setRevision(v=>v+1),[])
 return <details className="panel project-tools" open={open} onToggle={e=>setOpen(e.currentTarget.open)}><summary>{t('静帧预演 · 先看节奏')}</summary>{open&&<>
  <p>{t('用已保存的图片排成带预演标识的视频，先判断叙事和停顿。它是本地合成，不是视频模型生成，也不会替换正式剪辑。')}</p>
  <PreviewPlanner project={project} space={space} disabled={disabled} onDirtyChange={onDirtyChange} onCreated={saved}/>
  <PreviewHistory key={revision} project={project.id} space={space} disabled={disabled}/>
 </>}</details>
}
function PreviewPlanner({project,space,disabled,onDirtyChange,onCreated}:{project:DramaProject;space:string;disabled:boolean;onDirtyChange:(v:boolean)=>void;onCreated:()=>void}){
 const {t}=useI18n(),workspace=useAppWorkspace(),path=`/projects/${project.id}/media/previews`
 const write=useSeriesWrite<PreviewDetail>(`animatic:${project.id}`,path,'POST',onCreated,definitivePreviewRejection)
 const original=write.recovery as unknown as PreviewInput|null
 const [basis,setBasis]=useState<{project:PreviewPlan;media:PreviewMedia;shot_episodes:Record<string,number>}|null>(null),[loading,setLoading]=useState(true),[readError,setReadError]=useState(''),[readRevision,setReadRevision]=useState(0),[refreshed,setRefreshed]=useState(false)
 const [episode,setEpisode]=useState(original?.episode_number??1),[aspect,setAspect]=useState(original?.aspect_ratio??'16:9'),[sound,setSound]=useState<'silent'|'temporary'>(original?.sound_mode??'silent'),[choices,setChoices]=useState<PreviewChoice[]>(original?.shots??[]),[add,setAdd]=useState('')
 const readOnly=workspace.space?.role==='viewer',locked=disabled||readOnly||write.busy||write.uncertain||loading
 useEffect(()=>{const abort=new AbortController();void request<{project:PreviewPlan;media:PreviewMedia;shot_episodes:Record<string,number>}>(`/projects/${project.id}/media/previews/options?space_id=${space}`,{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setBasis(v);setLoading(false);setReadError('');if(readRevision)setRefreshed(true);else if(!original)setAspect(v.media.aspect_ratio||'16:9')}}).catch(e=>{if(!abort.signal.aborted){setLoading(false);setReadError(errorMessage(e,t('预演来源读取失败')))}});return()=>abort.abort()
 // Refreshing sources preserves the creator's choices, order and timing.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[project.id,space,readRevision])
 useEffect(()=>{onDirtyChange(write.dirty||write.busy||write.uncertain);return()=>onDirtyChange(false)},[onDirtyChange,write.dirty,write.busy,write.uncertain])
 function update(next:PreviewChoice[]){setChoices(next);write.markDirty()}
 function move(index:number,step:number){const next=[...choices];[next[index],next[index+step]]=[next[index+step],next[index]];update(next)}
 const eligible=basis?eligiblePreviewShots(basis.project,episode,basis.shot_episodes):[],images=basis?.media.assets.filter(a=>a.kind==='image'&&a.role!=='mask')??[],audio=basis?.media.assets.filter(a=>a.kind==='audio')??[]
 const valid=Boolean(basis&&Number.isSafeInteger(episode)&&episode>=1&&choices.length&&choices.every(c=>eligible.some(s=>s.id===c.shot_id)&&images.some(a=>a.id===c.asset_id)&&Number.isFinite(c.duration)&&c.duration>=.5&&(!c.audio_asset_id||sound==='temporary'&&audio.some(a=>a.id===c.audio_asset_id)))&&(sound==='silent'||choices.some(c=>c.audio_asset_id)))

 const body:PreviewInput={expected_project_version:basis?.project.version??0,expected_media_version:basis?.media.version??0,episode_number:episode,aspect_ratio:aspect,sound_mode:sound,shots:choices}
 return <section className="series-form" aria-label={t('规划静帧预演')}><h3>{t('安排本次预演')}</h3>
  {loading&&<p role="status">{t('正在读取预演来源…')}</p>}{readError&&<p role="alert" className="error">{readError}</p>}
  <button type="button" disabled={write.busy||write.uncertain||disabled} onClick={()=>{setLoading(true);setReadRevision(v=>v+1)}}>{t('重新核对分镜与素材')}</button>
  {refreshed&&<p className="editing-note" role="status">{t('已读取最新分镜和素材，保留了你的选片、顺序和时长。请核对变更后再创建预演。')}</p>}
  {basis&&Object.values(basis.shot_episodes).some(n=>n<0)&&<p className="editing-note" role="status">{t('部分场次镜头的旧剧本来源无法核对，暂不列为可选。可先查看版本历史或明确建立独立镜头。')}</p>}
  {basis&&<p>{t('来源：项目版本 {project} · 素材版本 {media}',{project:basis.project.version,media:basis.media.version})}</p>}
  {write.error&&<p role="alert" className="error">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}
  {write.uncertain&&<div className="editing-note"><p>{t('上次预演创建结果待核对。重试沿用原请求，不会调用模型或创建第二份预演。')}</p><button type="button" disabled={write.busy||readOnly||disabled} onClick={()=>void write.save(body)}>{t('核对原预演请求')}</button></div>}
  <fieldset disabled={locked}><div className="storyboard-fields">
   {!project.series_episode_id&&<label>{t('预演集数')}<input aria-label={t('预演集数')} type="number" min="1" step="1" value={episode||''} onChange={e=>{setEpisode(Number(e.target.value));write.markDirty()}}/></label>}
   <label>{t('预演画幅')}<select aria-label={t('预演画幅')} value={aspect} onChange={e=>{setAspect(e.target.value);write.markDirty()}}>{['16:9','9:16','1:1'].map(v=><option key={v}>{v}</option>)}</select></label>
   <label>{t('预演声音')}<select aria-label={t('预演声音')} value={sound} onChange={e=>{const next=e.target.value as 'silent'|'temporary';setSound(next);update(choices.map(v=>next==='silent'?{...v,audio_asset_id:undefined}:v))}}><option value="silent">{t('明确使用静音')}</option><option value="temporary">{t('使用选定临时声轨')}</option></select></label>
  </div><p>{t('临时声轨从头播放，超出镜头部分会截断，不足处静音。声音意图和剧本对白不会自动生成声音。')}</p>
  {basis&&!basis.project.storyboard?.shots.length&&<p className="page-empty">{t('先保存镜头计划，再安排静帧预演。')}</p>}
  {!choices.length&&<p className="page-empty">{t('还没有选择本次预演的镜头。可以从已采用图片开始，或逐镜加入。')}</p>}
  <button type="button" onClick={()=>basis&&workspace.requestLeave(()=>update(adoptedPreviewChoices(basis.project,basis.media,episode,basis.shot_episodes)))}>{t('用已采用图片规划本集')}</button>
  <p>{t('也可以逐镜选片。独立镜头需自行确认本次集归属；保存预演不会改动原镜头归属。')}</p>
  <div className="storyboard-shots">{choices.map((choice,index)=>{const shot=basis?.project.storyboard?.shots.find(s=>s.id===choice.shot_id);return <article className="storyboard-shot" key={`${choice.shot_id}:${index}`}><h4>{t('预演第 {p0} 镜',{p0:index+1})}</h4><p>{shot?.description??t('这个镜头已从当前计划移除，请保留来源核对后移除本次选择。')}</p>{shot&&!eligible.some(s=>s.id===shot.id)&&<p className="error">{t('这个镜头不属于当前预演集，请调整集号或移除此选择。')}</p>}<div className="media-actions"><button type="button" disabled={index===0} onClick={()=>move(index,-1)}>{t('前移预演镜头')}</button><button type="button" disabled={index===choices.length-1} onClick={()=>move(index,1)}>{t('后移预演镜头')}</button><button type="button" onClick={()=>update(choices.filter((_,i)=>i!==index))}>{t('移除此预演镜头')}</button></div>
   <label>{t('预演图片 {p0}',{p0:index+1})}<select aria-label={t('预演图片 {p0}',{p0:index+1})} value={choice.asset_id} onChange={e=>update(choices.map((v,i)=>i===index?{...v,asset_id:e.target.value}:v))}><option value="">{t('选择本项目图片')}</option>{images.map(a=><option key={a.id} value={a.id}>{a.name}</option>)}</select></label>
   <label>{t('预演时长 {p0}（秒）',{p0:index+1})}<input aria-label={t('预演时长 {p0}（秒）',{p0:index+1})} type="number" min="0.5" step="0.1" value={choice.duration||''} onChange={e=>update(choices.map((v,i)=>i===index?{...v,duration:Number(e.target.value)}:v))}/></label>
   {sound==='temporary'&&<label>{t('预演临时声轨 {p0}',{p0:index+1})}<select aria-label={t('预演临时声轨 {p0}',{p0:index+1})} value={choice.audio_asset_id??''} onChange={e=>update(choices.map((v,i)=>i===index?{...v,audio_asset_id:e.target.value||undefined}:v))}><option value="">{t('本镜静音')}</option>{audio.map(a=><option key={a.id} value={a.id}>{a.name}</option>)}</select></label>}
  </article>})}</div>
  <label>{t('添加预演镜头')}<select aria-label={t('添加预演镜头')} value={add} onChange={e=>setAdd(e.target.value)}><option value="">{t('选择已保存的镜头')}</option>{eligible.map(s=><option key={s.id} value={s.id}>{s.description}</option>)}</select></label><button type="button" disabled={!add} onClick={()=>{const s=eligible.find(s=>s.id===add);if(s){update([...choices,{shot_id:s.id,asset_id:'',duration:s.duration_seconds}]);setAdd('')}}}>{t('加入预演')}</button>
  {!images.length&&<p className="page-empty">{t('还没有可用图片。请先生成或上传图片；预演不会补入模拟画面。')} <Link to={`/projects/${project.id}/production`}>{t('前往镜头制作')}</Link></p>}
  <p>{t('本次约 {p0} 秒 · 360p 预演；正式成片画质在剪辑中单独选择。',{p0:choices.reduce((n,c)=>n+(Number.isFinite(c.duration)?c.duration:0),0).toFixed(1)})}</p>
  {!valid&&choices.length>0&&<p className="drama-validation" role="status">{t('请为每镜选择有效图片和时长；临时声音模式至少选择一份声轨。')}</p>}
  <button type="button" className="primary" disabled={!valid||Boolean(readError)} onClick={()=>void write.save(body)}>{t(write.busy?'正在创建预演…':'创建独立静帧预演')}</button>
  </fieldset></section>
}

function PreviewHistory({project,space,disabled}:{project:string;space:string;disabled:boolean}){
 const {t}=useI18n(),[page,setPage]=useState<{items:PreviewSummary[];next_cursor:string}>({items:[],next_cursor:''}),[cursors,setCursors]=useState(['']),[refresh,setRefresh]=useState(0),[error,setError]=useState(''),[loading,setLoading]=useState(true)
 const cursor=cursors.at(-1)??''
 useEffect(()=>{const abort=new AbortController();void request<typeof page>(`/projects/${project}/media/previews?space_id=${space}&limit=20${cursor?'&cursor='+encodeURIComponent(cursor):''}`,{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setPage(v);setError('');setLoading(false)}}).catch(e=>{if(!abort.signal.aborted){setError(errorMessage(e,t('预演历史读取失败')));setLoading(false)}});return()=>abort.abort()},[project,space,cursor,refresh,t])
 const active=page.items.some(v=>v.status==='queued'||v.status==='running')
 useEffect(()=>{if(!active)return;const timer=setInterval(()=>setRefresh(v=>v+1),2500);return()=>clearInterval(timer)},[active])
 const changed=useCallback(()=>setRefresh(v=>v+1),[])
 return <section className="media-exports" aria-label={t('已保存的静帧预演')}><div className="section-heading"><h3>{t('已保存的静帧预演')}</h3><button type="button" onClick={()=>setRefresh(v=>v+1)}>{t('刷新预演状态')}</button></div>{error&&<p role="alert" className="error">{error}</p>}{loading&&<p role="status">{t('正在读取预演历史…')}</p>}{!loading&&!error&&!page.items.length&&<p className="page-empty">{t('还没有静帧预演。先安排选片与节奏，再创建一份独立预演。')}</p>}{page.items.map(v=><PreviewCard key={v.id} project={project} space={space} value={v} onChanged={changed} disabled={disabled}/>)}<div className="media-actions"><button type="button" disabled={cursors.length<2} onClick={()=>{setLoading(true);setCursors(v=>v.slice(0,-1))}}>{t('返回较新预演')}</button><button type="button" disabled={!page.next_cursor} onClick={()=>{setLoading(true);setCursors(v=>[...v,page.next_cursor])}}>{t('查看较早预演')}</button></div></section>
}
function PreviewCard({project,space,value,onChanged,disabled}:{project:string;space:string;value:PreviewSummary;onChanged:()=>void;disabled:boolean}){
 const {t,formatDate}=useI18n(),{space:workspace}=useAppWorkspace(),[source,setSource]=useState<PreviewSource|null>(null),[readError,setReadError]=useState(''),[reading,setReading]=useState(false),path=`/projects/${project}/media/previews/${value.id}`
 const canceled=useCallback(()=>onChanged(),[onChanged]),resumed=useCallback(()=>onChanged(),[onChanged])
 const cancel=useSeriesWrite<PreviewDetail>(`animatic:${value.id}:cancel`,path+'/cancel','POST',canceled),resume=useSeriesWrite<PreviewDetail>(`animatic:${value.id}:resume`,path+'/resume','POST',resumed)
 const locked=disabled||workspace?.role==='viewer'||cancel.busy||resume.busy
 async function read(){setReading(true);setReadError('');try{const d=await request<PreviewDetail>(path+`?space_id=${space}`);setSource(d.preview_source)}catch(e){setReadError(errorMessage(e,t('预演来源读取失败')))}finally{setReading(false)}}
 return <article><h4>{t(previewStatus[value.status]??'状态待核对')} · {formatDate(value.created_at)}</h4><p>{t('静帧预演 · {duration} 秒 · {sound}',{duration:value.duration.toFixed(1),sound:t(value.sound_mode==='silent'?'明确使用静音':'使用选定临时声轨')})}</p>{value.render_progress&&value.status==='running'&&<p role="status">{t('已完成 {completed}/{total} 段画面与声音',{completed:value.render_progress.completed,total:value.render_progress.total})}</p>}{value.error&&<p role="alert" className="error">{value.error}</p>}{value.status==='succeeded'&&value.file_url&&<><video controls preload="metadata" aria-label={t('独立静帧预演播放器')} src={baseURL+value.file_url}/><a className="button-link" download={`animatic-${value.id}.mp4`} href={baseURL+value.file_url+'&download=1'}>{t('下载带标识的预演')}</a></>}
  <p className="drama-help">{t('预演仅用于审视节奏，不是正式成片，不能直接进入发布流程。正式剪辑与已采用素材保持不变。')}</p>
  {(value.status==='queued'||value.status==='running'||cancel.uncertain)&&<button type="button" disabled={locked||resume.uncertain} onClick={()=>void cancel.save({})}>{t(cancel.uncertain?'核对原取消请求':'取消这次预演合成')}</button>}
  {((value.resume_available&&(value.status==='failed'||value.status==='cancelled'))||resume.uncertain)&&<><p>{t('继续只会使用这份预演固定的图片和声轨，不会重新调用模型或使用后来改动的时间轴。')}</p><button type="button" disabled={locked||cancel.uncertain} onClick={()=>void resume.save({})}>{t(resume.uncertain?'核对原继续请求':'继续这次预演合成')}</button></>}
  {(cancel.error||resume.error)&&<p role="alert" className="error">{cancel.error||resume.error}</p>}
  <details><summary>{t('查看固定镜头与声音来源')}</summary><button type="button" disabled={reading} onClick={()=>void read()}>{t(reading?'正在读取资料…':'读取预演来源')}</button>{readError&&<p role="alert" className="error">{readError}</p>}{source&&<><p>{t('来源：项目版本 {project} · 素材版本 {media}',{project:source.project_version,media:source.media_version})}</p>{source.shots.map((s,i)=><section key={`${s.shot.id}:${i}`}><h5>{t('预演第 {p0} 镜',{p0:i+1})}</h5><p>{s.shot.description} · {s.duration}s</p><p>{source.assets.find(a=>a.id===s.asset_id)?.name} · {s.audio_asset_id?source.assets.find(a=>a.id===s.audio_asset_id)?.name:t('本镜静音')}</p><DirectorDirection shot={s.shot}/></section>)}</>}</details>
 </article>
}
