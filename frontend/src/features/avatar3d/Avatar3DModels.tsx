import {useCallback,useEffect,useRef,useState} from 'react'
import {FileSHA256} from '../uploads/sha256'
import {useI18n} from '../../i18n'
import {useAppWorkspace} from '../../app/workspace'
import {useNavigationTask} from '../../app/navigationGuard'
import {useSeriesWrite} from '../series/useSeriesWrite'
import {ApiError,errorMessage,request} from '../../api'
import type {Avatar3DModel} from './Avatar3DStage'
interface Upload {sha256:string;key:string;metadata:{name:string;body:string;license_reference:string;consent_reference:string;confirmed:boolean};filename:string;size:number;modified:number}
function recover(key:string):Upload|null {try{const v=JSON.parse(sessionStorage.getItem(key)??'null') as Upload|null;return v&&/^[\w-]{8,128}$/.test(v.key)&&v.metadata&&typeof v.metadata.name==='string'&&typeof v.metadata.license_reference==='string'&&typeof v.metadata.consent_reference==='string'&&['M','F'].includes(v.metadata.body)&&v.metadata.confirmed===true&&typeof v.sha256==='string'&&/^[a-f0-9]{64}$/.test(v.sha256)&&typeof v.filename==='string'&&Number.isFinite(v.size)?v:null}catch{return null}}
export function Avatar3DModels({project,space,disabled,saved}:{project:string;space:string;disabled:boolean;saved:(v:Avatar3DModel)=>void}){
 const {t}=useI18n(),workspace=useAppWorkspace(),path=`/projects/${project}/avatar-3d-models`,storageKey=`studio.avatar3d.upload:${workspace.session.user?.id??'demo-user'}:${space}:${project}`
 const [pending,setPending]=useState(()=>recover(storageKey)),[name,setName]=useState(''),[license,setLicense]=useState(''),[consent,setConsent]=useState(''),[body,setBody]=useState('F'),[confirmed,setConfirmed]=useState(false),[file,setFile]=useState<File|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(''),[storageError,setStorageError]=useState(false)
 const input=useRef<HTMLInputElement>(null),flight=useRef(false),alive=useRef(true)
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[])
 const builtinSaved=useCallback((v:Avatar3DModel)=>saved(v),[saved])
 const builtin=useSeriesWrite<Avatar3DModel>(`avatar3d-builtin:${project}`,path,'POST',builtinSaved)
 useNavigationTask(busy||!!pending||!!name||!!license||!!consent||!!file)
 function done(v:Avatar3DModel){try{sessionStorage.removeItem(storageKey)}catch{/* Mounted state remains authoritative. */}if(alive.current){setPending(null);setName('');setLicense('');setConsent('');setConfirmed(false);setFile(null);if(input.current)input.current.value='';saved(v)}}
 async function lookup(){if(!pending||flight.current)return;flight.current=true;setBusy(true);setError('');try{done(await request<Avatar3DModel>(`${path}/requests/${pending.key}/result?space_id=${space}`))}catch(e){if(alive.current)setError(e instanceof ApiError&&e.status===404?t('尚未找到登记结果，请重新选择原GLB并重试同一上传。'):errorMessage(e,t('模型登记结果尚未确认。')))}finally{flight.current=false;if(alive.current)setBusy(false)}}
 async function upload(){
  if(!file||flight.current||disabled)return
  flight.current=true;setBusy(true);setError('')
  try{
   const hash=new FileSHA256()
   for(let offset=0;offset<file.size;offset+=1024*1024){if(!alive.current)throw new DOMException('Upload cancelled','AbortError');hash.update(new Uint8Array(await file.slice(offset,offset+1024*1024).arrayBuffer()));await new Promise(resolve=>setTimeout(resolve,0))}
   const sha256=hash.hex()
   if(pending&&(file.size!==pending.size||sha256!==pending.sha256)){setError(t('请选择原文件；恢复上传不会更换原许可和请求。'));return}
   const attempt=pending??{sha256,key:crypto.randomUUID(),metadata:{name:name.trim(),body,license_reference:license.trim(),consent_reference:consent.trim(),confirmed},filename:file.name,size:file.size,modified:file.lastModified}
   setPending(attempt);try{sessionStorage.setItem(storageKey,JSON.stringify(attempt))}catch{setStorageError(true)}
   const form=new FormData();form.append('metadata',new Blob([JSON.stringify(attempt.metadata)],{type:'application/json'}));form.append('model',file)
   done(await request<Avatar3DModel>(`${path}?space_id=${space}`,{method:'POST',headers:{'Idempotency-Key':attempt.key},body:form,timeoutMs:300000}))
  }catch(e){if(alive.current){if(!pending&&e instanceof ApiError&&e.status<500){setPending(null);try{sessionStorage.removeItem(storageKey)}catch{/* Optional. */}}setError(errorMessage(e,t('上传结果尚未确认，请核对原请求。')))}}finally{flight.current=false;if(alive.current)setBusy(false)}
 }

 return <details className="panel"><summary>{t('添加三维角色')}</summary><div className="avatar3d-models"><section><h3>{t('CC0开源角色')}</h3><p>{t('内置MPFB角色采用CC0许可，不代表其他网站模型也可商用。点击后登记到当前作品，首次加载使用压缩资源。')}</p><a href="/avatars/README.md" target="_blank" rel="noreferrer">{t('查看模型与代码许可来源')}</a><p><button disabled={disabled||builtin.busy||busy||!!pending} onClick={()=>void builtin.save({name:t('开源三维角色'),builtin:'mpfb-cc0-v1',body:'F',license_reference:'CC0 1.0 · MPFB',consent_reference:'Creator selected the fictional CC0 MPFB character',confirmed:true})}>{t(builtin.uncertain?'核对原角色登记':'保存并使用CC0角色')}</button></p>{builtin.error&&<p role="alert">{builtin.error}</p>}</section>
 <section><h3>{t('使用自己的GLB角色')}</h3><p>{t('需要自包含GLB2、兼容骨骼和面部形变；照片不能直接当作GLB。外链贴图和远端解码器扩展会被拒绝。')}</p><fieldset disabled={disabled||busy||!!pending||builtin.busy||builtin.uncertain}><label>{t('角色名称')}<input maxLength={40} value={name} onChange={e=>setName(e.target.value)}/></label><label>{t('骨架模板')}<select value={body} onChange={e=>setBody(e.target.value)}><option value="F">{t('女性骨架')}</option><option value="M">{t('男性骨架')}</option></select></label><label>{t('模型许可依据')}<input maxLength={500} value={license} onChange={e=>setLicense(e.target.value)} placeholder={t('许可文件、购买记录或原创声明')}/></label><label>{t('形象使用依据')}<input maxLength={500} value={consent} onChange={e=>setConsent(e.target.value)}/></label><label><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/>{t('我确认模型、贴图与形象有权用于本作品，平台未代为核验商业许可')}</label></fieldset>
 <label>{t('选择GLB文件')}<input ref={input} type="file" accept=".glb,model/gltf-binary" disabled={disabled||busy||builtin.busy||builtin.uncertain} onChange={e=>setFile(e.target.files?.[0]??null)}/></label><button disabled={disabled||busy||!file||!pending&&(!name.trim()||license.trim().length<3||consent.trim().length<3||!confirmed)} onClick={()=>void upload()}>{t(busy?'正在处理模型…':pending?'重试同一GLB上传':'上传并保存角色')}</button>
 {pending&&<p role="status">{t('原上传待核对；浏览器刷新不会保留文件，请重新选择同一GLB，许可与请求保持不变。')}<button disabled={disabled||busy} onClick={()=>void lookup()}>{t('核对原模型登记')}</button></p>}{error&&<p role="alert">{error}</p>}{storageError&&<p role="status">{t('浏览器无法保留恢复记录，请在离开前确认保存结果。')}</p>}</section></div></details>
}
export function Avatar3DRevoke({project,model,disabled,saved}:{project:string;model:Avatar3DModel;disabled:boolean;saved:(v:Avatar3DModel)=>void}){
 const {t}=useI18n(),write=useSeriesWrite<Avatar3DModel>(`avatar3d-revoke:${model.id}`,`/projects/${project}/avatar-3d-models/${model.id}/revoke`,'POST',saved)
 return <section className="editing-note"><p>{model.license_reference}</p><p>{model.consent_reference}</p>{model.revoked_at?<p role="status">{t('此模型授权已撤销，不能开始新的口播播放。')}</p>:<button disabled={disabled||write.busy} onClick={()=>void write.save({confirmed:true})}>{t(write.uncertain?'核对原授权撤销':'撤销此模型授权')}</button>}{write.error&&<p role="alert">{write.error}</p>}<p className="drama-help">{t('撤销会阻止新的使用；已下载到设备的模型与音频无法被平台远程回收。')}</p></section>
}
