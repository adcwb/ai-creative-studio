import {useEffect,useRef,useState} from 'react'
import type {Space} from '../api'
import {errorMessage,request} from '../api'
import {useAppWorkspace} from '../app/workspace'
import {useNavigationTask} from '../app/navigationGuard'
import {useI18n} from '../i18n'
interface Access {id:string;name:string;version:number;role:string;owner_id:string;permissions:string[]}
export function TeamSpaceActions({space}:{space:Space}){
 const {t}=useI18n(),{refreshSpaces}=useAppWorkspace()
 const [mode,setMode]=useState<'rename'|'leave'|''>(''),[access,setAccess]=useState<Access|null>(null),[name,setName]=useState(space.name),[confirmed,setConfirmed]=useState(false),[busy,setBusy]=useState(false),[uncertain,setUncertain]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState('')
 const alive=useRef(true)
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[])
 useNavigationTask(busy||uncertain||(mode==='rename'&&name!==space.name))
 const canManage=space.permissions?.includes('members.manage')??(space.role==='owner'||space.role==='admin')
 async function open(next:'rename'|'leave'){
  setMode(next);setBusy(true);setError('');setNotice('');setConfirmed(false)
  try{const current=await request<Access>(`/organizations/${space.id}/access`);if(alive.current){setAccess(current);if(!uncertain)setName(current.name);setUncertain(false)}}catch(e){if(alive.current)setError(errorMessage(e,t('团队状态读取失败')))}finally{if(alive.current)setBusy(false)}
 }
 async function save(){
  if(!access||busy||uncertain||!confirmed)return
  setBusy(true);setError('')
  try{await request(`/organizations/${space.id}/${mode==='rename'?'settings':'leave'}`,{method:mode==='rename'?'PATCH':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({expected_version:access.version,...(mode==='rename'?{name}:{})})});if(alive.current){setMode('');setAccess(null);setNotice(t(mode==='rename'?'团队名称已更新':'已退出团队，团队作品仍保留在原空间'));refreshSpaces()}}
  catch(e){if(alive.current){setError(errorMessage(e,t('团队操作结果未确认，请重新读取团队状态')));setUncertain(true);setConfirmed(false)}}finally{if(alive.current)setBusy(false)}
 }
 return <div className="team-space-actions"><div className="media-actions">{canManage&&<button disabled={busy||Boolean(mode)} onClick={()=>void open('rename')}>{t('修改团队名称')}</button>}{space.role!=='owner'&&<button disabled={busy||Boolean(mode)} onClick={()=>void open('leave')}>{t('退出团队')}</button>}</div>
 {mode&&<section className="editing-note" aria-label={t(mode==='rename'?'修改团队名称':'退出团队')}><h3>{t(mode==='rename'?'修改团队名称':'退出团队')}</h3>{mode==='rename'?<label>{t('团队名称')}<input value={name} maxLength={80} disabled={busy||uncertain} onChange={e=>{setName(e.target.value);setConfirmed(false)}}/></label>:<p>{t('退出后将失去团队作品和模型的访问权限。不会删除团队作品；重新加入需要管理员授权。')}</p>}
 {access&&<p>{t('当前团队：{name} · 版本 {version}',{name:access.name,version:access.version})}</p>}
 {!uncertain&&<label><input type="checkbox" checked={confirmed} disabled={busy||!access} onChange={e=>setConfirmed(e.target.checked)}/>{t('我已核对并确认此操作')}</label>}
 <div className="media-actions"><button className="primary" disabled={busy||uncertain||!access||!confirmed||(mode==='rename'&&(!name.trim()||name.trim()===access.name))} onClick={()=>void save()}>{t(busy?'正在保存…':mode==='rename'?'保存团队名称':'确认退出团队')}</button>{uncertain?<button disabled={busy} onClick={()=>{refreshSpaces();void open(mode)}}>{t('重新读取团队状态')}</button>:<button disabled={busy} onClick={()=>{setMode('');setAccess(null);setName(space.name)}}>{t('取消')}</button>}</div></section>}
 {error&&<p role="alert" className="error">{error}</p>}{notice&&<p role="status">{notice}</p>}
 </div>
}
