import {hasPermission} from '../../permissions'
import {useCallback,useEffect,useRef,useState} from 'react'
import {request,errorMessage} from '../../api'
import {useAppWorkspace} from '../../app/workspace'
import {useI18n} from '../../i18n'
import {RuntimeModelPicker} from '../../components/RuntimeModelPicker'
import {runtimeFields,runtimeReady,type RuntimeChoice} from '../../runtimeSelection'
import {StoryBodyView} from './StoryBodyView'
import {StoryFields} from './StoryFields'
import {useSeriesWrite} from './useSeriesWrite'
import {directoryURL} from './types'
import type {StoryBody,StoryInput,StoryOutline,StoryVersion} from './storyTypes'

interface Source {season_id:string;episode_id?:string;anchor_project_id:string;project_version:number;source_sha256:string;material:StoryVersion[];episodes:{id:string;title:string;summary:string;ordinal:number}[]}
interface Summary {id:string;project_id:string;status:string;prompt:string;title?:string;model:string;created_at:string;error?:string;source_sha256:string}
interface Candidate {id:string;project_id:string;status:string;prompt:string;model:string;created_at:string;error?:string;error_code?:string;outline_context:Source;outline_result?:{title:string;outline:StoryOutline;rationale:string}}

export function OutlineCandidates({season,episode,onAdopted}:{season:string;episode?:string;onAdopted:()=>void}){
 const {t}=useI18n(),[opened,setOpened]=useState(false)
 return <details className="story-snapshots" onToggle={e=>{if(e.currentTarget.open)setOpened(true)}}><summary>{t('AI 季集纲候选 · 先比较再采纳')}</summary>{opened&&<CandidateBoard season={season} episode={episode} onAdopted={onAdopted}/>}</details>
}
function CandidateBoard({season,episode,onAdopted}:{season:string;episode?:string;onAdopted:()=>void}){
 const {t}=useI18n(),{space,requestLeave}=useAppWorkspace(),[source,setSource]=useState<Source|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false),[choice,setChoice]=useState<RuntimeChoice|null>(null),[job,setJob]=useState<Candidate|null>(null),[items,setItems]=useState<Summary[]>([]),[cursor,setCursor]=useState(''),[revision,setRevision]=useState(0)
 const path=`/seasons/${season}/outline-candidates`,scope=(p:string)=>directoryURL(p,space!.id)+(episode?'&episode_id='+episode:'')
 const accepted=useCallback((v:Candidate)=>{setJob(v);setRevision(n=>n+1)},[])
 const write=useSeriesWrite<Candidate>(`outline-create:${season}:${episode??'season'}`,path+(episode?'?episode_id='+episode:''),'POST',accepted)
 const [brief,setBrief]=useState(()=>String(write.recovery?.prompt??'')),live=useRef(true),flight=useRef(false)
 useEffect(()=>{live.current=true;return()=>{live.current=false}},[])
 useEffect(()=>{const abort=new AbortController();void request<{items:Summary[];next_cursor:string}>(scope(path),{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setItems(v.items);setCursor(v.next_cursor)}}).catch(e=>{if(!abort.signal.aborted)setError(errorMessage(e,t('候选列表读取失败')))});return()=>abort.abort()
 // Switching UI language must not erase a brief or repeat an accepted request.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[space?.id,season,episode,revision])
 useEffect(()=>{
  if(!job||!['queued','running'].includes(job.status))return
  const abort=new AbortController();let timer:ReturnType<typeof setTimeout>
  async function poll(){try{const v=await request<Candidate>(directoryURL(`/projects/${job!.project_id}/creative-jobs/${job!.id}`,space!.id),{signal:abort.signal});if(!abort.signal.aborted){setJob(v);if(['queued','running'].includes(v.status))timer=setTimeout(()=>void poll(),1000);else setRevision(n=>n+1)}}catch(e){if(!abort.signal.aborted)setError(errorMessage(e,t('候选状态读取失败，请刷新列表核对。')))}}
  timer=setTimeout(()=>void poll(),1000);return()=>{abort.abort();clearTimeout(timer)}
 // A job is polled under its immutable project and scope.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[space?.id,job?.id,job?.status])
 async function prepare(){if(flight.current)return;flight.current=true;setBusy(true);setError('');try{const next=await request<Source>(scope(path+'/source'));if(live.current)setSource(next)}catch(e){if(live.current)setError(errorMessage(e,t('策划来源读取失败，请先添加剧集并核对正式资料。')))}finally{flight.current=false;if(live.current)setBusy(false)}}
 async function more(){if(flight.current||!cursor)return;flight.current=true;setBusy(true);try{const next=await request<{items:Summary[];next_cursor:string}>(scope(path)+'&cursor='+cursor);if(live.current){setItems(old=>[...old,...next.items.filter(v=>!old.some(o=>o.id===v.id))]);setCursor(next.next_cursor)}}catch(e){if(live.current)setError(errorMessage(e,t('候选列表读取失败')))}finally{flight.current=false;if(live.current)setBusy(false)}}
 async function read(item:Summary){if(flight.current)return;flight.current=true;setBusy(true);setError('');try{const next=await request<Candidate>(directoryURL(`/projects/${item.project_id}/creative-jobs/${item.id}`,space!.id));if(live.current)setJob(next)}catch(e){if(live.current)setError(errorMessage(e,t('候选状态读取失败，请刷新列表核对。')))}finally{flight.current=false;if(live.current)setBusy(false)}}
 const locked=!hasPermission(space,'workspace.write')||write.busy||write.uncertain
 const modelPreview={operation:'script' as const,project_id:source?.anchor_project_id,input:{}}
 return <section className="series-form outline-candidates" aria-label={t('集纲候选工作区')}><p>{t('围绕人物目标、冲突升级和结尾钩子准备一个候选。使用剧本生成绑定的真实文本模型，固定故事圣经、季集计划与目录来源；建议不会自动改写正式资料或生成视频。')}</p>
 <details className="outline-new" open={!job||write.uncertain}><summary>{t('准备新的集纲候选')}</summary><button type="button" disabled={busy||locked} onClick={()=>void prepare()}>{t(busy?'正在读取来源…':'核对本次策划来源')}</button>
 {source&&<details><summary>{t('本次固定来源 · {count} 份正式计划',{count:source.material.length})}</summary><p>{t('来源摘要：{sha}',{sha:source.source_sha256.slice(0,12)})}</p>{source.material.map(v=><p key={v.id}>{v.title} · {t('版本 {p0}',{p0:v.number})}</p>)}{!source.material.length&&<p>{t('尚无故事圣经或季集计划，模型只能参考目录简介；请人工核对人物与连续性。')}</p>}<ol>{source.episodes.map(v=><li key={v.id}>{v.title} · {v.summary}</li>)}</ol></details>}
 <form onSubmit={e=>{e.preventDefault();void write.save({anchor_project_id:source?.anchor_project_id,expected_project_version:source?.project_version,source_sha256:source?.source_sha256,prompt:brief,...runtimeFields(choice)})}}><fieldset disabled={locked}><label>{t('这次希望怎样推进故事')}<textarea required minLength={3} maxLength={2000} value={brief} onChange={e=>{setBrief(e.target.value);write.markDirty()}}/></label><RuntimeModelPicker space={space!.id} capability="text" gatewayOnly value={choice} onChange={setChoice} preview={modelPreview} disabled={locked}/></fieldset>
 {write.error&&<p role="alert" className="error">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}{write.uncertain&&<p className="editing-note">{t('请求结果未确认，请使用原请求核对，不要重新生成。')}</p>}
 {hasPermission(space,'workspace.write')&&<button type="submit" className="primary" disabled={write.busy||(!write.uncertain&&(!source||!choice||!runtimeReady(choice,modelPreview)||brief.trim().length<3))}>{t(write.busy?'正在提交…':write.uncertain?'使用原请求核对结果':'生成集纲候选')}</button>}</form></details>
 {error&&<p role="alert" className="error">{error}</p>}
 {job&&<article className="outline-candidate"><p role="status">{t(({queued:'集纲候选等待执行',running:'正在生成集纲候选',succeeded:'集纲候选已保存',failed:'集纲候选未完成',cancelled:'集纲候选已取消'} as Record<string,string>)[job.status]??'状态待核对')} · {job.model}</p>{job.error&&<p role="alert" className="error">{job.error}</p>}<OutlineCancellation key={job.id} job={job} onChanged={accepted}/>{job.outline_result&&<OutlineAdoption key={job.id} job={job} space={space!.id} season={season} episode={episode} onAdopted={onAdopted}/>}</article>}
 <div className="series-heading"><h3>{t('已保存的集纲候选')}</h3><button type="button" disabled={busy} onClick={()=>setRevision(n=>n+1)}>{t('刷新候选列表')}</button></div>
 {!items.length&&<p className="page-empty">{t('还没有集纲候选。也可以继续手动编写正式节拍。')}</p>}
 {items.map(v=><article className="story-card" key={v.id}><div><h4>{v.title??v.prompt}</h4><p>{v.model} · {new Date(v.created_at).toLocaleString()}</p><p>{v.error??t(({queued:'集纲候选等待执行',running:'正在生成集纲候选',succeeded:'集纲候选已保存',failed:'集纲候选未完成',cancelled:'集纲候选已取消'} as Record<string,string>)[v.status]??'状态待核对')}</p></div><button type="button" disabled={busy||write.busy||write.uncertain} onClick={()=>requestLeave(()=>void read(v))}>{t('查看与比较集纲候选')}</button></article>)}{cursor&&<button type="button" disabled={busy} onClick={()=>void more()}>{t('加载更多集纲候选')}</button>}
 </section>
}
function OutlineAdoption({job,space,season,episode,onAdopted}:{job:Candidate;space:string;season:string;episode?:string;onAdopted:()=>void}){
 const {t}=useI18n(),{space:currentSpace}=useAppWorkspace(),[current,setCurrent]=useState<StoryVersion|null>(null),[error,setError]=useState(''),[loaded,setLoaded]=useState(false),[confirmed,setConfirmed]=useState(false),[adopted,setAdopted]=useState(false),[revision,setRevision]=useState(0)
 const saved=useCallback(()=>{setAdopted(true);setRevision(v=>v+1);onAdopted()},[onAdopted])
 const path=`/seasons/${season}/outline-candidates/${job.id}/adopt`+(episode?'?episode_id='+episode:'')
 const write=useSeriesWrite<StoryVersion>(`outline-adopt:${job.id}`,path,'POST',saved)
 const seed=(write.recovery?.story as StoryInput|undefined),[title,setTitle]=useState(()=>seed?.title??job.outline_result!.title),[body,setBody]=useState<StoryBody>(()=>structuredClone(seed?.body??{evidence:[],outline:job.outline_result!.outline}))
 useEffect(()=>{const abort=new AbortController();void request<{current?:StoryVersion}>(directoryURL(`/seasons/${season}/writing-beats`,space)+(episode?'&episode_id='+episode:''),{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setCurrent(v.current??null);setLoaded(true);setError('')}}).catch(e=>{if(!abort.signal.aborted)setError(errorMessage(e,t('节拍读取失败')))});return()=>abort.abort()},[space,season,episode,t,revision])
 const locked=!hasPermission(currentSpace,'workspace.write')||write.busy||write.uncertain||adopted
 return <section aria-label={t('比较与采纳集纲候选')}><p>{job.outline_result!.rationale}</p><p className="drama-help">{t('候选来源摘要：{sha}。编辑建议并明确确认后，才追加正式节拍版本。来源或正式计划更新时会拒绝覆盖，原候选始终保留。',{sha:job.outline_context.source_sha256.slice(0,12)})}</p>
 <div className={current?'migration-grid':'outline-single'}><section><h4>{t('当前正式节拍')}</h4>{current?<StoryBodyView body={current.body}/>:<p>{t(loaded?'尚无正式节拍':'正在读取故事资料…')}</p>}</section><form onSubmit={e=>{e.preventDefault();void write.save({anchor_project_id:job.project_id,confirmed:true,story:{expected_version:seed?.expected_version??current?.number??0,title,kind:'outline',status:'active',required:true,body}})}}><fieldset disabled={locked}><label>{t('候选计划标题')}<input required value={title} onChange={e=>{setTitle(e.target.value);setConfirmed(false);write.markDirty()}}/></label><StoryFields body={body} onChange={v=>{setBody(v);setConfirmed(false);write.markDirty()}} characters={[]} episodes={[]}/><label className="story-check"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/>{t('我已比较并确认将本候选保存为正式计划')}</label></fieldset>
 {(error||write.error)&&<p role="alert" className="error">{error||write.error}{error&&<button type="button" onClick={()=>setRevision(v=>v+1)}>{t('重新读取资料')}</button>}</p>}{write.uncertain&&<p className="editing-note">{t('采纳结果未确认，请使用原请求核对；不会重复覆盖后续编辑。')}</p>}{adopted&&<p role="status">{t('集纲已保存为正式节拍，原候选和历史版本保留。')}</p>}
 {hasPermission(currentSpace,'workspace.write')&&!adopted&&<button type="submit" disabled={write.busy||(!write.uncertain&&(!confirmed||!loaded||!!error))}>{t(write.uncertain?'使用原请求核对结果':'确认采纳集纲')}</button>}</form></div></section>
}

function OutlineCancellation({job,onChanged}:{job:Candidate;onChanged:(job:Candidate)=>void}){
 const {t}=useI18n(),{space}=useAppWorkspace()
 const write=useSeriesWrite<Candidate>(`outline-cancel:${job.id}`,`/projects/${job.project_id}/creative-jobs/${job.id}/cancel`,'POST',onChanged)
 if(!hasPermission(space,'workspace.write')||(!write.uncertain&&!['queued','running'].includes(job.status)))return null
 return <section><p className="drama-help">{t('停止本地等待不会撤销已发送的模型请求；结果或费用可能仍需向网关核对。')}</p>{write.error&&<p role="alert" className="error">{write.error}</p>}<button type="button" disabled={write.busy} onClick={()=>void write.save({})}>{t(write.uncertain?'使用原请求核对结果':'取消创作任务')}</button></section>
}
