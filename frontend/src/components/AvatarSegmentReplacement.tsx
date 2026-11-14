import {useCallback,useState} from 'react'
import {useI18n} from '../i18n'
import {useSeriesWrite,readSeriesRecovery} from '../features/series/useSeriesWrite'
import {useAppWorkspace} from '../app/workspace'
import {avatarCutLabel,type AvatarSegmentProgress} from './avatarCuts'
export function AvatarSegmentReplacement({project,version,job,asset,segments,disabled,changed}:{project:string;version:number;job:string;asset:string;segments:AvatarSegmentProgress[];disabled:boolean;changed:()=>void}){
 const {t}=useI18n(),workspace=useAppWorkspace(),[accepted,setAccepted]=useState(false)
 const path=`/projects/${project}/creative-jobs/${job}/segment-replacements`,identity=`avatar-replace:${job}`
 const [recovery]=useState(()=>readSeriesRecovery(workspace.session.user?.id??'demo-user',workspace.space!.id,identity,path,'POST'))
 const [selected,setSelected]=useState<number[]>(()=>Array.isArray(recovery?.segments)?recovery.segments.filter((n):n is number=>typeof n==='number'&&Number.isInteger(n)&&n>0):[])
 const [confirmed,setConfirmed]=useState(()=>recovery?.confirmed===true)
 const saved=useCallback(()=>{setAccepted(true);setSelected([]);setConfirmed(false);changed()},[changed])
 const write=useSeriesWrite<{id:string}>(identity,path,'POST',saved)
 const locked=disabled||write.busy||write.uncertain
 return <section className="editing-note" aria-label={t('替换指定片段')}><h4>{t('替换指定片段')}</h4><p>{t('仅重新生成勾选片段，保留原配音与切点并复用其他片段，合成一个新版本。原成片和剪辑保持不变；接缝效果需要重新审片。')}</p>
 <fieldset disabled={locked}>{segments.map(seg=><label key={seg.index}><input type="checkbox" checked={selected.includes(seg.index)} onChange={e=>{setSelected(v=>e.target.checked?[...v,seg.index]:v.filter(n=>n!==seg.index));setAccepted(false);write.markDirty()}}/>{t('第 {index} 段',{index:seg.index})} · {seg.start_seconds.toFixed(3)}s–{(seg.start_seconds+seg.duration_seconds).toFixed(3)}s · {t(avatarCutLabel(seg.cut_reason))}</label>)}
 <label><input type="checkbox" checked={confirmed} onChange={e=>{setConfirmed(e.target.checked);write.markDirty()}}/>{t('我确认继续使用原形象、声音与云端传输授权，重新生成所选片段可能计费')}</label></fieldset>
 <button disabled={disabled||write.busy||!write.uncertain&&(!confirmed||selected.length===0)} onClick={()=>void write.save({source_asset_id:asset,expected_project_version:version,segments:[...selected].sort((a,b)=>a-b),confirmed:true})}>{t(write.uncertain?'核对原片段替换请求':'生成所选片段的新版本')}</button>
 {accepted&&<p role="status">{t('新版本已加入任务列表，请等待完成后审片。')}</p>}{write.uncertain&&<p role="status">{t('结果待核对，片段与授权确认已固定；重试使用原请求。')}</p>}{write.error&&<p role="alert">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}
 </section>
}
