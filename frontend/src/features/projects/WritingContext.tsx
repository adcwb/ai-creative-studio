import {hasPermission} from '../../permissions'
import {StoryContextSourcePicker} from '../series/StoryContextSourcePicker'
import {useSeriesEpisode} from '../series/episodeContext'
import {StoryEvidenceViewer} from '../series/StoryEvidenceViewer'
import {useAppWorkspace} from '../../app/workspace'
import {useCallback,useEffect,useRef,useState} from 'react'
import {request,errorMessage} from '../../api'
import {useI18n} from '../../i18n'
import {useSeriesWrite} from '../series/useSeriesWrite'
import {directoryURL} from '../series/types'
import {StoryBodyView} from '../series/StoryBodyView'
import type {StoryContextSnapshot,StoryContextFreshness} from '../series/storyTypes'

export interface WritingReview {id:string;ready:boolean;changed:boolean}
const noReview:WritingReview={id:'',ready:false,changed:false}
function ContextMaterial({snapshot}:{snapshot:StoryContextSnapshot}){const {t}=useI18n(),{space}=useAppWorkspace();return <div><p>{t('本集纳入 {p0} 份资料，省略 {p1} 份。',{p0:snapshot.context.included.length,p1:snapshot.context.omitted.length})}</p>{snapshot.context.problems.map((p,i)=><p className="editing-note" key={i}>{t(p.message)}</p>)}{snapshot.context.included.map(v=><details key={v.id}><summary>{v.title} · {t('版本 {p0}',{p0:v.number})}</summary><StoryBodyView body={v.body}/><StoryEvidenceViewer root={snapshot.series_id} space={space!.id} value={v.body.evidence}/>{v.kind==='fact'&&<p>{v.audience_knows?t('观众在本集已知'):t('观众在本集尚未知情')}</p>}</details>)}{!!snapshot.context.omitted.length&&<details><summary>{t('查看省略原因')}</summary>{snapshot.context.omitted.map(v=><p key={v.id}>{snapshot.context.sources.find(s=>s.id===v.id)?.title} · {t(({archived:'资料已归档',future_event:'叙事尚未出现的事件',outside_episode_range:'不在本集适用范围',other_episode_cast:'其他剧集的选角',other_writing_scope:'其他季集的节拍',input_capacity:'超过本次输入容量',not_retrieved:'未命中本次检索条件'} as Record<string,string>)[v.reason]??'资料未纳入本集')}</p>)}</details>}</div>}

export function WritingContextPicker({episode,space,projectVersion,disabled,onChange}:{episode:string;space:string;projectVersion:number;disabled:boolean;onChange:(v:WritingReview)=>void}){
 const {t}=useI18n(),[enabled,setEnabled]=useState(false),[snapshot,setSnapshot]=useState<StoryContextSnapshot|null>(null),[fresh,setFresh]=useState(false),[error,setError]=useState('')
 const version=useRef(projectVersion);useEffect(()=>{version.current=projectVersion},[projectVersion])
 const accepted=useCallback((v:StoryContextSnapshot)=>{setSnapshot(v);setFresh(v.project_version===version.current);setEnabled(true)},[])
 const write=useSeriesWrite<StoryContextSnapshot>(`writing-context:${episode}`,`/episodes/${episode}/context-snapshots`,'POST',accepted)
 const series=useSeriesEpisode()
 const [query,setQuery]=useState(()=>String(write.recovery?.query??'')),[records,setRecords]=useState<string[]>(()=>write.recovery?.record_ids as string[]??[])
 const pending=write.busy||write.uncertain
 const problems=snapshot?.context.problems.filter(p=>p.code!=='script_required')??[]
 const ready=!enabled||Boolean(snapshot&&fresh&&snapshot.project_version===projectVersion&&!problems.length&&!pending)
 useEffect(()=>{onChange({id:enabled?snapshot?.id??'':'',ready,changed:false})},[enabled,snapshot,ready,onChange])
 async function check(){if(!snapshot)return;setError('');try{const v=await request<StoryContextFreshness>(directoryURL(`/episodes/${episode}/context-snapshots/${snapshot.id}/freshness`,space));setFresh(v.current)}catch(e){setFresh(false);setError(errorMessage(e,t('当前来源核对失败')))}}
 return <section className="editing-note" aria-label={t('本集写作上下文')}><label className="story-check"><input type="checkbox" checked={enabled} disabled={disabled||pending} onChange={e=>setEnabled(e.target.checked)}/>{t('依据正式故事资料接续剧情')}</label><p>{t(enabled?'先准备并核对本集的正式设定、季集节拍和相关前集事件。必需资料有问题时不会生成，也不会静默截断。':'本次仅依据已保存简报探索独立候选，不声称已接续正式世界观与剧情。')}</p>
 {enabled&&<><details><summary>{t('调整可选资料范围')}</summary><label>{t('本次写作关注（选填）')}<input aria-label={t('本次写作关注（选填）')} value={query} disabled={disabled||pending} onChange={e=>{setQuery(e.target.value);setFresh(false);write.markDirty()}}/></label><p>{t('关键词筛选可选资料，正式圣经与必需节拍始终优先核对；省略的资料会列出原因，不会静默删掉规则。')}</p>{series&&<StoryContextSourcePicker root={series.series.id} space={space} known={snapshot?.context.sources??[]} selected={records} disabled={disabled||pending} onChange={ids=>{setRecords(ids);setFresh(false);write.markDirty()}}/>}</details><button type="button" disabled={disabled||write.busy} onClick={()=>void write.save({query,record_ids:records})}>{t(write.uncertain?'核对原资料准备请求':'准备本集写作资料')}</button>{snapshot&&<><button type="button" disabled={disabled||pending} onClick={()=>void check()}>{t('核对当前来源')}</button><ContextMaterial snapshot={snapshot}/>{(!fresh||snapshot.project_version!==projectVersion)&&<p role="status">{t('写作来源已变化，请重新准备本集资料。')}</p>}</>}</>}
 {write.error&&<p className="error" role="alert">{write.error}</p>}{error&&<p className="error" role="alert">{error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}
 </section>
}

export function WritingSourceReview({episode,space,contextID,projectVersion,onChange}:{episode:string;space:string;contextID:string;projectVersion:number;onChange:(v:WritingReview)=>void}){
 const {space:workspaceSpace}=useAppWorkspace()
 const {t}=useI18n(),[original,setOriginal]=useState<StoryContextSnapshot|null>(null),[current,setCurrent]=useState<StoryContextSnapshot|null>(null),[error,setError]=useState(''),[ready,setReady]=useState(false),[revision,setRevision]=useState(0)
 const version=useRef(projectVersion);useEffect(()=>{version.current=projectVersion},[projectVersion])
 const accepted=useCallback((v:StoryContextSnapshot)=>{setCurrent(v);setReady(v.project_version===version.current)},[])
 const write=useSeriesWrite<StoryContextSnapshot>(`writing-review:${contextID}`,`/episodes/${episode}/context-snapshots`,'POST',accepted)
 useEffect(()=>{const abort=new AbortController();void(async()=>{try{const v=await request<StoryContextSnapshot>(directoryURL(`/episodes/${episode}/context-snapshots/${contextID}`,space),{signal:abort.signal});const freshness=await request<StoryContextFreshness>(directoryURL(`/episodes/${episode}/context-snapshots/${contextID}/freshness`,space),{signal:abort.signal});if(!abort.signal.aborted){setOriginal(v);setCurrent(v);setReady(freshness.current);setError('')}}catch(e){if(!abort.signal.aborted){setError(errorMessage(e,t('上下文快照读取失败')));setReady(false)}}})();return()=>abort.abort()
 // Do not replace the candidate's local edits when reading current sources.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[episode,space,contextID,projectVersion,revision])
 const changed=Boolean(original&&current&&(original.directory_sha256!==current.directory_sha256||original.context.sources_sha256!==current.context.sources_sha256||original.project_version!==current.project_version))
 useEffect(()=>onChange(current?{id:current.id,ready:ready&&!write.busy&&!write.uncertain&&current.project_version===projectVersion,changed}:noReview),[current,ready,changed,projectVersion,write.busy,write.uncertain,onChange])
 return <section className="editing-note" aria-label={t('写作来源对照')}><h4>{t('写作来源对照')}</h4><p>{t('采纳前核对生成时与现在的正式资料。变化只作为创作判断依据，不会自动改候选或重新调用模型。')}</p>{error&&<p className="error" role="alert">{error}</p>}
 <button type="button" disabled={write.busy||write.uncertain} onClick={()=>setRevision(n=>n+1)}>{t('核对生成时的来源')}</button>
 {original&&<><details><summary>{t('生成时采用的正式资料')}</summary><ContextMaterial snapshot={original}/></details><button type="button" disabled={write.busy||!hasPermission(workspaceSpace,'workspace.write')} onClick={()=>void write.save(original.context.selection)}>{t(write.uncertain?'核对原资料准备请求':'准备现在的对照资料')}</button></>}
 {current&&current.id!==original?.id&&<details open><summary>{t('本次核对的正式资料')}</summary><ContextMaterial snapshot={current}/></details>}{!ready&&<p role="status">{t('来源已更新或尚未核对，请准备现在的对照资料后再决定。')}</p>}{write.error&&<p role="alert" className="error">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}
 </section>
}
