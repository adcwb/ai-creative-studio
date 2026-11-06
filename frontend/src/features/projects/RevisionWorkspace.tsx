import {useCallback,useEffect,useState} from 'react'
import {ApiError,errorMessage,request} from '../../api'
import {useAppWorkspace} from '../../app/workspace'
import {useI18n} from '../../i18n'
import type {DramaScript,StoryboardPlan} from '../../drama'
import {DirectorDirection} from './DirectorWorkspace'
import {readSeriesRecovery,useSeriesWrite} from '../series/useSeriesWrite'

interface Branch {id:string;name:string;head_revision_id:string;version:number}
interface Summary {id:string;branch_id:string;label:string;content_sha256:string;actor:string;project_version:number;media_version:number;created_at:string;parent_id:string}
interface Detail extends Summary {manifest:{brief:{title:string;brief:string};script:DramaScript|null;storyboard:StoryboardPlan|null;timeline:{aspect_ratio:string;output_resolution?:string;clips:{id:string;asset_id:string;duration:number;subtitle:string}[]};poster:{width:number;height:number;layers:unknown[]}|null;sources:{script_origin:string;assets:{asset:{id:string;name:string}}[];contexts:unknown[];library_versions:unknown[];jobs:unknown[]}}}
interface Receipt {id:string;branch:Branch;revision?:Detail}
interface Basis {project_version:number;media_version:number;branch:Branch|null}
interface Capture {label:string;expected_project_version:number;expected_media_version:number;expected_branch_version:number;expected_head_revision_id:string}
function definite(e:unknown){return e instanceof ApiError&&e.status===409&&(e.code==='version_conflict'||e.code==='revision_version_conflict')}
export function RevisionWorkspace({project,space,disabled,onDirtyChange}:{project:string;space:string;disabled:boolean;onDirtyChange:(v:boolean)=>void}){
 const {session}=useAppWorkspace(),{t}=useI18n(),base=`/projects/${project}`,storageKey=`studio.revision.branch:${session.user?.id??'demo-user'}:${space}:${project}`
 const [branch,setBranch]=useState(()=>{try{return sessionStorage.getItem(storageKey)||'main'}catch{return 'main'}})
 const [open,setOpen]=useState(()=>Boolean(readSeriesRecovery(session.user?.id??'demo-user',space,`revision:capture:${project}`,`${base}/revision-branches/${branch}/revisions`,'POST')||readSeriesRecovery(session.user?.id??'demo-user',space,`revision:fork:${project}`,base+'/revision-branches','POST')))
 const [loaded,setLoaded]=useState(open)
 const choose=useCallback((id:string)=>{try{sessionStorage.setItem(storageKey,id)}catch{/* Original write recovery displays its own storage notice. */}setBranch(id)},[storageKey])
 return <details className="panel project-tools" open={open} onToggle={e=>{setOpen(e.currentTarget.open);if(e.currentTarget.open)setLoaded(true)}}><summary>{t('制作修订 · 留住每个方向')}</summary>{loaded&&<RevisionEditor key={`${project}:${space}:${branch}`} project={project} space={space} branch={branch} choose={choose} disabled={disabled} onDirtyChange={onDirtyChange}/>}</details>
}
function RevisionEditor({project,space,branch,choose,disabled,onDirtyChange}:{project:string;space:string;branch:string;choose:(id:string)=>void;disabled:boolean;onDirtyChange:(v:boolean)=>void}){
 const {t}=useI18n(),workspace=useAppWorkspace(),base=`/projects/${project}`,[refresh,setRefresh]=useState(0),[basis,setBasis]=useState<Basis|null>(null),[loading,setLoading]=useState(true),[error,setError]=useState(''),[refreshed,setRefreshed]=useState(false)
 const saved=useCallback(()=>{setLabel('');setRefresh(v=>v+1)},[])
 const captured=useSeriesWrite<Receipt>(`revision:capture:${project}`,`${base}/revision-branches/${branch}/revisions`,'POST',saved,definite)
 const original=captured.recovery as unknown as Capture|null
 const [label,setLabel]=useState(original?.label??'')
 const forked=useCallback((v:Receipt)=>choose(v.branch.id),[choose])
 const fork=useSeriesWrite<Receipt>(`revision:fork:${project}`,base+'/revision-branches','POST',forked)
 const forkRecovery=fork.recovery as {source_revision_id?:string;name?:string}|null
 const [source,setSource]=useState(forkRecovery?.source_revision_id??''),[name,setName]=useState(forkRecovery?.name??'')
 const [branches,setBranches]=useState<{items:Branch[];next_cursor:string}>({items:[],next_cursor:''}),[branchCursors,setBranchCursors]=useState([''])
 const [history,setHistory]=useState<{items:Summary[];next_cursor:string}>({items:[],next_cursor:''}),[historyCursors,setHistoryCursors]=useState([''])
 const [detail,setDetail]=useState<Detail|null>(null),[reading,setReading]=useState(false)
 const locked=disabled||workspace.space?.role==='viewer'||loading||captured.busy||captured.uncertain||fork.busy||fork.uncertain
 useEffect(()=>{const abort=new AbortController();void request<Basis>(`${base}/revision-options?space_id=${space}&branch_id=${branch}`,{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setBasis(v);setLoading(false);setError('');if(refresh)setRefreshed(true)}}).catch(e=>{if(!abort.signal.aborted){setError(errorMessage(e,t('制作修订读取失败')));setLoading(false)}});return()=>abort.abort()},[base,space,branch,refresh,t])
 const branchCursor=branchCursors.at(-1)??'',historyCursor=historyCursors.at(-1)??''
 useEffect(()=>{const abort=new AbortController();void request<typeof branches>(`${base}/revision-branches?space_id=${space}${branchCursor?'&cursor='+encodeURIComponent(branchCursor):''}`,{signal:abort.signal}).then(v=>{if(!abort.signal.aborted)setBranches(v)}).catch(e=>{if(!abort.signal.aborted)setError(errorMessage(e,t('制作分支读取失败')))});return()=>abort.abort()},[base,space,branchCursor,refresh,t])
 useEffect(()=>{const abort=new AbortController();void request<typeof history>(`${base}/revision-branches/${branch}/revisions?space_id=${space}${historyCursor?'&cursor='+encodeURIComponent(historyCursor):''}`,{signal:abort.signal}).then(v=>{if(!abort.signal.aborted)setHistory(v)}).catch(e=>{if(!abort.signal.aborted)setError(errorMessage(e,t('修订历史读取失败')))});return()=>abort.abort()},[base,branch,space,historyCursor,refresh,t])
 useEffect(()=>{onDirtyChange(captured.dirty||captured.uncertain||captured.busy||fork.dirty||fork.uncertain||fork.busy);return()=>onDirtyChange(false)},[onDirtyChange,captured.dirty,captured.uncertain,captured.busy,fork.dirty,fork.uncertain,fork.busy])
 const body:Capture={label,expected_project_version:basis?.project_version??0,expected_media_version:basis?.media_version??0,expected_branch_version:basis?.branch?.version??0,expected_head_revision_id:basis?.branch?.head_revision_id??''}
 async function read(id:string){setReading(true);setError('');try{setDetail(await request<Detail>(`${base}/revisions/${id}?space_id=${space}`));setSource(id)}catch(e){setError(errorMessage(e,t('修订来源读取失败')))}finally{setReading(false)}}
 const current=basis?.branch,choices=branches.items.filter(v=>v.id!=='main');if(current&&current.id!=='main'&&!choices.some(v=>v.id===current.id))choices.unshift(current)
 return <section className="series-form" aria-label={t('制作修订')}><p>{t('把当前剧本、镜头、剪辑和素材来源留成一份记录。保存不调用模型，不改动当前作品；切换分支只查看记录，不切换正在编辑的内容。')}</p>
 {loading&&<p role="status">{t('正在核对制作版本…')}</p>}{error&&<p role="alert" className="error">{error}</p>}
 <label>{t('制作分支')}<select aria-label={t('制作分支')} value={branch} disabled={locked} onChange={e=>{const id=e.target.value;workspace.requestLeave(()=>choose(id))}}><option value="main">{t('主线')}</option>{choices.map(v=><option key={v.id} value={v.id}>{v.name}</option>)}</select></label>
 <div className="media-actions"><button type="button" disabled={locked||branchCursors.length<2} onClick={()=>setBranchCursors(v=>v.slice(0,-1))}>{t('上一页制作分支')}</button><button type="button" disabled={locked||!branches.next_cursor} onClick={()=>setBranchCursors(v=>[...v,branches.next_cursor])}>{t('更多制作分支')}</button></div>
 {basis&&<p>{t('来源：项目版本 {project} · 素材版本 {media}',{project:basis.project_version,media:basis.media_version})} · {t('分支修订 {version}',{version:current?.version??0})}</p>}
 <button type="button" disabled={captured.busy||captured.uncertain||fork.busy||fork.uncertain||disabled} onClick={()=>{setLoading(true);setRefresh(v=>v+1)}}>{t('重新核对制作版本')}</button>{refreshed&&<p role="status">{t('已核对最新版本，保留了你的修订说明。保存前请确认当前作品。')}</p>}
 {(captured.error||fork.error)&&<p role="alert" className="error">{captured.error||fork.error}</p>}{(captured.storageNotice||fork.storageNotice)&&<p role="status">{captured.storageNotice||fork.storageNotice}</p>}
 {captured.uncertain&&<p className="editing-note">{t('上次修订结果待核对，请沿用原请求。当前作品后来有改动也不会替换原快照。')} <button type="button" disabled={disabled||captured.busy||workspace.space?.role==='viewer'} onClick={()=>void captured.save(body)}>{t('核对原修订请求')}</button></p>}
 {fork.uncertain&&<p className="editing-note">{t('上次分支结果待核对，请沿用原名称和起点核对。')} <button type="button" disabled={disabled||fork.busy||workspace.space?.role==='viewer'} onClick={()=>void fork.save({source_revision_id:source,name})}>{t('核对原分支请求')}</button></p>}
 <fieldset disabled={locked}><label>{t('修订说明')}<input aria-label={t('修订说明')} maxLength={80} value={label} onChange={e=>{setLabel(e.target.value);captured.markDirty()}} placeholder={t('例如：保留留白，缩短门前停顿')}/></label><button type="button" className="primary" disabled={!basis||!label.trim()||Boolean(error)} onClick={()=>void captured.save(body)}>{t('保存当前制作修订')}</button></fieldset>
 <h3>{t('这个分支的记录')}</h3>{!loading&&!history.items.length&&<p className="page-empty">{current?.head_revision_id?t('此分支保留了起点，保存当前制作后会增加独立修订。'):t('还没有制作修订，可以先留住当前方向。')}</p>}
 {current?.head_revision_id&&<button type="button" disabled={reading} onClick={()=>void read(current.head_revision_id)}>{t('查看分支当前起点')}</button>}
 <div className="media-exports">{history.items.map(v=><article key={v.id}><h4>{v.label}</h4><p>{t('来源：项目版本 {project} · 素材版本 {media}',{project:v.project_version,media:v.media_version})}</p><button type="button" disabled={reading} onClick={()=>void read(v.id)}>{t('查看此修订来源')}</button></article>)}</div>
 <div className="media-actions"><button type="button" disabled={historyCursors.length<2} onClick={()=>setHistoryCursors(v=>v.slice(0,-1))}>{t('较新制作修订')}</button><button type="button" disabled={!history.next_cursor} onClick={()=>setHistoryCursors(v=>[...v,history.next_cursor])}>{t('较早制作修订')}</button></div>
 {detail&&<section className="storyboard-shot" aria-label={t('固定修订来源')}><h3>{detail.label}</h3><p>{detail.manifest.brief.title} · {detail.manifest.brief.brief}</p><p>{t('固定素材 {assets} 份 · 正式上下文 {contexts} 份 · 资产版本 {versions} 份',{assets:detail.manifest.sources.assets.length,contexts:detail.manifest.sources.contexts.length,versions:detail.manifest.sources.library_versions.length})}</p><details><summary>{t('查看固定创作内容')}</summary><RevisionContent detail={detail}/></details><p>{t('这份记录不包含运行权限或发布授权。创建分支不会恢复旧权限，也不会重新生成素材。')}</p>
 <fieldset disabled={locked}><label>{t('新分支名称')}<input aria-label={t('新分支名称')} maxLength={80} value={name} onChange={e=>{setName(e.target.value);fork.markDirty()}}/></label><button type="button" disabled={!name.trim()} onClick={()=>void fork.save({source_revision_id:source,name})}>{t('从此修订建立分支')}</button></fieldset></section>}
 </section>
}

function RevisionContent({detail}:{detail:Detail}){
 const {t}=useI18n(),m=detail.manifest
 return <div className="drama-document"><h4>{t('固定剧本')}</h4>{m.script?<><p>{m.script.title} · {m.script.logline}</p>{m.script.scenes.map((scene,i)=><section className="drama-scene" key={scene.id??i}><h5>{scene.heading}</h5><p>{scene.action}</p>{scene.dialogue.map((line,j)=><p key={j}><strong>{line.character}</strong> · {line.text}</p>)}</section>)}</>:<p>{t('保存时尚无剧本。')}</p>}<h4>{t('固定镜头计划')}</h4>{m.storyboard?.shots.map(shot=><section className="storyboard-shot" key={shot.id}><p>{shot.description} · {shot.duration_seconds}s</p><DirectorDirection shot={shot}/></section>)}<h4>{t('固定剪辑')}</h4><p>{m.timeline.aspect_ratio} · {m.timeline.output_resolution}</p>{m.timeline.clips.map(clip=><section className="storyboard-shot" key={clip.id}><p>{m.sources.assets.find(a=>a.asset.id===clip.asset_id)?.asset.name} · {clip.duration}s</p>{clip.subtitle&&<p>{clip.subtitle}</p>}</section>)}{m.poster&&<p>{t('固定海报：{width} × {height}，{layers} 层',{width:m.poster.width,height:m.poster.height,layers:m.poster.layers.length})}</p>}</div>
}
