import {useCallback,useEffect,useState} from 'react'
import {errorMessage,request,type Space} from '../api'
import {useAppWorkspace} from '../app/workspace'
import {useI18n} from '../i18n'
import {useSeriesWrite} from '../features/series/useSeriesWrite'

export function TeamClosureActions({space}:{space:Space}){
 const {t}=useI18n(),{requestLeave}=useAppWorkspace(),[open,setOpen]=useState(false),[current,setCurrent]=useState<Space|null>(null),[error,setError]=useState(''),[revision,setRevision]=useState(0)
 useEffect(()=>{if(!open)return;const abort=new AbortController();void request<Space>(`/organizations/${space.id}/access`,{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setCurrent(v);setError('')}}).catch(e=>{if(!abort.signal.aborted)setError(errorMessage(e,t('团队状态读取失败')))});return()=>abort.abort()},[open,space.id,space.closed,space.version,revision,t])
 if(space.role!=='owner')return null
 return <details className="team-closure" onToggle={e=>setOpen(e.currentTarget.open)}><summary>{t(space.closed?'重新开放团队':'关闭团队与保留历史')}</summary>{open&&<>{error&&<p className="error" role="alert">{error}</p>}<button type="button" onClick={()=>requestLeave(()=>setRevision(n=>n+1))}>{t("重新读取团队状态")}</button>{current?<ClosureForm key={`${current.closed}:${current.version}`} space={current}/>:<p role="status">{t("正在读取团队状态…")}</p>}</>}</details>
}
function ClosureForm({space}:{space:Space}){
 const {t}=useI18n(),{refreshSpaces}=useAppWorkspace(),[name,setName]=useState(''),[confirmed,setConfirmed]=useState(false),[saved,setSaved]=useState(false)
 const done=useCallback(()=>{setSaved(true);refreshSpaces()},[refreshSpaces])
 const write=useSeriesWrite<{id:string;version:number;closed:boolean}>(`team-${space.closed?'reopen':'close'}:${space.id}`,`/organizations/${space.id}/${space.closed?'reopen':'close'}`,'POST',done)
 const locked=write.busy||write.uncertain||saved
 return <form onSubmit={e=>{e.preventDefault();void write.save({expected_version:space.version??1,name,confirmed:true})}}><p>{t(space.closed?'重新开放仅恢复所有者的编辑和执行权限，原成员不会自动重新加入。':'关闭会移除其他成员并停止本团队新的编辑与模型执行。所有者仍可读取、下载历史作品；不会物理删除媒体或撤销已发送的上游请求。')}</p><fieldset disabled={locked}><label>{t('输入完整团队名称确认')}<input value={name} onChange={e=>{setName(e.target.value);setConfirmed(false);write.markDirty()}} autoComplete="off" placeholder={space.name}/></label><label className="story-check"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/>{t('我已核对团队成员和历史作品的后续安排')}</label></fieldset>{write.error&&<p className="error" role="alert">{write.error}</p>}{write.uncertain&&<p className="editing-note">{t('团队状态结果未确认，请使用原请求核对，不要重复变更状态。')}</p>}{saved?<p role="status">{t('团队状态已更新')}</p>:<button type="submit" disabled={write.busy||(!write.uncertain&&(!confirmed||name!==space.name))}>{t(write.uncertain?'使用原请求核对结果':space.closed?'确认重新开放团队':'确认关闭团队')}</button>}</form>
}
