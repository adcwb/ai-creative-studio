import {useCallback,useState} from 'react'
import {useNavigationTask} from '../app/navigationGuard'
import {baseURL} from '../api'
import {useI18n} from '../i18n'
import {useSeriesWrite} from '../features/series/useSeriesWrite'
export interface AvatarReview {id:string;version:number;asset_id:string;outcome:'adopted'|'rework';note?:string;reviewed_by:string;created_at:string}
interface PreviewAsset {kind:string;name:string;file_url:string}
export function AvatarReviewPanel({project,job,asset,reviews,reference,audio,disabled}:{project:string;job:string;asset:string;reviews:AvatarReview[];reference?:PreviewAsset;audio?:PreviewAsset;disabled:boolean}){
 const {t}=useI18n(),[outcome,setOutcome]=useState<'adopted'|'rework'>('adopted'),[note,setNote]=useState(''),[checks,setChecks]=useState([false,false,false,false]),[last,setLast]=useState<AvatarReview|null>(null)
 const [dirty,setDirty]=useState(false)
 useNavigationTask(dirty)
 const saved=useCallback((v:AvatarReview)=>{setLast(v);setDirty(false)},[])
 const write=useSeriesWrite<AvatarReview>(`avatar-review:${job}`,`/projects/${project}/creative-jobs/${job}/avatar-reviews`,'POST',saved)
 const history=last&&!reviews.some(v=>v.version===last.version)?[...reviews,last]:reviews
 const current=history[history.length-1],locked=disabled||write.busy||write.uncertain
 const labels=['人物与参考形象一致','口型与配音同步','声音清晰且内容正确','已检查AI生成标识']
 return <section className="editing-note avatar-review" aria-label={t('数字人成片审阅')}><h4>{t('数字人成片审阅')}</h4><p>{t('人工检查此版本；保存审片只记录决定，不会自动发布、覆盖剪辑或重新生成。')}</p>
 {reference&&<details><summary>{t('对照原始形象与配音')}</summary><div className="avatar-review-sources"><div><p>{reference.name}</p>{reference.kind==='image'?<img alt={reference.name} src={baseURL+reference.file_url}/>:<video controls preload="metadata" src={baseURL+reference.file_url}/>}</div>{audio&&<div><p>{audio.name}</p><audio controls preload="none" src={baseURL+audio.file_url}/></div>}</div></details>}
 {current&&<p role="status">{t('最近审片决定')}：{t(current.outcome==='adopted'?'人工确认采用':'需要修改')} · {t('版本')} {current.version}</p>}
 <fieldset disabled={locked}><label>{t('审片决定')}<select value={outcome} onChange={e=>{setOutcome(e.target.value as 'adopted'|'rework');setDirty(true)}}><option value="adopted">{t('人工确认采用')}</option><option value="rework">{t('需要修改')}</option></select></label>{labels.map((label,i)=><label key={label}><input type="checkbox" checked={checks[i]} onChange={e=>{setChecks(v=>v.map((old,n)=>n===i?e.target.checked:old));setDirty(true)}}/>{t(label)}</label>)}<label>{t('具体修改意见或审片备注')}<textarea maxLength={2000} value={note} onChange={e=>{setNote(e.target.value);setDirty(true)}}/></label></fieldset>
 <button disabled={disabled||write.busy||!write.uncertain&&(outcome==='adopted'?!checks.every(Boolean):!note.trim())} onClick={()=>void write.save({expected_version:current?.version??0,asset_id:asset,outcome,identity_confirmed:checks[0],lip_sync_confirmed:checks[1],audio_confirmed:checks[2],disclosure_confirmed:checks[3],note})}>{t(write.uncertain?'核对原审片记录':'保存人工审片记录')}</button>
 {write.error&&<p role="alert">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}
 {history.length>0&&<details><summary>{t('查看审片历史')}</summary><ol>{history.map(v=><li key={v.id}>{t(v.outcome==='adopted'?'人工确认采用':'需要修改')} · {t('版本')} {v.version}{v.note&&<p>{v.note}</p>}</li>)}</ol></details>}
 </section>
}
