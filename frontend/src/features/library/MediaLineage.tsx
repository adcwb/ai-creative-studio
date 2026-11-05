import {useEffect, useRef, useState} from 'react'
import {errorMessage, request} from '../../api'
import {useI18n} from '../../i18n'
interface Lineage {asset_id:string;status:'recorded'|'recovered'|'unverifiable';library_sources:{asset_id:string;version_id:string}[];content_sha256?:string}
export function MediaLineage({space,project,asset}:{space:string;project:string;asset:string}) {
 const {t}=useI18n()
 const [view,setView]=useState<Lineage|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false)
 const active=useRef<AbortController|null>(null)
 useEffect(()=>()=>active.current?.abort(),[])
 async function inspect(){
  active.current?.abort();const abort=new AbortController();active.current=abort;setBusy(true);setError('')
  try {const value=await request<Lineage>(`/projects/${project}/media/assets/${asset}/lineage?${new URLSearchParams({space_id:space})}`,{signal:abort.signal});if(!abort.signal.aborted)setView(value)}catch(e){if(!abort.signal.aborted)setError(errorMessage(e,t('来源记录读取失败')))}finally{if(!abort.signal.aborted)setBusy(false)}
 }
 return <div className="media-lineage"><button type="button" disabled={busy} onClick={()=>void inspect()}>{t(busy?'正在核对来源…':view?'重新核对来源':'查看素材来源')}</button>{error&&<p className="error" role="alert">{error}</p>}{view&&<><p role="status">{t(view.status==='recorded'?'素材来源已有记录':view.status==='recovered'?'已根据原始记录追溯来源':'素材来源尚无法核对')}</p>{view.status==='unverifiable'?<p>{t('历史文件仍可查看和剪辑。请从已知来源重新制作后，再用于新的模型生成；不会凭名称补造来源。')}</p>:<p>{view.library_sources.length?t('关联 {p0} 份固定版本来源，生成前仍需核对当前使用授权。',{p0:view.library_sources.length}):t('来源记录不涉及资产库引用；仍需满足当前权限与模型能力。')}</p>}<p>{t('来源核对只读，不改写原文件或回执；不证明真实权利或模型效果。')}</p></>}</div>
}
