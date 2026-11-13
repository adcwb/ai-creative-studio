import {hasPermission} from '../../permissions'
import {useCallback,useEffect,useRef,useState} from 'react'
import {errorMessage,request} from '../../api'
import {useAppWorkspace} from '../../app/workspace'
import {useI18n} from '../../i18n'
import {useNavigationTask} from '../../app/navigationGuard'
import {EpisodeField} from './StoryFields'
import {StoryBodyView} from './StoryBodyView'
import {directoryURL} from './types'
import {useDirectory} from './useDirectory'
import {useSeriesWrite} from './useSeriesWrite'
import type {StoryCast,StoryCastCharacter,StoryCastOptions,StoryEpisode,StoryInput,StoryVersion} from './storyTypes'

export function StoryCastPanel({root,space,episodes,onChanged}:{root:string;space:string;episodes:StoryEpisode[];onChanged:()=>void}){
 const{t}=useI18n();const{requestLeave}=useAppWorkspace();const[episode,setEpisode]=useState('')
 return <section className="series-form"><h2>{t('本集选角')}</h2><p className="drama-help">{t('把本集剧本角色明确对应到系列人物，可选用固定版本的角色资产。名字相同也不会自动关联；保存不会调用模型。')}</p><EpisodeField label="选择要核对的剧集" value={episode} episodes={episodes} optional={false} onChange={v=>requestLeave(()=>setEpisode(v))}/>{episode&&<LoadCast key={episode} root={root} space={space} episode={episode} onChanged={onChanged}/>}</section>
}
function LoadCast({root,space,episode,onChanged}:{root:string;space:string;episode:string;onChanged:()=>void}){
 const{t}=useI18n();const[initial,setInitial]=useState<StoryCastOptions|null>(null),[error,setError]=useState(''),[reload,setReload]=useState(0)
 useEffect(()=>{const abort=new AbortController();void request<StoryCastOptions>(directoryURL(`/episodes/${episode}/cast-options`,space),{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setInitial(v);setError('')}}).catch(e=>{if(!abort.signal.aborted)setError(errorMessage(e,t('本集选角读取失败')))});return()=>abort.abort()},[episode,space,reload,t])
 return <>{error&&<p className="error" role="alert">{error}<button type="button" onClick={()=>setReload(n=>n+1)}>{t('重新读取资料')}</button></p>}{initial?<CastEditor root={root} space={space} episode={episode} initial={initial} onChanged={onChanged}/>:!error&&<p role="status">{t('正在读取本集人物…')}</p>}</>
}
function CastEditor({root,space,episode,initial,onChanged}:{root:string;space:string;episode:string;initial:StoryCastOptions;onChanged:()=>void}){
 const{t}=useI18n();const{space:currentSpace}=useAppWorkspace();const[options,setOptions]=useState(initial),[saved,setSaved]=useState<StoryVersion|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false)
 const accepted=useCallback((v:StoryVersion)=>{setSaved(v);setExpected(v.number);setOptions(o=>({...o,current:o.current&&o.current.number>v.number?o.current:v}));onChanged()},[onChanged])
 const write=useSeriesWrite<StoryVersion>(`story-cast:${root}:${episode}`,`/episodes/${episode}/cast`,'PUT',accepted)
 const seed=write.recovery as Partial<StoryInput>|null
 const[expected,setExpected]=useState(()=>seed?.expected_version??initial.current?.number??0)
 const makeBasis=(o:StoryCastOptions):StoryCast=>({episode_id:episode,script_version:o.script_version,script_sha256:o.script_sha256,bible_version_id:o.bible?.version_id??'',characters:[]})
 const[cast,setCast]=useState<StoryCast>(()=>structuredClone(seed?.body?.cast??initial.current?.body.cast??makeBasis(initial)))
 const[status,setStatus]=useState<'active'|'archived'>(()=>seed?.status??initial.current?.status??'active')
 const abort=useRef<AbortController|null>(null);useEffect(()=>()=>abort.current?.abort(),[]);useNavigationTask(busy)
 const[assetsRevision,setAssetsRevision]=useState(0)
 const assets=useDirectory<{id:string;name:string;head_version_id:string}>(`/library/assets?kind=character&status=active`,space,assetsRevision)
 const chars=options.bible?.body.bible?.characters??[],script=(options.script?.characters??[]).filter(c=>Boolean(c.id))
 const locked=!hasPermission(currentSpace,'workspace.write')||busy||write.busy||write.uncertain
 const change=(fn:()=>void)=>{write.markDirty();setSaved(null);fn()}
 const entries=new Map(cast.characters.map(c=>[c.script_character_id,c]))
 const rows=[...script,...cast.characters.filter(c=>!script.some(s=>s.id===c.script_character_id)).map(c=>({id:c.script_character_id,name:c.script_character_name,description:''}))]
 const stale=cast.script_version!==options.script_version||cast.script_sha256!==options.script_sha256||cast.bible_version_id!==options.bible?.version_id
 function select(id:string,canonicalID:string){change(()=>setCast(old=>{const remaining=old.characters.filter(c=>c.script_character_id!==id);if(!canonicalID)return{...old,characters:remaining};const role=script.find(c=>c.id===id),person=chars.find(c=>c.id===canonicalID);if(!role||!person)return old;const previous=old.characters.find(c=>c.script_character_id===id);return{...old,characters:[...remaining,{script_character_id:id,script_character_name:role.name,series_character_id:canonicalID,series_character_name:person.name,notes:previous?.notes??'',...(previous?.series_character_id===canonicalID?{library_asset_id:previous.library_asset_id,asset_version_id:previous.asset_version_id,asset_name:previous.asset_name}:{})}]}}))}
 function update(id:string,patch:Partial<StoryCastCharacter>){change(()=>setCast(old=>({...old,characters:old.characters.map(c=>c.script_character_id===id?{...c,...patch}:c)})))}
 async function chooseAsset(id:string,assetID:string){if(!assetID){update(id,{library_asset_id:undefined,asset_version_id:undefined,asset_name:undefined});return}const asset=assets.items.find(a=>a.id===assetID);if(!asset)return;abort.current?.abort();const next=new AbortController();abort.current=next;setBusy(true);setError('');try{const v=await request<{id:string;card:{name:string}}>(directoryURL(`/library/assets/${asset.id}/versions/${asset.head_version_id}`,space),{signal:next.signal});if(!next.signal.aborted)update(id,{library_asset_id:asset.id,asset_version_id:v.id,asset_name:v.card.name})}catch(e){if(!next.signal.aborted)setError(errorMessage(e,t('角色资产版本读取失败')))}finally{if(!next.signal.aborted)setBusy(false)}}
 async function refresh(){abort.current?.abort();const next=new AbortController();abort.current=next;setBusy(true);setError('');try{const o=await request<StoryCastOptions>(directoryURL(`/episodes/${episode}/cast-options`,space),{signal:next.signal});if(!next.signal.aborted){setOptions(o);setAssetsRevision(n=>n+1)}}catch(e){if(!next.signal.aborted)setError(errorMessage(e,t('本集选角读取失败')))}finally{if(!next.signal.aborted)setBusy(false)}}
 function reconcile(){change(()=>{setExpected(options.current?.number??0);setCast(old=>({...makeBasis(options),characters:old.characters.map(c=>({...c,script_character_name:script.find(s=>s.id===c.script_character_id)?.name??c.script_character_name,series_character_name:chars.find(s=>s.id===c.series_character_id)?.name??c.series_character_name}))}))})}
 const invalid=options.problems.length>0||stale||cast.characters.some(c=>!script.some(x=>x.id===c.script_character_id)||!chars.some(x=>x.id===c.series_character_id))
 return <section aria-label={t('本集选角编辑器')}><p>{t('选角依据：剧本版本 {p0}',{p0:cast.script_version})}</p><p className="drama-help">{t('角色资产固定到所选版本；资产库以后改版不会替换已选版本。这不是锁脸保证或声音克隆授权。')}</p>
 {options.problems.map(p=><p className="error" key={p.code}>{t(p.message)}</p>)}{stale&&<p className="editing-note">{t('剧本或系列人物已变化。请比较后确认采用本次核对的来源，原选择仍保留。')}</p>}
 <form onSubmit={e=>{e.preventDefault();void write.save({expected_version:expected,title:t('本集选角'),kind:'cast',status,required:true,body:{cast,evidence:[]}})}}><fieldset disabled={locked}>
 {!rows.length&&<p className="page-empty">{t('本集剧本尚无角色。可以先在剧本中添加人物。')}</p>}
 {rows.map(role=>{const entry=entries.get(role.id),exists=script.some(s=>s.id===role.id);return <section className="story-card" key={role.id}><div><h3>{role.name}</h3><p>{role.description}</p>{!exists&&<p className="error">{t('此角色已不在当前剧本中，请明确移除旧关联。')}</p>}
 <label>{t('对应的系列人物')}<select aria-label={t('对应的系列人物：{name}',{name:role.name})} value={entry?.series_character_id??''} onChange={e=>select(role.id,e.target.value)}><option value="">{t('暂不关联')}</option>{entry&&!chars.some(c=>c.id===entry.series_character_id)&&<option value={entry.series_character_id}>{entry.series_character_name} · {t('当前不可用')}</option>}{chars.map(c=><option key={c.id} value={c.id}>{c.name}</option>)}</select></label>
 {entry&&<><p>{entry.script_character_name} · {entry.series_character_name}</p>{!exists&&<button type="button" onClick={()=>select(role.id,'')}>{t('移除此旧关联')}</button>}
 <label>{t('选择角色资产的当前版本（选填）')}<select aria-label={t('角色资产：{name}',{name:role.name})} value="" onChange={e=>void chooseAsset(role.id,e.target.value)}><option value="">{t('选择一个资产，保留目前固定的版本')}</option>{assets.items.map(a=><option value={a.id} key={a.id}>{a.name}</option>)}</select></label>{entry.asset_name&&<p>{t('固定的角色资产')} · {entry.asset_name} <button type="button" onClick={()=>void chooseAsset(role.id,'')}>{t('移除资产选择')}</button></p>}
 <label htmlFor={`cast-notes-${episode}-${role.id}`}>{t('本集人物状态与表演说明')}</label><textarea id={`cast-notes-${episode}-${role.id}`} value={entry.notes} onChange={e=>update(role.id,{notes:e.target.value})}/></>}
 </div></section>})}
 {assets.next_cursor&&<button type="button" disabled={assets.more} onClick={()=>void assets.loadMore()}>{t('加载更多角色资产')}</button>}{assets.error&&<p className="error" role="alert">{assets.error}</p>}
 <label>{t('选角状态')}<select value={status} onChange={e=>change(()=>setStatus(e.target.value as typeof status))}><option value="active">{t('正式使用')}</option><option value="archived">{t('已归档')}</option></select></label>
 <button type="button" onClick={()=>void refresh()}>{t('读取最新来源与选角')}</button><button type="button" onClick={reconcile} disabled={options.problems.length>0}>{t('核对后采用这些来源与版本，保留我的选择')}</button><p className="drama-help">{t('先读取并比较已保存的选角。确认只更新本次保存依据，保留你的角色对应与表演说明。')}</p>
 </fieldset>
 {options.current&&<details><summary>{t('对照已读取的选角版本 {p0}',{p0:options.current.number})}</summary><StoryBodyView body={options.current.body}/></details>}
 {(write.error||error)&&<p className="error" role="alert">{write.error||error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}{write.uncertain&&<p className="editing-note">{t('选角保存结果尚未确认，请核对原请求，避免重复追加版本。')}</p>}{saved&&<p role="status">{t('选角已保存为版本 {p0}',{p0:saved.number})}</p>}
 {hasPermission(currentSpace,'workspace.write')&&<button className="primary" disabled={busy||write.busy||(!write.uncertain&&invalid)}>{write.busy?t('正在保存…'):write.uncertain?t('核对原选角请求'):t('保存本集选角')}</button>}
 </form>
 {(saved??options.current)&&<CastHistory space={space} root={root} id={(saved??options.current)!.id} revision={(saved??options.current)!.number}/>}
 </section>
}

function CastHistory({space,root,id,revision}:{space:string;root:string;id:string;revision:number}){const{t}=useI18n();const[open,setOpen]=useState(false);return <section className="story-history"><button type="button" onClick={()=>setOpen(v=>!v)}>{t('查看选角版本历史')}</button>{open&&<CastVersions space={space} path={`/series/${root}/story-records/${id}/versions?limit=10`} revision={revision}/>}</section>}
function CastVersions({space,path,revision}:{space:string;path:string;revision:number}){const{t}=useI18n();const history=useDirectory<StoryVersion>(path,space,revision);return <>{history.error&&<p role="alert" className="error">{history.error}</p>}{history.items.map(v=><details key={v.version_id}><summary>{t('选角版本 {p0}',{p0:v.number})}</summary><StoryBodyView body={v.body}/></details>)}{history.next_cursor&&<button type="button" disabled={history.more} onClick={()=>void history.loadMore()}>{t('加载更多历史')}</button>}</>}
