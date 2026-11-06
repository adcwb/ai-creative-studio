import {WritingContextPicker,WritingSourceReview,type WritingReview} from './WritingContext'
import {useCallback,useEffect,useRef,useState} from 'react'
import {errorMessage,request} from '../../api'
import {useI18n} from '../../i18n'
import {DramaScriptEditor} from '../../components/DramaScriptEditor'
import {generationLabels,scriptDraftError,type DramaScript,type ScriptGeneration} from '../../drama'
import {useSeriesWrite} from '../series/useSeriesWrite'
import {directoryURL} from '../series/types'
import {useNavigationTask} from '../../app/navigationGuard'
import type {ProjectEditorState} from './useProjectEditor'
import {creationPath} from '../../creationGateway'
import {runtimeFields} from '../../runtimeSelection'

export function ScriptCandidates({state}:{state:ProjectEditorState}) {
 const {t}=useI18n(),{space,id,editor,setToolsDirty}=state
 const [items,setItems]=useState<ScriptGeneration[]>([]),[error,setError]=useState(''),[loading,setLoading]=useState(true),[revision,setRevision]=useState(0)
 const [pending,setPending]=useState<Set<string>>(()=>new Set())
 const changed=useCallback((id:string,dirty:boolean)=>setPending(old=>{const next=new Set(old);if(dirty)next.add(id);else next.delete(id);return next}),[])
 useEffect(()=>{setToolsDirty(pending.size>0);return()=>setToolsDirty(false)},[pending,setToolsDirty])
 useEffect(()=>{
  const abort=new AbortController()
  void request<ScriptGeneration[]>(directoryURL(`/projects/${id}/generations`,space),{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setItems(v);setError('');setLoading(false)}}).catch(e=>{if(!abort.signal.aborted){setError(errorMessage(e,t('生成记录读取失败')));setLoading(false)}})
  return()=>abort.abort()
  // Language changes preserve the writer's local edits.
  // eslint-disable-next-line react-hooks/exhaustive-deps
 },[space,id,editor.historyRevision,revision])
 return <section className="drama-history script-candidates" aria-label={t('剧本候选与采纳')}><div className="series-heading"><h3>{t('剧本候选与采纳')}</h3><button type="button" onClick={()=>setRevision(v=>v+1)}>{t('刷新候选记录')}</button></div>
 <p>{t('候选生成成功也不会替换正式稿。先比较，再决定是否采纳；未采纳的候选会保留。')}</p>
 <CandidateGenerator state={state} onPending={changed}/>
 {error&&<p className="error" role="alert">{error}</p>}{loading&&<p role="status">{t('正在读取生成记录…')}</p>}{!loading&&!items.length&&<p className="page-empty">{t('还没有生成记录。')}</p>}
 {items.map(item=><Candidate key={item.id} summary={item} state={state} onPending={changed}/>)}
 </section>
}

function CandidateGenerator({state,onPending}:{state:ProjectEditorState;onPending:(id:string,dirty:boolean)=>void}) {
 const {t}=useI18n(),{id,editor,runtimeChoice,effectiveCreative}=state
 const reload=useRef(editor.reload)
 useEffect(()=>{reload.current=editor.reload},[editor.reload])
 const saved=useCallback(()=>reload.current(false),[])
 const write=useSeriesWrite<ScriptGeneration>(`script-generation:${id}`,creationPath(`/projects/${id}/generations`),'POST',saved)
 const [context,setContext]=useState<WritingReview>({id:'',ready:true,changed:false})
 const pending=write.busy||write.uncertain
 useEffect(()=>{onPending('generation',pending);return()=>onPending('generation',false)},[pending,onPending])
 return <div>{editor.project?.series_episode_id&&<WritingContextPicker episode={editor.project.id} space={state.space} projectVersion={editor.project.version} disabled={editor.locked||write.busy||write.uncertain} onChange={setContext}/>}<button type="button" className="primary" disabled={!context.ready||editor.locked||Boolean(editor.loadError)||editor.dirty||state.toolsDirty||!effectiveCreative?.ready||write.busy} onClick={()=>void write.save({candidate_only:true,...(context.id?{context_snapshot_id:context.id}:{}),expected_version:editor.project?.version,...(runtimeChoice?{...runtimeFields(runtimeChoice),expected_provider:runtimeChoice.model.provider,expected_model:runtimeChoice.model.model}:{})})}>{write.busy?t('正在提交…'):t('生成剧本候选')}</button>
 {write.error&&<p className="error" role="alert">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}
 {write.uncertain&&<div className="editing-note"><p>{t('生成结果尚未确认。请核对原请求，系统不会另开一次模型调用。')}</p><button type="button" disabled={write.busy} onClick={()=>void write.save({})}>{t('核对原生成请求')}</button></div>}
 </div>
}

function Candidate({summary,state,onPending}:{summary:ScriptGeneration;state:ProjectEditorState;onPending:(id:string,dirty:boolean)=>void}) {
 const {t,formatDate}=useI18n(),{space,id,editor,requestLeave}=state
 const [shown,setShown]=useState<ScriptGeneration|null>(null),[draft,setDraft]=useState<DramaScript|null>(null),[editing,setEditing]=useState(false),[reason,setReason]=useState(''),[reviewed,setReviewed]=useState(false),[reviewedVersion,setReviewedVersion]=useState(0),[reviewedSource,setReviewedSource]=useState(''),[error,setError]=useState(''),[loading,setLoading]=useState(false)
 const path=`/projects/${id}/generations/${summary.id}`
 const reload=useRef(editor.reload)
 useEffect(()=>{reload.current=editor.reload},[editor.reload])
 const saved=useCallback(()=>{setReviewed(false);setShown(null);setDraft(null);setReason('');setEditing(false);reload.current(true)},[])
 const write=useSeriesWrite(`script-adoption:${summary.id}`,path+'/adoptions','POST',saved)
 const abort=useRef<AbortController|null>(null)
 useEffect(()=>()=>abort.current?.abort(),[])
 const dirty=editing||Boolean(reason)||Boolean(shown&&draft&&JSON.stringify(draft)!==JSON.stringify(shown.candidate))||write.dirty||write.busy||write.uncertain
 useNavigationTask(dirty)
 useEffect(()=>{onPending(summary.id,dirty);return()=>onPending(summary.id,false)},[summary.id,dirty,onPending])
 async function read(){
  abort.current?.abort();const controller=new AbortController();abort.current=controller;setLoading(true);setError('')
  try{const v=await request<ScriptGeneration>(directoryURL(path,space),{signal:controller.signal});if(!controller.signal.aborted){setShown(v);setDraft(v.candidate?structuredClone(v.candidate):null);setReason('');setEditing(false);setReviewed(false);setReviewedVersion(0)}}catch(e){if(!controller.signal.aborted)setError(errorMessage(e,t('候选读取失败，请保留当前稿并重试。')))}finally{if(!controller.signal.aborted)setLoading(false)}
 }
 const adopted=shown?.adoption??summary.adoption
 const [sourceReview,setSourceReview]=useState<WritingReview>({id:'',ready:false,changed:false})
 const changed=editor.project?.version!==summary.base_version||sourceReview.changed
 const acknowledged=reviewed&&reviewedVersion===editor.project?.version&&(!shown?.context_snapshot_id||reviewedSource===sourceReview.id)
 const locked=editor.locked||Boolean(editor.loadError)||editor.dirty||write.busy||write.uncertain
 const valid=draft&&!scriptDraftError(draft)&&(!shown?.context_snapshot_id||sourceReview.ready)
 return <article className="story-card script-candidate"><div><h4>{summary.source_title} · {t(generationLabels[summary.status])}</h4><p>{t('基于版本 {version}',{version:summary.base_version})} · {summary.provider} · <time>{formatDate(summary.created_at)}</time></p>
 {!summary.candidate_only&&<p>{t('旧版生成记录：当时采用自动更新流程。原稿可在版本历史中查看。')}</p>}
 {summary.error&&<p className="error" role="alert">{summary.error} <code>{summary.error_code}</code></p>}
 {adopted&&<p role="status">{t('已明确采纳为剧本版本 {p0}',{p0:adopted.script_version})}{adopted.edited?` · ${t('编辑后采纳')}`:''}{adopted.reason?` · ${adopted.reason}`:''}</p>}
 {summary.candidate_only&&summary.status==='succeeded'&&<button type="button" disabled={loading||write.busy} onClick={()=>requestLeave(()=>void read())}>{loading?t('正在读取候选…'):t('比较这份候选')}</button>}
 {error&&<p className="error" role="alert">{error}</p>}
 {shown&&draft&&<section aria-label={t('候选比较与决定')}>
 {shown.context_snapshot_id&&<WritingSourceReview episode={id!} space={space} contextID={shown.context_snapshot_id} projectVersion={editor.project?.version??0} onChange={setSourceReview}/>}
 <details><summary>{t('查看生成时的原稿与简报')}</summary><p>{shown.source_brief}</p>{shown.source_script?<ScriptText script={shown.source_script}/>:<p>{t('生成时尚无正式剧本。')}</p>}</details>
 <button type="button" disabled={write.busy||editor.loading} onClick={()=>editor.reload(false)}>{t('核对最新正式稿')}</button>
 <details><summary>{t('查看现在的正式剧本')}</summary><p>{editor.project?.brief}</p>{editor.project?.script?<ScriptText script={editor.project.script}/>:<p>{t('当前尚无正式剧本。')}</p>}</details>
 <DramaScriptEditor draftOnly provider={shown.provider} script={draft} editing={editing} disabled={locked||Boolean(adopted)} dirty={JSON.stringify(draft)!==JSON.stringify(shown.candidate)} onChange={v=>{setDraft(v)}} onEdit={()=>setEditing(true)} onCancel={()=>{setDraft(structuredClone(shown.candidate!));setEditing(false)}} onSave={()=>setEditing(false)}/>
 {!adopted&&<><label>{t('采纳理由（可选）')}<textarea aria-label={t('采纳理由（可选）')} disabled={locked} value={reason} maxLength={2000} onChange={e=>{setReason(e.target.value)}}/></label>
 {changed&&<div className="editing-note"><p>{t('正式稿或简报在生成后已变化。请比较现在的内容，再决定是否用这份候选形成新版本。')}</p><label className="checkbox-label"><input type="checkbox" checked={acknowledged} disabled={locked} onChange={e=>{setReviewed(e.target.checked);setReviewedVersion(editor.project?.version??0);setReviewedSource(sourceReview.id)}}/>{t('我已比较当前版本，仍决定采纳这份候选。')}</label></div>}
 <p className="drama-help">{t('采纳会追加正式剧本版本；已有分镜需要复核，旧稿与候选保持不变。')}</p>
 <button type="button" className="primary" disabled={locked||editing||!valid||(changed&&!acknowledged)} onClick={()=>void write.save({...(shown.context_snapshot_id?{review_context_snapshot_id:sourceReview.id}:{}),expected_version:editor.project?.version,reviewed_changes:changed&&acknowledged,script:draft,reason})}>{t('明确采纳为正式剧本')}</button></>}
 </section>}
 {write.error&&<p className="error" role="alert">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}
 {write.uncertain&&<div className="editing-note"><p>{t('采纳结果尚未确认。请用原请求核对，避免重复更新正式稿。')}</p><button type="button" disabled={write.busy} onClick={()=>void write.save({})}>{t('核对原采纳请求')}</button></div>}
 </div></article>
}

function ScriptText({script}:{script:DramaScript}){return <div><h4>{script.title}</h4><p>{script.logline}</p>{script.scenes.map((v,i)=><section key={v.id??i}><h5>{v.heading}</h5><p>{v.action}</p>{v.dialogue.map((d,j)=><p key={j}>{d.character}：{d.text}</p>)}</section>)}</div>}
