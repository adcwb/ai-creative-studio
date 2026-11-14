import {useCallback,useEffect,useState} from 'react'
import {baseURL,errorMessage,request} from '../api'
import {useAppWorkspace} from '../app/workspace'
import {hasPermission} from '../permissions'
import {useI18n} from '../i18n'
import {useSeriesWrite} from '../features/series/useSeriesWrite'
import {directoryURL} from '../features/series/types'

interface Metadata{id:string;export_id:string;version:number;title:string;description:string;tags:string[];cover_asset_id?:string;rights_confirmed:boolean;created_at:string}
interface Cover{id:string;name:string;kind:string;mime_type:string;file_url:string}
interface View{version:number;items:Metadata[]}
export function DeliveryMetadata({project,exportID,space,assets,disabled,onSaved,onBusy}:{project:string;exportID:string;space:string;assets:Cover[];disabled:boolean;onSaved:()=>void;onBusy:(id:string,busy:boolean)=>void}){
 const {t}=useI18n(),[opened,setOpened]=useState(false)
 const reportBusy=useCallback((value:boolean)=>onBusy(exportID,value),[exportID,onBusy])
 return <details className="delivery-preparation" onToggle={e=>{if(e.currentTarget.open)setOpened(true)}}><summary>{t('发布资料与版本')}</summary>{opened&&<MetadataBoard project={project} exportID={exportID} spaceID={space} assets={assets} disabled={disabled} onSaved={onSaved} onBusy={reportBusy}/>}</details>
}
function MetadataBoard({project,exportID,spaceID,assets,disabled,onSaved,onBusy}:{project:string;exportID:string;spaceID:string;assets:Cover[];disabled:boolean;onSaved:()=>void;onBusy:(busy:boolean)=>void}){
 const {t}=useI18n(),[view,setView]=useState<View|null>(null),[error,setError]=useState(''),[revision,setRevision]=useState(0)
 const path=`/projects/${project}/media/exports/${exportID}/delivery-metadata`
 useEffect(()=>{const abort=new AbortController();void request<View>(directoryURL(path,spaceID),{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setView(v);setError('')}}).catch(e=>{if(!abort.signal.aborted)setError(errorMessage(e,t('发布资料读取失败')))});return()=>abort.abort()},[path,spaceID,revision,t])
 const saved=useCallback(()=>{setRevision(n=>n+1);onSaved()},[onSaved])
 return <section aria-label={t('成片发布资料')}><p className="drama-help">{t('标题、简介、标签和封面固定到本次正式成片。保存新版本保留历史，不修改视频，也不会自动发布。')}</p>{error&&<p className="error" role="alert">{error}<button type="button" onClick={()=>setRevision(n=>n+1)}>{t('重新读取资料')}</button></p>}{!view&&!error&&<p role="status">{t('正在读取发布资料…')}</p>}{view&&<><MetadataForm key={view.items.at(-1)?.id??'new'} path={path} view={view} assets={assets} disabled={disabled} onSaved={saved} onBusy={onBusy}/>{view.items.length>0&&<details><summary>{t('已保存的发布版本')}</summary>{[...view.items].reverse().map(v=><article key={v.id}><strong>{t('版本 {p0}',{p0:v.version})} · {v.title}</strong><p>{v.description}</p><p>{v.tags.join(' · ')}</p><a href={`${baseURL}/api/v1/projects/${project}/media/exports/${exportID}/delivery.zip?${new URLSearchParams({space_id:spaceID,metadata_version:String(v.version)})}`} download>{t('下载此发布版本交付包')}</a></article>)}</details>}</>}</section>
}
function MetadataForm({path,view,assets,disabled,onSaved,onBusy}:{path:string;view:View;assets:Cover[];disabled:boolean;onSaved:()=>void;onBusy:(busy:boolean)=>void}){
 const {t}=useI18n(),{space}=useAppWorkspace(),seed=view.items.at(-1)
 const write=useSeriesWrite<Metadata>(`delivery-metadata:${path}`,path,'POST',onSaved)
 const [title,setTitle]=useState(()=>String(write.recovery?.title??seed?.title??'')),[description,setDescription]=useState(()=>String(write.recovery?.description??seed?.description??'')),[tags,setTags]=useState(()=>((write.recovery?.tags as string[]|undefined)??seed?.tags??[]).join('\n')),[cover,setCover]=useState(()=>String(write.recovery?.cover_asset_id??seed?.cover_asset_id??'')),[confirmed,setConfirmed]=useState(false)
 useEffect(()=>{onBusy(write.busy||write.uncertain);return()=>onBusy(false)},[onBusy,write.busy,write.uncertain])
 const canWrite=hasPermission(space,'workspace.write'),locked=disabled||!canWrite||write.busy||write.uncertain,selected=assets.find(v=>v.id===cover)
 return <form onSubmit={e=>{e.preventDefault();void write.save({expected_metadata_version:seed?.version??0,title,description,tags:tags.split('\n').map(v=>v.trim()).filter(Boolean),cover_asset_id:cover,rights_confirmed:true})}}><fieldset disabled={locked}><label>{t('发布标题')}<input required maxLength={200} value={title} onChange={e=>{setTitle(e.target.value);setConfirmed(false);write.markDirty()}}/></label><label>{t('发布简介')}<textarea maxLength={12000} value={description} onChange={e=>{setDescription(e.target.value);setConfirmed(false);write.markDirty()}}/></label><label>{t('标签 · 每行一个')}<textarea value={tags} onChange={e=>{setTags(e.target.value);setConfirmed(false);write.markDirty()}}/></label><label>{t('交付封面')}<select value={cover} onChange={e=>{setCover(e.target.value);setConfirmed(false);write.markDirty()}}><option value="">{t('暂不附封面')}</option>{assets.filter(v=>v.kind==='image'&&['image/png','image/jpeg','image/webp'].includes(v.mime_type)).map(v=><option key={v.id} value={v.id}>{v.name}</option>)}</select></label>{selected&&<img className="delivery-cover" src={`${baseURL}${selected.file_url}`} alt={t('选定交付封面')} loading="lazy"/>}<label className="story-check"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/>{t('我已核对发布资料和所用素材的传播权利')}</label><p className="drama-help">{t('此确认为创作者声明，不代表平台完成许可认证或内容审核。请按目标平台要求自行核对。')}</p></fieldset>{write.error&&<p role="alert" className="error">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}{write.uncertain&&<p className="editing-note">{t('发布资料保存结果未确认，请使用原请求核对，历史版本不会被覆盖。')}</p>}{disabled&&!write.uncertain&&<p className="drama-help">{t('请先保存或放弃当前剪辑修改，再保存发布资料。')}</p>}{canWrite&&<button type="submit" disabled={write.busy||(!write.uncertain&&(disabled||!confirmed||!title.trim()))}>{t(write.uncertain?'使用原请求核对结果':'保存发布资料版本')}</button>}</form>
}
