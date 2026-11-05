import {useCallback,useEffect,useId,useRef,useState} from 'react'
import {errorMessage,request} from '../../api'
import {useAppWorkspace} from '../../app/workspace'
import {useI18n} from '../../i18n'
import {StorySourceComparison} from './StorySourceComparison'
import {StoryEvidenceEditor} from './StoryEvidenceEditor'
import {StoryFields} from './StoryFields'
import {StoryBodyView} from './StoryBodyView'
import {directoryURL} from './types'
import {useDirectory} from './useDirectory'
import {useSeriesWrite} from './useSeriesWrite'
import {emptyBody,storyLabels,type StoryBody,type StoryDecision,type StoryDecisionInput,type StoryEpisode,type StoryFindingReview,type StoryKind,type StoryVersion} from './storyTypes'

export function StoryDecisionEditor({space,episode,initial,onChanged,onPendingChange}:{space:string;episode:string;initial:StoryFindingReview;onChanged:()=>void;onPendingChange:(value:boolean)=>void}){
 const{t}=useI18n();const{space:currentSpace}=useAppWorkspace();const[view,setView]=useState(initial),[decision,setDecision]=useState(initial.decision??null),[error,setError]=useState(''),[reading,setReading]=useState(false),[version,setVersion]=useState<StoryVersion|null>(null)
 const path=`/episodes/${episode}/continuity-checks/${initial.check_id}/findings/${initial.finding.id}`
 const saved=useCallback((d:StoryDecision)=>{setDecision(d);onChanged()},[onChanged])
 const write=useSeriesWrite<StoryDecision>(`story-decision:${initial.check_id}:${initial.finding.id}`,path+'/decisions','POST',saved)
 const reasonID=useId();const pending=write.dirty||write.busy||write.uncertain;useEffect(()=>{onPendingChange(pending);return()=>onPendingChange(false)},[pending,onPendingChange])
 const seed=write.recovery as unknown as StoryDecisionInput|null
 const[action,setAction]=useState<StoryDecisionInput['action']|''>(()=>seed?.action??''),[reason,setReason]=useState(()=>seed?.reason??''),[ack,setAck]=useState(()=>seed?.acknowledge_changed??false),[basis,setBasis]=useState(()=>seed?.review_sha256??initial.review_sha256)
 const proposal=seed?.proposal??initial.finding.proposal
 const[kind,setKind]=useState<StoryKind>(()=>proposal?.kind??'fact'),[title,setTitle]=useState(()=>proposal?.title??initial.finding.title),[body,setBody]=useState<StoryBody>(()=>structuredClone(proposal?.body??emptyBody('fact'))),[required,setRequired]=useState(()=>proposal?.required??false),[status,setStatus]=useState<'active'|'archived'>(()=>proposal?.status??'active'),[expected,setExpected]=useState(()=>proposal?.expected_version??0)
 const episodes=useDirectory<StoryEpisode>(`/series/${initial.snapshot.series_id}/story-episodes?limit=100`,space,0)
 const abort=useRef<AbortController|null>(null);useEffect(()=>()=>abort.current?.abort(),[])
 const writable=currentSpace?.role!=='viewer',locked=!writable||write.busy||write.uncertain||Boolean(decision)
 const characters=view.current_bible?.status==='active'?view.current_bible.body.bible?.characters??[]:[]
 const change=(fn:()=>void)=>{write.markDirty();fn()}
 async function refresh(){if(locked||reading)return;abort.current?.abort();const next=new AbortController();abort.current=next;setReading(true);setError('');try{const latest=await request<StoryFindingReview>(directoryURL(path+'/review',space),{signal:next.signal});if(!next.signal.aborted){setView(latest);setDecision(latest.decision??null);setExpected(latest.current_target?.number??0);setBasis(latest.review_sha256);setAck(false);write.markDirty()}}catch(e){if(!next.signal.aborted)setError(errorMessage(e,t('当前资料核对失败')))}finally{if(!next.signal.aborted)setReading(false)}}
 async function readAccepted(){if(!decision?.target_record_id||!decision.version_id)return;abort.current?.abort();const next=new AbortController();abort.current=next;setReading(true);setError('');try{const v=await request<StoryVersion>(directoryURL(`/series/${initial.snapshot.series_id}/story-records/${decision.target_record_id}/versions/${decision.version_id}`,space),{signal:next.signal});if(!next.signal.aborted)setVersion(v)}catch(e){if(!next.signal.aborted)setError(errorMessage(e,t('已采纳版本读取失败')))}finally{if(!next.signal.aborted)setReading(false)}}
 return <section className="series-form story-editor" aria-label={t('逐条处理连续性问题')}><h3>{initial.finding.title}</h3><p className="drama-help">{t('先比较证据和当前稿，再记录你的创作判断。保存处理结果不会再次调用模型。')}</p>
 {decision&&<section className="editing-note" role="status"><h4>{t(decision.action==='adopt'?'已显式采纳':decision.action==='intentional'?'已保留为创作意图':'已记录为不采纳')}</h4>{decision.reason&&<p>{decision.reason}</p>}<p>{t('处理记录保留原判断；正式资料可以继续追加修订。')}</p>{decision.target_record_id&&<button type="button" disabled={reading} onClick={()=>void readAccepted()}>{t('查看这次采纳的正式版本')}</button>}</section>}
 {version&&<section className="editing-note"><h4>{version.title} · {t('版本 {p0}',{p0:version.number})}</h4><StoryBodyView body={version.body}/></section>}
 {view.sources_changed&&<StorySourceComparison view={view} space={space}/>}
 <form onSubmit={e=>{e.preventDefault();if(!action&&!write.uncertain)return;void write.save({action,reason,review_sha256:basis,acknowledge_changed:ack,...(action==='adopt'?{proposal:{expected_version:expected,title,kind,status,required,body}}:{})})}}>
 <fieldset disabled={locked}><div className="drama-brief-actions">{([['intentional','保留为创作意图'],['reject','不采纳此问题'],['adopt','编辑后采纳']] as const).map(([key,label])=><button type="button" key={key} aria-pressed={action===key} onClick={()=>change(()=>setAction(key))}>{t(label)}</button>)}</div>
 {action==='adopt'&&<><p className="editing-note">{t(initial.finding.target_record_id?'这会追加该资料的正式新版本，旧版本保留。':'这会创建一条新的正式资料，不会自动修改其他资料。')}</p>
 {!initial.finding.target_record_id&&<label>{t('资料类型')}<select value={kind} onChange={e=>change(()=>{setKind(e.target.value as StoryKind);setBody(emptyBody(e.target.value as StoryKind))})}>{(['fact','event','foreshadow'] as StoryKind[]).map(k=><option value={k} key={k}>{t(storyLabels[k])}</option>)}</select></label>}
 {view.current_target&&<details open><summary>{t('比较当前正式版本 {p0}',{p0:view.current_target.number})}</summary><h4>{view.current_target.title}</h4><StoryBodyView body={view.current_target.body}/></details>}
 <label>{t('资料标题')}<input required value={title} onChange={e=>change(()=>setTitle(e.target.value))}/></label><StoryFields body={body} onChange={v=>change(()=>setBody(v))} characters={characters} episodes={episodes.items}/><StoryEvidenceEditor root={initial.snapshot.series_id} space={space} episodes={episodes.items} value={body.evidence} disabled={locked} onChange={evidence=>change(()=>setBody({...body,evidence}))}/>{episodes.next_cursor&&<button type="button" disabled={episodes.more} onClick={()=>void episodes.loadMore()}>{t('加载更多剧集选项')}</button>}{episodes.error&&<p className="error" role="alert">{episodes.error}</p>}
 {kind!=='bible'&&<label className="story-check"><input type="checkbox" checked={required} onChange={e=>change(()=>setRequired(e.target.checked))}/>{t('本集输入不可静默省略')}</label>}<label>{t('资料状态')}<select value={status} onChange={e=>change(()=>setStatus(e.target.value as typeof status))}><option value="active">{t('正式使用')}</option><option value="archived">{t('已归档')}</option></select></label>
 <p>{expected>0?t('本次保存基于资料版本 {p0}',{p0:expected}):t('这是一条新资料，将保存为首个正式版本。')}</p><button type="button" disabled={reading} onClick={()=>void refresh()}>{t('核对当前版本并保留草稿')}</button><p className="drama-help">{t('核对只更新比较依据与版本，保留你正在编辑的内容。')}</p>
 
 {view.sources_changed&&<label className="story-check"><input type="checkbox" required checked={ack} onChange={e=>change(()=>setAck(e.target.checked))}/>{t('我已比较来源变更，仍按以上内容保存这一版。')}</label>}
 </>}
 {action&&<div><label htmlFor={reasonID}>{t('处理说明')}</label><textarea id={reasonID} value={reason} required={action!=='adopt'} onChange={e=>change(()=>setReason(e.target.value))}/></div>}
 </fieldset>
 {(write.error||error)&&<p className="error" role="alert">{write.error||error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}{write.uncertain&&<p className="editing-note">{t('处理结果尚未确认。请用原请求核对，避免重复追加正式版本。')}</p>}
 {writable&&(!decision||write.uncertain)&&<button className="primary" disabled={write.busy||reading||(!write.uncertain&&(!action||(action==='adopt'&&(!title.trim()||(view.sources_changed&&!ack)))))}>{write.busy?t('正在保存…'):write.uncertain?t('核对原处理请求'):action==='adopt'?t('确认并保存正式版本'):t('保存创作判断')}</button>}
 </form>
 </section>
}
