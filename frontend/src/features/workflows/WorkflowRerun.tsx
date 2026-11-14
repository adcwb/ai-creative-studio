import {useState} from 'react'
import {useAppWorkspace} from '../../app/workspace'
import {hasPermission} from '../../permissions'
import {useI18n} from '../../i18n'
import {useSeriesWrite} from '../series/useSeriesWrite'
import type {Run} from './types'
export function WorkflowRerun({project,run,node,saved}:{project:string;run:Run;node:string;saved:(r:Run)=>void}){
 const {t}=useI18n(),workspace=useAppWorkspace(),[open,setOpen]=useState(false),[confirmed,setConfirmed]=useState(false)
 const write=useSeriesWrite<Run>(`workflow-rerun:${run.id}:${node}`,`/projects/${project}/workflow-runs/${run.id}/reruns`,'POST',saved)
 if(!['succeeded','failed','cancelled'].includes(run.status))return null
 const disabled=!hasPermission(workspace.space,'workspace.write')
 return <div><button type="button" disabled={disabled} onClick={()=>setOpen(v=>!v)}>{t('从此步骤重新制作')}</button>{(open||write.uncertain)&&<div className="editing-note"><p>{t('此步骤及其下游将重新执行；其他已完成步骤沿用，旧运行和素材保留。结果未知时须先核对原任务。')}</p><label><input type="checkbox" checked={confirmed} disabled={disabled||write.busy||write.uncertain} onChange={e=>setConfirmed(e.target.checked)}/>{t('确认重新执行此步骤及下游')}</label><button disabled={disabled||write.busy||!confirmed&&!write.uncertain} onClick={()=>void write.save({node_id:node,expected_version:run.version,confirmed:true})}>{t(write.uncertain?'使用原请求核对结果':'开始局部重做')}</button>{write.error&&<p role="alert">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}</div>}</div>
}
