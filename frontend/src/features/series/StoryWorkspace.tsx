import {hasPermission} from '../../permissions'
import {StoryContextSourcePicker} from './StoryContextSourcePicker'
import {StoryEvidenceEditor} from './StoryEvidenceEditor'
import {StoryCastPanel} from './StoryCastPanel'
import {useCallback,useEffect,useRef,useState} from 'react'
import {request,errorMessage} from '../../api'
import {useAppWorkspace} from '../../app/workspace'
import {useNavigationTask} from '../../app/navigationGuard'
import {useI18n} from '../../i18n'
import {useDirectory} from './useDirectory'
import {readSeriesRecovery,useSeriesWrite} from './useSeriesWrite'
import {directoryURL} from './types'
import {StoryBodyView as ReadBody} from './StoryBodyView'
import {StoryChecksPanel} from './StoryChecksPanel'
import {EpisodeField,StoryFields} from './StoryFields'
import {emptyBody,storyLabels,type StoryContextFreshness,type StoryContextSelection,type StoryContextSnapshot,type StoryContextSummary,type StoryBody,type StoryCharacter,type StoryEpisode,type StoryHistory,type StoryInput,type StoryKind,type StoryPage,type StoryPreview,type StorySummary,type StoryVersion} from './storyTypes'

export function StoryWorkspace({root}:{root:string}){const{t}=useI18n();const[open,setOpen]=useState(false);return <details className="panel project-tools story-workspace" onToggle={e=>{if(e.currentTarget.open)setOpen(true)}}><summary>{t('故事资料与连续性')}</summary>{open&&<StoryTabs root={root}/>}</details>}
function StoryTabs({root}:{root:string}){
 const{t}=useI18n();const{space,requestLeave}=useAppWorkspace();const[tab,setTab]=useState<StoryKind|'context'>('bible'),[revision,setRevision]=useState(0),[characters,setCharacters]=useState<StoryCharacter[]>([]),[bibleError,setBibleError]=useState('')
 const refresh=useCallback(()=>setRevision(v=>v+1),[])
 const episodes=useDirectory<StoryEpisode>(`/series/${root}/story-episodes?limit=100`,space!.id,revision)
 useEffect(()=>{const abort=new AbortController();void(async()=>{try{const list=await request<StoryPage>(directoryURL(`/series/${root}/story-records?kind=bible`,space!.id),{signal:abort.signal});const head=list.items[0];const bible=head?await request<StoryVersion>(directoryURL(`/series/${root}/story-records/${head.id}`,space!.id),{signal:abort.signal}):null;if(!abort.signal.aborted){setCharacters(bible?.status==='active'?bible.body.bible?.characters??[]:[]);setBibleError('')}}catch(e){if(!abort.signal.aborted)setBibleError(errorMessage(e,t('故事资料读取失败')))}})();return()=>abort.abort()
 // Keep the writer's selections when the language changes.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[root,space?.id,revision])
 return <><p>{t('先保存正式设定，再为每集核对需要的资料。保存与预览不会调用模型。')}</p><button type="button" onClick={()=>setRevision(v=>v+1)}>{t('刷新正式资料')}</button><p className="drama-help">{t('刷新来源不会覆盖当前草稿。')}</p><nav className="project-steps" aria-label={t('故事资料分类')}>{([...Object.entries(storyLabels).filter(([key])=>key!=='outline'),['context','本集上下文']] as [StoryKind|'context',string][]).map(([key,label])=><button type="button" key={key} aria-pressed={tab===key} onClick={()=>requestLeave(()=>setTab(key))}>{t(label)}</button>)}</nav>
 {bibleError&&<p className="error" role="alert">{bibleError}</p>}{episodes.error&&<p className="error" role="alert">{episodes.error}</p>}
 {episodes.next_cursor&&<button type="button" disabled={episodes.more} onClick={()=>void episodes.loadMore()}>{t('加载更多剧集选项')}</button>}
 {tab==='cast'?<StoryCastPanel root={root} space={space!.id} episodes={episodes.items} onChanged={refresh}/>:tab==='context'?<ContextPanel key="context" root={root} space={space!.id} episodes={episodes.items} refresh={refresh}/>:<StoryTab key={tab} root={root} space={space!.id} kind={tab} revision={revision} episodes={episodes.items} characters={characters} refresh={refresh}/>}
 </>
}
function StoryTab({root,space,kind,revision,episodes,characters,refresh}:{root:string;space:string;kind:StoryKind;revision:number;episodes:StoryEpisode[];characters:StoryCharacter[];refresh:()=>void}){
 const{t}=useI18n();const{space:currentSpace,requestLeave,session}=useAppWorkspace();const[mode,setMode]=useState(()=>readSeriesRecovery(session.user?.id??'demo-user',space,`story:${root}:${kind}`,`/series/${root}/story-records`,'POST')?'new':''),[item,setItem]=useState<StoryVersion|null>(null),[error,setError]=useState(''),[loading,setLoading]=useState(false)
 const list=useDirectory<StorySummary>(`/series/${root}/story-records?kind=${kind}`,space,revision)
 const controller=useRef<AbortController|null>(null);useEffect(()=>()=>controller.current?.abort(),[])
 async function edit(summary:StorySummary){controller.current?.abort();const abort=new AbortController();controller.current=abort;setLoading(true);setError('');try{const v=await request<StoryVersion>(directoryURL(`/series/${root}/story-records/${summary.id}`,space),{signal:abort.signal});if(!abort.signal.aborted){setItem(v);setMode(summary.id)}}catch(e){if(!abort.signal.aborted)setError(errorMessage(e,t('故事资料读取失败')))}finally{if(!abort.signal.aborted)setLoading(false)}}
 const saved=useCallback(()=>{setMode('');setItem(null);refresh()},[refresh])
 const writable=hasPermission(currentSpace,'workspace.write')
 return <div className="story-tab"><div className="series-heading"><h2>{t(storyLabels[kind])}</h2>{writable&&(!list.items.length||kind!=='bible')&&!mode&&<button type="button" disabled={list.loading||loading} onClick={()=>{setItem(null);setMode('new')}}>{t(kind==='bible'?'建立故事圣经':'添加正式资料')}</button>}</div>
 <p className="drama-help">{t(kind==='bible'?'正式规则与人物弧线保留版本；在本集上下文中核对资料后，可发起AI检查并逐条决定是否采纳。':kind==='fact'?'客观设定、角色所信和待证信息分开保存，避免把误认当成客观真相。':kind==='event'?'叙事出现的集数与故事中的时间分别记录，闪回不会被自动排到第一集。':'记录埋设与回收的创作意图，调整剧集顺序后需要重新核对。')}</p>
 {(error||list.error)&&<div className="error" role="alert">{error||list.error}<button type="button" onClick={refresh}>{t('重新读取资料')}</button></div>}{(loading||list.loading)&&<p role="status">{t('正在读取故事资料…')}</p>}
 {mode?<StoryEditor key={mode} root={root} kind={kind} item={item??undefined} space={space} characters={characters} episodes={episodes} onSaved={saved} onClose={()=>setMode('')}/>:<>{!list.loading&&!list.error&&!list.items.length&&<p className="page-empty">{t('还没有这类正式资料。可以先写最影响故事的一条。')}</p>}{list.items.map(n=><article className="story-card" key={n.id}><div><h3>{n.title}</h3><p>{t('版本 {p0}',{p0:n.number})} · {n.status==='archived'?t('已归档'):t('正式资料')}{n.required?` · ${t('本集输入不可静默省略')}`:''}</p></div><button type="button" disabled={loading} onClick={()=>requestLeave(()=>void edit(n))}>{writable?t('查看与编辑'):t('查看资料')}</button></article>)}{list.next_cursor&&<button type="button" disabled={list.more} onClick={()=>void list.loadMore()}>{t('加载更多资料')}</button>}</>}
 </div>
}
export function StoryEditor({root,kind,item,space,characters,episodes,onSaved,onClose,outlineScope,initialTitle,identity}:{root:string;kind:StoryKind;item?:StoryVersion;space:string;characters:StoryCharacter[];episodes:StoryEpisode[];onSaved:()=>void;onClose:()=>void;outlineScope?:{season_id:string;episode_id?:string};initialTitle?:string;identity?:string}){
 const{t}=useI18n();const{space:currentSpace,requestLeave}=useAppWorkspace();const path=`/series/${root}/story-records${item?'/'+item.id:''}`
 const write=useSeriesWrite<StoryVersion>(identity??`story:${root}:${item?.id??kind}`,path,item?'PUT':'POST',onSaved)
 const seed=write.recovery as Partial<StoryInput>|null
 const[title,setTitle]=useState(()=>seed?.title??item?.title??initialTitle??(kind==='bible'?t('故事圣经'):'')),[body,setBody]=useState<StoryBody>(()=>structuredClone(seed?.body??item?.body??(kind==='outline'?{evidence:[],outline:{...emptyBody('outline').outline!,...outlineScope}}:emptyBody(kind)))),[required,setRequired]=useState(()=>seed?.required??item?.required??(kind==='bible'||kind==='outline')),[status,setStatus]=useState<'active'|'archived'>(()=>seed?.status??item?.status??'active')
 const[expectedVersion,setExpectedVersion]=useState(()=>seed?.expected_version??item?.number??0),[comparison,setComparison]=useState<StoryVersion|null>(null),[comparisonError,setComparisonError]=useState('')
 const[history,setHistory]=useState<StoryHistory>({items:[],next_cursor:''}),[historyError,setHistoryError]=useState(''),[historyBusy,setHistoryBusy]=useState(false),[shown,setShown]=useState<StoryVersion|null>(null)
 const alive=useRef(true),historyController=useRef<AbortController|null>(null)
 useEffect(()=>{alive.current=true;return()=>{alive.current=false;historyController.current?.abort()}},[])
 const writable=hasPermission(currentSpace,'workspace.write');const locked=!writable||write.busy||write.uncertain
 const change=(f:()=>void)=>{write.markDirty();f()}
 async function loadHistory(more=false){if(!item||historyBusy)return;setHistoryBusy(true);setHistoryError('');const abort=new AbortController();historyController.current=abort;try{const page=await request<StoryHistory>(directoryURL(path+'/versions',space,more?history.next_cursor:''),{signal:abort.signal});if(alive.current&&!abort.signal.aborted)setHistory(old=>more?{items:[...old.items,...page.items.filter(x=>!old.items.some(v=>v.version_id===x.version_id))],next_cursor:page.next_cursor}:page)}catch(e){if(alive.current&&!abort.signal.aborted)setHistoryError(errorMessage(e,t('资料历史读取失败')))}finally{if(alive.current&&!abort.signal.aborted)setHistoryBusy(false)}}
 async function refreshExpected(){if(!item||locked)return;const abort=new AbortController();historyController.current?.abort();historyController.current=abort;setComparisonError('');try{const v=await request<StoryVersion>(directoryURL(path,space),{signal:abort.signal});if(alive.current&&!abort.signal.aborted){setComparison(v);setExpectedVersion(v.number)}}catch(e){if(alive.current&&!abort.signal.aborted)setComparisonError(errorMessage(e,t('故事资料读取失败')))}}
 const invalid=!title.trim()||Boolean(body.bible?.characters.some(c=>!c.name.trim()))
 return <section className="series-form story-editor" aria-label={t('正式资料编辑器')}><form onSubmit={e=>{e.preventDefault();void write.save({expected_version:expectedVersion,title,kind,status,required,body})}}><fieldset disabled={locked}>
 <label>{t('资料标题')}<input value={title} required onChange={e=>change(()=>setTitle(e.target.value))}/></label><StoryFields body={body} onChange={v=>change(()=>setBody(v))} characters={characters} episodes={episodes}/><StoryEvidenceEditor root={root} space={space} episodes={episodes} value={body.evidence} disabled={locked} onChange={evidence=>change(()=>setBody({...body,evidence}))}/>
 {kind!=='bible'&&kind!=='outline'&&<label className="story-check"><input type="checkbox" checked={required} onChange={e=>change(()=>setRequired(e.target.checked))}/>{t('本集输入不可静默省略')}</label>}<label>{t('资料状态')}<select value={status} onChange={e=>change(()=>setStatus(e.target.value as typeof status))}><option value="active">{t('正式使用')}</option><option value="archived">{t('已归档')}</option></select></label>
 </fieldset>
 {write.error&&<p className="error" role="alert">{write.error}</p>}{item&&writable&&!write.uncertain&&<button type="button" disabled={locked} onClick={()=>void refreshExpected()}>{t('核对最新版本并保留草稿')}</button>}{comparisonError&&<p role="alert" className="error">{comparisonError}</p>}{comparison&&<section className="editing-note"><h4>{t('服务器当前版本 {p0}',{p0:comparison.number})}</h4><ReadBody body={comparison.body}/><p>{t('草稿未改动。请比较后再保存；保存仍会校验最新版本。')}</p></section>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}{write.uncertain&&<p className="editing-note">{t('上次保存结果尚未确认。请用原请求核对，避免重复创建。')}</p>}
 <div className="drama-brief-actions"><button type="button" disabled={write.busy} onClick={()=>requestLeave(onClose)}>{t('返回资料列表')}</button>{writable&&<button className="primary" disabled={write.busy||(!write.uncertain&&invalid)}>{write.busy?t('正在保存…'):write.uncertain?t('核对并重试保存'):t('保存正式版本')}</button>}</div></form>
 {item&&<section className="story-history"><div className="series-heading"><h3>{t('资料版本历史')}</h3><button type="button" disabled={historyBusy} onClick={()=>void loadHistory()}>{t('读取资料历史')}</button></div>{historyError&&<p className="error" role="alert">{historyError}</p>}{history.items.map(v=><button type="button" key={v.version_id} onClick={()=>setShown(v)}>{t('版本 {p0}',{p0:v.number})} · {v.title}</button>)}{history.next_cursor&&<button type="button" disabled={historyBusy} onClick={()=>void loadHistory(true)}>{t('加载更多历史')}</button>}
 {shown&&<article className="editing-note"><h4>{t('版本 {p0}',{p0:shown.number})} · {shown.title}</h4><ReadBody body={shown.body}/>{writable&&<button type="button" disabled={locked} onClick={()=>requestLeave(()=>{setTitle(shown.title);setBody(structuredClone(shown.body));setRequired(shown.required);setStatus(shown.status);write.markDirty()})}>{t('以此版本作为新草稿')}</button>}<p>{t('历史版本保持不变。保存草稿会追加正式新版本。')}</p></article>}</section>}
 </section>
}

function ContextPanel({root,space,episodes,refresh}:{root:string;space:string;episodes:StoryEpisode[];refresh:()=>void}){
 const{t}=useI18n();const{space:currentSpace,requestLeave}=useAppWorkspace();const[episode,setEpisode]=useState(''),[preview,setPreview]=useState<StoryPreview|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false)
 const controller=useRef<AbortController|null>(null),flight=useRef(false);useEffect(()=>()=>controller.current?.abort(),[]);useNavigationTask(busy)
 async function build(){if(flight.current||!episode)return;flight.current=true;setBusy(true);setError('');const abort=new AbortController();controller.current=abort;try{const p=await request<StoryPreview>(directoryURL(`/episodes/${episode}/context-previews`,space),{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:'{}',signal:abort.signal});if(!abort.signal.aborted)setPreview(p)}catch(e){if(!abort.signal.aborted)setError(errorMessage(e,t('上下文预览失败')))}finally{flight.current=false;if(!abort.signal.aborted)setBusy(false)}}
 const names=new Map(preview?.included.flatMap(v=>v.body.bible?.characters.map(c=>[c.id,c.name] as const)??[])??[])
 const sources=new Map(preview?.sources.map(s=>[s.id,s])??[])
 const reasons:Record<string,string>={archived:'资料已归档',outside_episode_range:'不在本集适用范围',future_event:'叙事尚未出现的事件',other_episode_cast:'其他剧集的选角',input_capacity:'超过本次输入容量',not_retrieved:'未命中本次检索条件'}
 return <section className="series-form"><h2>{t('本集上下文')}</h2><p className="drama-help">{t('核对本集会使用哪些正式资料。这不是AI连续性检查，也不会生成剧本。')}</p>{!episodes.length&&<p>{t('先在季与剧集中添加一集，再来核对资料。')}</p>}<EpisodeField label="选择要核对的剧集" value={episode} episodes={episodes} optional={false} onChange={v=>requestLeave(()=>{setEpisode(v);setPreview(null)})}/><button type="button" disabled={!episode||busy||!hasPermission(currentSpace,'workspace.write')} onClick={()=>void build()}>{busy?t('正在核对资料…'):t('预览本集资料')}</button>{error&&<p className="error" role="alert">{error}</p>}
 {preview&&<section aria-label={t('本集资料预览')}><p>{t('本集纳入 {p0} 份资料，省略 {p1} 份。',{p0:preview.included.length,p1:preview.omitted.length})}</p>{preview.problems.map((p,i)=><p className="error" key={i}>{t(p.message)}</p>)}{preview.included.map(v=><article className="story-card" key={v.id}><div><h3>{v.title}</h3><p>{t('版本 {p0}',{p0:v.number})}</p><ReadBody body={v.body} names={names}/>{v.body.fact&&<><p>{v.audience_knows?t('观众在本集已知'):t('观众在本集尚未知情')}</p><p>{t('本集知情角色')} · {v.known_by.length?v.known_by.map(id=>names.get(id)??t('未找到的系列人物')).join('、'):t('尚无知情角色')}</p></>}</div></article>)}{!!preview.omitted.length&&<details><summary>{t('查看省略原因')}</summary>{preview.omitted.map(v=><p key={v.id}>{sources.get(v.id)?.title} · {t(reasons[v.reason]??'资料未纳入本集')} · {t('版本 {p0}',{p0:sources.get(v.id)?.number??''})}</p>)}</details>}<p className="drama-help">{t('预览固定了本次来源版本。资料或剧集顺序修改后，请重新核对。')}</p></section>}
 {episode&&<ContextSnapshots key={episode} root={root} episode={episode} space={space} sources={preview?.sources??[]} refresh={refresh}/>}
 </section>
}

function ContextSnapshots({root,episode,space,sources,refresh}:{root:string;episode:string;space:string;sources:StorySummary[];refresh:()=>void}){
 const{t}=useI18n();const{space:currentSpace,requestLeave}=useAppWorkspace();const[revision,setRevision]=useState(0),[shown,setShown]=useState<StoryContextSnapshot|null>(null),[loading,setLoading]=useState(false),[error,setError]=useState('')
 const[freshness,setFreshness]=useState<StoryContextFreshness|null>(null)
 const[checkPending,setCheckPending]=useState(false)
 const path=`/episodes/${episode}/context-snapshots`
 const saved=useCallback((v:StoryContextSnapshot)=>{setShown(v);setFreshness(null);setRevision(n=>n+1)},[])
 const write=useSeriesWrite<StoryContextSnapshot>(`story-context:${episode}`,path,'POST',saved)
 const seed=write.recovery as Partial<StoryContextSelection>|null
 const[query,setQuery]=useState(()=>seed?.query??''),[selected,setSelected]=useState<string[]>(()=>seed?.record_ids??[])
 const list=useDirectory<StoryContextSummary>(path,space,revision)
 const abort=useRef<AbortController|null>(null);useEffect(()=>()=>abort.current?.abort(),[])
 async function read(id:string){abort.current?.abort();const next=new AbortController();abort.current=next;setLoading(true);setError('');try{const v=await request<StoryContextSnapshot>(directoryURL(path+'/'+id,space),{signal:next.signal});if(!next.signal.aborted){setShown(v);setFreshness(null)}}catch(e){if(!next.signal.aborted)setError(errorMessage(e,t('上下文快照读取失败')))}finally{if(!next.signal.aborted)setLoading(false)}}
 async function check(){if(!shown)return;abort.current?.abort();const next=new AbortController();abort.current=next;setLoading(true);setError('');setFreshness(null);try{const v=await request<StoryContextFreshness>(directoryURL(path+'/'+shown.id+'/freshness',space),{signal:next.signal});if(!next.signal.aborted)setFreshness(v)}catch(e){if(!next.signal.aborted)setError(errorMessage(e,t('当前来源核对失败')))}finally{if(!next.signal.aborted)setLoading(false)}}

 const available=sources.length?sources:shown?.context.sources??[]
 const locked=write.busy||write.uncertain||!hasPermission(currentSpace,'workspace.write')
 const names=new Map(shown?.context.included.flatMap(v=>v.body.bible?.characters.map(c=>[c.id,c.name] as const)??[])??[])
 const titles=new Map(shown?.context.sources.map(v=>[v.id,v.title])??[])
 const reasons:Record<string,string>={archived:'资料已归档',outside_episode_range:'不在本集适用范围',future_event:'叙事尚未出现的事件',other_episode_cast:'其他剧集的选角',input_capacity:'超过本次输入容量',not_retrieved:'未命中本次检索条件'}
 return <section className="story-history story-snapshots" aria-label={t('本集资料快照')}><h3>{t('保存可追溯的本集资料')}</h3><p className="drama-help">{t('按关键词或明确选中的资料准备本集输入，故事圣经与必需资料始终优先核对。保存不会调用模型。')}</p>
 <form onSubmit={e=>{e.preventDefault();const save=()=>void write.save({query,record_ids:selected});if(checkPending&&!write.uncertain)requestLeave(save);else save()}}><fieldset disabled={locked}><label>{t('本次关注的关键词')}<input value={query} onChange={e=>{setQuery(e.target.value);write.markDirty()}} placeholder={t('例如：雨夜 误认 父亲')}/></label><p className="drama-help">{t('任一关键词命中即纳入；留空时按叙事范围准备资料。超出本次输入容量会列出原因，不会限制项目资料总量。')}</p>
 <StoryContextSourcePicker root={root} space={space} known={available} selected={selected} disabled={locked} onChange={ids=>{setSelected(ids);write.markDirty()}}/></fieldset>
 {write.error&&<p className="error" role="alert">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}{write.uncertain&&<p className="editing-note">{t('上次保存结果尚未确认。请用原请求核对，避免重复创建。')}</p>}
 {hasPermission(currentSpace,'workspace.write')&&<button type="submit" className="primary" disabled={write.busy}>{write.busy?t('正在保存…'):write.uncertain?t('核对并重试保存'):t('保存本集资料快照')}</button>}</form>
 <div className="series-heading"><h3>{t('已保存的上下文')}</h3><button type="button" onClick={()=>setRevision(n=>n+1)}>{t('刷新快照列表')}</button></div>
 {(error||list.error)&&<p role="alert" className="error">{error||list.error}</p>}{(list.loading||loading)&&<p role="status">{t('正在读取上下文…')}</p>}{!list.loading&&!list.error&&!list.items.length&&<p className="page-empty">{t('还没有本集快照。保存后可核对当时的资料和剧本。')}</p>}
 {list.items.map(v=><article className="story-card" key={v.id}><div><h4>{v.script_version>0?t('剧本版本 {p0}',{p0:v.script_version}):t('尚未保存剧本')}</h4><p>{t('纳入 {p0} 份资料，待处理 {p1} 项。',{p0:v.included_count,p1:v.problem_count})}</p><time dateTime={v.created_at}>{new Date(v.created_at).toLocaleString()}</time></div><button type="button" disabled={loading} onClick={()=>requestLeave(()=>void read(v.id))}>{t('查看当时资料')}</button></article>)}{list.next_cursor&&<button type="button" disabled={list.more} onClick={()=>void list.loadMore()}>{t('加载更多快照')}</button>}
 {shown&&<article className="editing-note" aria-label={t('已固定的本集上下文')}><h3>{t('这次保存的资料与剧本')}</h3><p>{t('这是历史快照，不会随正式资料编辑而变化。后续AI执行前还需核对来源是否更新。')}</p><button type="button" disabled={loading} onClick={()=>void check()}>{t('核对当前来源')}</button>{freshness&&<p role="status">{freshness.current?t('本次核对时来源版本未变，仍需处理快照中的待核对项。'):t('来源已更新。这份快照继续保留原稿；需要使用新稿时，请重新保存快照。')}</p>}{shown.context.problems.map((v,i)=><p role="alert" className="error" key={i}>{t(v.message)}</p>)}
 <p>{t('本集纳入 {p0} 份资料，省略 {p1} 份。',{p0:shown.context.included.length,p1:shown.context.omitted.length})}</p>{shown.context.included.map(v=><section key={v.id}><h4>{v.title} · {t('版本 {p0}',{p0:v.number})}</h4><ReadBody body={v.body} names={names}/></section>)}{!!shown.context.omitted.length&&<details><summary>{t('查看省略原因')}</summary>{shown.context.omitted.map(v=><p key={v.id}>{titles.get(v.id)} · {t(reasons[v.reason]??'资料未纳入本集')}</p>)}</details>}
 <details><summary>{t('查看当时剧本')}</summary>{shown.script?<><h4>{shown.script.title}</h4>{shown.script.scenes.map(v=><section key={v.id}><h4>{v.heading}</h4><p>{v.action}</p>{v.dialogue.map((line,i)=><p key={i}>{line.character}：{line.text}</p>)}</section>)}</>:<p>{t('保存时尚未填写剧本。')}</p>}</details></article>}
 {shown&&<StoryChecksPanel key={shown.id} space={space} episode={episode} snapshot={shown} onChanged={refresh} onPendingChange={setCheckPending}/>}
 </section>
}
