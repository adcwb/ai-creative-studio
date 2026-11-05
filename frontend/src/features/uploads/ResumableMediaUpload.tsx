import {useCallback,useEffect,useRef,useState} from 'react'
import {request,errorMessage,ApiError} from '../../api'
import {useAppWorkspace} from '../../app/workspace'
import {useI18n} from '../../i18n'
import {FileSHA256} from './sha256'
import './uploads.css'
interface UploadInput {name:string;mime_type:string;size_bytes:number;sha256:string;role:string;character:string;shot_id:string}
export interface UploadSession extends UploadInput {id:string;space_id:string;project_id:string;actor_id:string;state:'receiving'|'completed'|'cancelled';offset:number;chunk_size:number;uploaded_asset_id?:string;expires_at:string}
interface PendingCreate {key:string;input:UploadInput}
export function ResumableMediaUpload({space,project,inputID,label,role,character='',shotID='',accept='image/png,image/jpeg,audio/wav,audio/mpeg,video/mp4',disabled=false,onBusy,onComplete}:{space:string;project:string;inputID:string;label:string;role:string;character?:string;shotID?:string;accept?:string;disabled?:boolean;onBusy:(busy:boolean)=>void;onComplete:(session:UploadSession)=>void|Promise<void>}) {
  const {session}=useAppWorkspace(),{t,formatDate}=useI18n()
  const storageKey=`upload.pending:${session.user?.id??'demo'}:${space}:${project}`
  const path=`/projects/${encodeURIComponent(project)}/media/upload-sessions`,query=`?${new URLSearchParams({space_id:space})}`
  const [items,setItems]=useState<UploadSession[]>([]),[after,setAfter]=useState(''),[error,setError]=useState(''),[busy,setBusy]=useState(false),[progress,setProgress]=useState(0),[phase,setPhase]=useState(''),[unconfirmed,setUnconfirmed]=useState<PendingCreate|null>(()=>{try{return JSON.parse(sessionStorage.getItem(storageKey)??'null')}catch{return null}})
  const input=useRef<HTMLInputElement>(null),target=useRef<UploadSession|null>(null),controller=useRef<AbortController|null>(null),alive=useRef(true)
  const load=useCallback(async(cursor='')=>{const result=await request<{items:UploadSession[];next_after:string}>(`${path}${query}&limit=20${cursor?`&after=${cursor}`:''}`);if(alive.current){setItems(previous=>cursor?[...previous,...result.items]:result.items);setAfter(result.next_after)}},[path,query])
  useEffect(()=>{alive.current=true;void load().catch(e=>{if(alive.current)setError(errorMessage(e,t('上传进度读取失败')))});return()=>{alive.current=false;controller.current?.abort()}},[load,t])
  useEffect(()=>{onBusy(busy);return()=>onBusy(false)},[busy,onBusy])
  function remember(value:PendingCreate|null){if(alive.current)setUnconfirmed(value);try{if(value)sessionStorage.setItem(storageKey,JSON.stringify(value));else sessionStorage.removeItem(storageKey)}catch{/* In-memory identity remains available. */}}
  async function accepted(u:UploadSession){if(u.state==='completed'){remember(null);if(alive.current)setItems(previous=>previous.filter(x=>x.id!==u.id));try{await onComplete(u)}catch{if(alive.current)setError(t('素材已保存，列表刷新失败，请重新读取素材'))}return true}return false}
  async function finish(u:UploadSession,signal:AbortSignal){const done=await request<UploadSession>(`${path}/${u.id}/complete${query}`,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':`complete-${u.id}`},body:'{}',signal,timeoutMs:300000});await accepted(done)}
  function begin(){if(controller.current)return null;const next=new AbortController();controller.current=next;setBusy(true);setError('');setProgress(0);return next}
  async function end(){controller.current=null;if(alive.current){setBusy(false);setPhase('');await load().catch(()=>{})}}
  async function run(file:File){
    const operation=begin();if(!operation)return
    const resume=target.current;target.current=null
    let creating=false
    try {
      const mime=file.type==='audio/x-wav'?'audio/wav':file.type
      if(!['image/png','image/jpeg','audio/wav','audio/mpeg','video/mp4'].includes(mime)||file.size<1||!Number.isSafeInteger(file.size))throw new Error(t('请选择有效的PNG、JPEG、MP4、WAV或MP3文件'))
      setPhase(t('正在校验文件'))
      const hash=new FileSHA256(),step=8*1024*1024
      for(let offset=0;offset<file.size;offset+=step){operation.signal.throwIfAborted();hash.update(new Uint8Array(await file.slice(offset,offset+step).arrayBuffer()));if(alive.current)setProgress(Math.round(Math.min(file.size,offset+step)/file.size*100));await new Promise(resolve=>setTimeout(resolve,0))}
      const sha=hash.hex()
      let u=resume??(!unconfirmed?items.find(item=>Date.parse(item.expires_at)>Date.now()&&item.sha256===sha&&item.size_bytes===file.size&&item.mime_type===mime&&item.role===role&&item.character===character&&item.shot_id===shotID)??null:null)
      if(u&&(u.sha256!==sha||u.size_bytes!==file.size||u.mime_type!==mime))throw new Error(t('所选文件与原上传不一致，请重新选择原文件'))
      if(!u){const pending=unconfirmed??{key:crypto.randomUUID(),input:{name:file.name,mime_type:mime,size_bytes:file.size,sha256:sha,role,character,shot_id:shotID}};if(pending.input.sha256!==sha||pending.input.size_bytes!==file.size||pending.input.mime_type!==mime)throw new Error(t('请先恢复或取消上次未确认的上传'));remember(pending);creating=true;u=await request<UploadSession>(`${path}${query}`,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':pending.key},body:JSON.stringify(pending.input),signal:operation.signal});creating=false;remember(null)}else{u=await request<UploadSession>(`${path}/${u.id}${query}`,{signal:operation.signal})}
      if(await accepted(u))return
      if(u.state!=='receiving')throw new Error(t('上传会话已关闭，请重新选择文件开始'))
      setPhase(t('正在上传'));setProgress(Math.floor(u.offset/u.size_bytes*100))
      while(u.offset<u.size_bytes){operation.signal.throwIfAborted();const offset:number=u.offset;const chunk:Blob=file.slice(offset,offset+u.chunk_size);const digest=await crypto.subtle.digest('SHA-256',await chunk.arrayBuffer());const sha=Array.from(new Uint8Array(digest),b=>b.toString(16).padStart(2,'0')).join('');u=await request<UploadSession>(`${path}/${u.id}/parts/${offset}${query}`,{method:'PUT',headers:{'Content-Type':'application/octet-stream','X-Chunk-SHA256':sha,'Idempotency-Key':`${u.id}-${offset}`},body:chunk,signal:operation.signal});if(alive.current)setProgress(Math.floor(u.offset/u.size_bytes*100))}
      setPhase(t('正在校验并保存素材'));await finish(u,operation.signal)
    }catch(e){if(creating&&!unconfirmed&&e instanceof ApiError&&[400,401,403,404,409,410,415].includes(e.status))remember(null);if(alive.current)setError(operation.signal.aborted?t('上传已暂停，已确认的进度会保留'):errorMessage(e,t('上传中断，请恢复原会话')))}finally{await end()}
  }
  async function complete(u:UploadSession){const operation=begin();if(!operation)return;setPhase(t('正在校验并保存素材'));try{await finish(u,operation.signal)}catch(e){if(alive.current)setError(errorMessage(e,t('上传中断，请恢复原会话')))}finally{await end()}}
  async function cancel(u?:UploadSession){const operation=begin();if(!operation)return;try{if(!u&&unconfirmed){u=await request<UploadSession>(`${path}${query}`,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':unconfirmed.key},body:JSON.stringify(unconfirmed.input),signal:operation.signal});if(await accepted(u))return}if(u)await request(`${path}/${u.id}/cancel${query}`,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':`cancel-${u.id}`},body:'{}',signal:operation.signal});remember(null)}catch(e){if(alive.current)setError(errorMessage(e,t('上传取消尚未确认，请重试')))}finally{await end()}}
  function pick(u:UploadSession|null){target.current=u;input.current?.click()}
  return <div className="resumable-upload">
    <label htmlFor={inputID}>{label}</label><input ref={input} id={inputID} type="file" accept={accept} disabled={disabled||busy} onChange={event=>{const file=event.target.files?.[0];event.target.value='';if(file)void run(file)}}/>
    {busy&&<div role="status"><p>{phase}{phase===t('正在校验并保存素材')?'':` · ${progress}%`}</p><progress aria-label={t('上传进度')} max="100" value={progress}/><button type="button" onClick={()=>controller.current?.abort()}>{t('暂停上传')}</button></div>}
    {error&&<p role="alert" className="error">{error}</p>}
    {unconfirmed&&<div className="editing-note"><p>{t('上次上传创建结果尚未确认')} · {unconfirmed.input.name}</p><button type="button" disabled={busy||disabled} onClick={()=>pick(null)}>{t('重选原文件以恢复')}</button><button type="button" disabled={busy||disabled} onClick={()=>void cancel()}>{t('取消这次上传')}</button></div>}
    {items.length>0&&<details open><summary>{t('可恢复的上传')}</summary><ul>{items.map(u=><li key={u.id}><strong>{u.name}</strong><span>{Math.floor(u.offset/u.size_bytes*100)}% · {t('临时进度保留至')} {formatDate(u.expires_at)}</span><div><button type="button" disabled={busy||disabled||!!unconfirmed} onClick={()=>u.offset===u.size_bytes?void complete(u):pick(u)}>{t(u.offset===u.size_bytes?'完成校验':'重选原文件以恢复')}</button><button type="button" disabled={busy||disabled||!!unconfirmed} onClick={()=>void cancel(u)}>{t('取消这次上传')}</button></div></li>)}</ul>{after&&<button disabled={busy} onClick={()=>void load(after).catch(e=>setError(errorMessage(e,t('上传进度读取失败'))))}>{t('加载更多')}</button>}</details>}
  </div>
}
