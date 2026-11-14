import {hasPermission} from '../../permissions'
import {useCallback,useEffect,useRef,useState} from 'react'
import {errorMessage,request} from '../../api'
import {useAppWorkspace} from '../../app/workspace'
import {useI18n} from '../../i18n'
import {RuntimeModelPicker} from '../../components/RuntimeModelPicker'
import {runtimeFields,runtimeReady,type RuntimeChoice,type RuntimePreview} from '../../runtimeSelection'
import {directoryURL} from './types'
import {useDirectory} from './useDirectory'
import {StoryDecisionEditor} from './StoryDecisionEditor'
import {useSeriesWrite} from './useSeriesWrite'
import type {StoryFindingReview,StoryCheck,StoryCheckSummary,StoryContextSnapshot,StoryFinding} from './storyTypes'

const preview:RuntimePreview={operation:'story_continuity',input:{}}
const stateNames:Record<string,string>={queued:'等待检查',running:'正在检查',succeeded:'检查完成',failed:'检查未完成',unknown:'结果未知',cancelled:'已停止'}
const errorNames:Record<string,string>={provider_input_capacity:'输入资料与预留输出超过上游模型窗口，请减少本次资料或选择容量合适的模型；未自动截断或重发。',provider_input_count_unavailable:'上游原生输入计数未完成，没有发送生成请求；请核对模型接口与计数能力后显式重试。',story_context_changed:'资料或剧本已更新，请重新保存快照后检查。',invalid_story_check_output:'模型返回的引用或建议无法核验，未改动正式资料。',invalid_provider_response:'模型返回格式不符合要求，未改动正式资料。',execution_governance_denied:'内容审核或执行校验未通过，请联系管理员。',model_runtime_denied:'模型或执行权限已变化，请核对当前配置。',provider_rate_limited:'上游网关暂时拒绝了请求，请核对网关状态。'}
export function StoryChecksPanel({space,episode,snapshot,onChanged,onPendingChange}:{space:string;episode:string;snapshot:StoryContextSnapshot;onChanged:()=>void;onPendingChange:(value:boolean)=>void}){
 const{t}=useI18n();const{space:currentSpace,requestLeave}=useAppWorkspace();const[choice,setChoice]=useState<RuntimeChoice|null>(null),[revision,setRevision]=useState(0),[check,setCheck]=useState<StoryCheck|null>(null),[basis,setBasis]=useState(snapshot),[error,setError]=useState(''),[loading,setLoading]=useState(false)
 const[review,setReview]=useState<StoryFindingReview|null>(null),[reviewError,setReviewError]=useState(''),[reviewBusy,setReviewBusy]=useState(false)
 const reviewAbort=useRef<AbortController|null>(null);useEffect(()=>()=>reviewAbort.current?.abort(),[])
 const[reviewPending,setReviewPending]=useState(false)
 const path=`/episodes/${episode}/continuity-checks`
 const accepted=useCallback((job:StoryCheck)=>{setCheck(job);setReview(null);setBasis(snapshot);setRevision(n=>n+1)},[snapshot])
 const write=useSeriesWrite<StoryCheck>(`story-check:${snapshot.id}`,path,'POST',accepted)
 const pending=reviewPending||write.busy||write.uncertain;useEffect(()=>{onPendingChange(pending);return()=>onPendingChange(false)},[pending,onPendingChange])
 const jobs=useDirectory<StoryCheckSummary>(path,space,revision)
 const abort=useRef<AbortController|null>(null);useEffect(()=>()=>abort.current?.abort(),[])
 async function read(id:string){abort.current?.abort();const controller=new AbortController();abort.current=controller;setLoading(true);setError('');try{const job=await request<StoryCheck>(directoryURL(path+'/'+id,space),{signal:controller.signal});const source=job.input.context_snapshot_id===snapshot.id?snapshot:await request<StoryContextSnapshot>(directoryURL(`/episodes/${episode}/context-snapshots/${job.input.context_snapshot_id}`,space),{signal:controller.signal});if(!controller.signal.aborted){setCheck(job);setBasis(source)}}catch(e){if(!controller.signal.aborted)setError(errorMessage(e,t('连续性检查读取失败')))}finally{if(!controller.signal.aborted)setLoading(false)}}
 async function openReview(f:StoryFinding){if(!check)return;reviewAbort.current?.abort();const next=new AbortController();reviewAbort.current=next;setReviewBusy(true);setReviewError('');try{const v=await request<StoryFindingReview>(directoryURL(path+'/'+check.id+'/findings/'+f.id+'/review',space),{signal:next.signal});if(!next.signal.aborted)setReview(v)}catch(e){if(!next.signal.aborted)setReviewError(errorMessage(e,t('问题审阅读取失败')))}finally{if(!next.signal.aborted)setReviewBusy(false)}}
 useEffect(()=>{if(!check||!['queued','running'].includes(check.state))return;const controller=new AbortController();const timer=setTimeout(()=>{void request<StoryCheck>(directoryURL(path+'/'+check.id,space),{signal:controller.signal}).then(job=>{if(!controller.signal.aborted){setCheck(job);setError('');if(!['queued','running'].includes(job.state))setRevision(n=>n+1)}}).catch(e=>{if(!controller.signal.aborted)setError(errorMessage(e,t('检查状态暂时无法确认，请重新读取。')))})},1000);return()=>{controller.abort();clearTimeout(timer)}},[check,path,space,t])
 const running=write.busy||Boolean(check&&['queued','running'].includes(check.state))
 const ready=Boolean(choice?.model.gateway_id)&&runtimeReady(choice,preview)&&snapshot.context.problems.length===0
 return <section className="story-snapshots" aria-label={t('AI故事连续性检查')}><h3>{t('请AI辅助审阅连续性')}</h3><p className="drama-help">{t('使用真实网关文本模型核对这份资料与剧本。AI可能误判悬念或人物误认，请逐条判断；检查不会自动修改正式资料。')}</p>
 {!write.uncertain&&<RuntimeModelPicker space={space} capability="text" gatewayOnly value={choice} onChange={setChoice} preview={preview} disabled={running||!hasPermission(currentSpace,'workspace.write')}/>}
 {!!snapshot.context.problems.length&&<p className="editing-note">{t('请先处理这份快照中的资料或剧本问题，再保存新快照开始检查。')}</p>}
 {write.uncertain&&<p className="editing-note">{t('上次检查请求尚未确认。使用原选择核对，不会自动创建第二次调用。')}</p>}
 {(write.error||error||jobs.error)&&<p className="error" role="alert">{write.error||error||jobs.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}
 {hasPermission(currentSpace,'workspace.write')&&<button type="button" className="primary" disabled={running||(!write.uncertain&&!ready)} onClick={()=>{const submit=()=>void write.save({context_snapshot_id:snapshot.id,...runtimeFields(choice)});if(reviewPending&&!write.uncertain)requestLeave(submit);else submit()}}>{write.busy?t('正在提交…'):write.uncertain?t('核对原检查请求'):t('发起一次AI连续性检查')}</button>}
 <div className="series-heading"><h3>{t('本集检查记录')}</h3><button type="button" onClick={()=>setRevision(n=>n+1)}>{t('刷新检查记录')}</button></div>
 {jobs.loading&&<p role="status">{t('正在读取检查记录…')}</p>}{!jobs.loading&&!jobs.error&&!jobs.items.length&&<p className="page-empty">{t('还没有检查记录。保存资料或查看历史不会触发模型调用。')}</p>}
 {jobs.items.map(job=><article className="story-card" key={job.id}><div><h4>{t(stateNames[job.state]??'检查未完成')}</h4><p>{job.model}</p><time dateTime={job.created_at}>{new Date(job.created_at).toLocaleString()}</time></div><button type="button" disabled={loading} onClick={()=>{const open=()=>void read(job.id);if(reviewPending)requestLeave(open);else open()}}>{t('查看检查结果')}</button></article>)}{jobs.next_cursor&&<button type="button" disabled={jobs.more} onClick={()=>void jobs.loadMore()}>{t('加载更多检查')}</button>}
 {check&&<section aria-label={t('连续性检查结果')}><h3 role="status">{t(stateNames[check.state]??'检查未完成')}</h3>{error&&<button type="button" onClick={()=>void read(check.id)}>{t('重新读取检查状态')}</button>}{check.state==='unknown'&&<p className="editing-note">{t('上游可能已执行，未自动重发。请先核对网关记录，再决定是否发起新的检查。')}</p>}{check.error_code&&check.state!=='unknown'&&<p className="error">{t(errorNames[check.error_code]??'检查未完成，原稿和正式资料保持不变。')}</p>}
 {check.result&&<><p>{check.result.summary}</p>{!check.result.findings.length&&<p>{t('本次未发现有原文支撑的问题。仍请按创作意图审稿。')}</p>}{check.result.findings.map(f=><FindingCard key={f.id} finding={f} snapshot={basis} disabled={reviewBusy} onReview={()=>{const open=()=>void openReview(f);if(reviewPending)requestLeave(open);else open()}}/>)}</>}
 {reviewError&&<p className="error" role="alert">{reviewError}</p>}{reviewBusy&&<p role="status">{t('正在核对问题与当前稿…')}</p>}{review&&review.check_id===check.id&&<StoryDecisionEditor key={review.finding.id+review.review_sha256} space={space} episode={episode} initial={review} onChanged={onChanged} onPendingChange={setReviewPending}/>}

 </section>}
 </section>
}
function FindingCard({finding:f,snapshot,onReview,disabled}:{finding:StoryFinding;snapshot:StoryContextSnapshot;onReview:()=>void;disabled:boolean}){
 const{t}=useI18n();const severity:Record<string,string>={note:'创作提示',warning:'建议复核',critical:'重点复核'},confidence:Record<string,string>={low:'判断把握较低',medium:'仍需作者判断',high:'建议优先核对证据'}
 return <article className="editing-note story-finding"><h4>{f.title}</h4><p>{t(severity[f.severity]??'建议复核')} · {t(confidence[f.confidence]??'仍需作者判断')}</p><p>{f.explanation}</p><details><summary>{t('核对本次引用的原文')}</summary>{f.evidence.map((e,i)=>{const record=snapshot.context.included.find(v=>v.id===e.record_id&&v.version_id===e.version_id),scene=snapshot.script?.scenes.find(v=>v.id===e.scene_id);return <section key={i}><p>{record?`${record.title} · ${t('版本 {p0}',{p0:record.number})}`:t('当时剧本版本 {p0}',{p0:snapshot.script_version})}</p><blockquote>{e.quote}</blockquote>{scene&&<details><summary>{t('查看当时完整场次')}</summary><h4>{scene.heading}</h4><p>{scene.action}</p>{scene.dialogue.map((line,i)=><p key={i}>{line.character}：{line.text}</p>)}</details>}</section>})}</details><button type="button" disabled={disabled} onClick={onReview}>{t('审阅并记录判断')}</button></article>
}
