import {useEffect,useRef,useState} from 'react'
import {ApiError,errorMessage,request} from '../../api'
import {useAppWorkspace} from '../../app/workspace'
import {useNavigationTask} from '../../app/navigationGuard'
import {translate} from '../../i18n'
import {directoryURL,type SeriesEntry} from './types'
const memoryPending=new Map<string,Pending>()
interface Pending {key:string;body:string;path:string;method:string}
function readPending(key:string,path:string,method:string):Pending|null{
 try{
  const p=JSON.parse(sessionStorage.getItem(key)??'null') as Pending|null
  if(p&&p.path===path&&p.method===method&&typeof p.body==='string'&&/^[a-zA-Z0-9_-]{8,128}$/.test(p.key)){
   const body:unknown=JSON.parse(p.body)
   if(body&&typeof body==='object'&&!Array.isArray(body))return p
  }
 }catch{/* Session storage may be unavailable; mounted-page recovery still works. */}
 const p=memoryPending.get(key);return p&&p.path===path&&p.method===method?p:null
}
export function useSeriesWrite(identity:string,path:string,method:string,onSaved:(n:SeriesEntry)=>void){
 const {space,session}=useAppWorkspace()
 const storageKey=`studio.series.pending:${session.user?.id??'demo-user'}:${space!.id}:${identity}`
 const [recovery]=useState(()=>readPending(storageKey,path,method))
 const pending=useRef<Pending|null>(recovery)
 const [busy,setBusy]=useState(false),[uncertain,setUncertain]=useState(Boolean(recovery)),[dirty,setDirty]=useState(Boolean(recovery)),[error,setError]=useState('')
 const [storageNotice,setStorageNotice]=useState('')
 const [saved,setSaved]=useState<SeriesEntry|null>(null)
 const alive=useRef(true),flight=useRef(false)
 useNavigationTask(dirty||busy||uncertain)
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[])
 useEffect(()=>{if(!saved)return;const timer=setTimeout(()=>onSaved(saved),0);return()=>clearTimeout(timer)},[saved,onSaved])
 async function save(body:unknown){
  if(flight.current)return
  flight.current=true;setBusy(true);setError('')
  if(!pending.current)pending.current={key:crypto.randomUUID(),body:JSON.stringify(body),path,method}
  memoryPending.set(storageKey,pending.current)
  try{sessionStorage.setItem(storageKey,JSON.stringify(pending.current))}catch{setStorageNotice(translate('浏览器无法保留恢复记录，请在离开前确认保存结果。'))}
  const attempt=pending.current
  try{
   const n=await request<SeriesEntry>(directoryURL(attempt.path,space!.id),{method:attempt.method,headers:{'Content-Type':'application/json','Idempotency-Key':attempt.key},body:attempt.body})
   try{sessionStorage.removeItem(storageKey)}catch{/* In-memory state is authoritative for this mounted form. */}
   memoryPending.delete(storageKey);pending.current=null
   if(alive.current){setDirty(false);setUncertain(false);setSaved(n)}
  }catch(reason){
   // A rejected retry cannot prove that the earlier ambiguous attempt was never accepted.
   const definite=reason instanceof ApiError&&reason.status<500&&!uncertain
   if(definite){memoryPending.delete(storageKey);pending.current=null;try{sessionStorage.removeItem(storageKey)}catch{/* No accepted request to restore. */}}
   if(alive.current){setUncertain(!definite);setError(errorMessage(reason,translate('保存结果尚未确认，请在本页重试核对。')))}
  }finally{flight.current=false;if(alive.current)setBusy(false)}
 }
 return {storageNotice,recovery:recovery?JSON.parse(recovery.body) as Record<string,unknown>:null,busy,uncertain,dirty,error,markDirty:()=>setDirty(true),save}
}
