import {useEffect,useRef,useState} from 'react'
import type {Space} from '../api'
import {errorMessage,request} from '../api'
import {useAppWorkspace} from '../app/workspace'
import {useNavigationTask} from '../app/navigationGuard'
import {useI18n} from '../i18n'
interface Access {id:string;name:string;version:number;role:string;owner_id:string;permissions:string[]}
export function TeamSpaceActions({space}:{space:Space}){
 const {t}=useI18n(),{refreshSpaces}=useAppWorkspace()
 const [mode,setMode]=useState<'rename'|'leave'|'transfer'|''>(''),[access,setAccess]=useState<Access|null>(null),[name,setName]=useState(space.name),[confirmed,setConfirmed]=useState(false),[busy,setBusy]=useState(false),[uncertain,setUncertain]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState('')
 const [members,setMembers]=useState<{id:string;email:string}[]>([]),[target,setTarget]=useState('')
 const alive=useRef(true)
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[])
 useNavigationTask(busy||uncertain||(mode==='rename'&&name!==space.name))
 const canManage=space.permissions?.includes('workspace.manage')??(space.role==='owner'||space.role==='admin')
 async function open(next:'rename'|'leave'|'transfer'){
  setMode(next);setBusy(true);setError('');setNotice('');setConfirmed(false)
  try{const [current,list]=await Promise.all([request<Access>(`/organizations/${space.id}/access`),next==='transfer'?request<{id:string;email:string}[]>(`/organizations/${space.id}/members`):Promise.resolve([])]);if(alive.current){if(next==='transfer'&&uncertain&&target&&current.owner_id===target){setMode('');setUncertain(false);setAccess(null);setNotice(t('团队所有权已交接，原所有者保留管理员权限'));refreshSpaces();return}setAccess(current);setMembers(list);if(!uncertain){setName(current.name);setTarget('')}setUncertain(false)}}catch(e){if(alive.current)setError(errorMessage(e,t('团队状态读取失败')))}finally{if(alive.current)setBusy(false)}
 }
 async function save(){
  if(!access||busy||uncertain||!confirmed)return
  setBusy(true);setError('')
  try{await request(`/organizations/${space.id}/${mode==='rename'?'settings':mode==='transfer'?'transfer':'leave'}`,{method:mode==='rename'?'PATCH':'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({expected_version:access.version,...(mode==='rename'?{name}:mode==='transfer'?{target_user_id:target,confirmed:true}:{})})});if(alive.current){setMode('');setAccess(null);setNotice(t(mode==='rename'?'团队名称已更新':mode==='transfer'?'团队所有权已交接，原所有者保留管理员权限':'已退出团队，团队作品仍保留在原空间'));refreshSpaces()}}
  catch(e){if(alive.current){setError(errorMessage(e,t('团队操作结果未确认，请重新读取团队状态')));setUncertain(true);setConfirmed(false)}}finally{if(alive.current)setBusy(false)}
 }
 return <div className="team-space-actions"><div className="media-actions">{canManage&&<button disabled={busy||Boolean(mode)} onClick={()=>void open('rename')}>{t('修改团队名称')}</button>}{space.role==='owner'&&<button disabled={busy||Boolean(mode)} onClick={()=>void open('transfer')}>{t('移交团队所有权')}</button>}{space.role!=='owner'&&<button disabled={busy||Boolean(mode)} onClick={()=>void open('leave')}>{t('退出团队')}</button>}</div>
 {mode&&<section className="editing-note" aria-label={t(mode==='rename'?'修改团队名称':mode==='transfer'?'移交团队所有权':'退出团队')}><h3>{t(mode==='rename'?'修改团队名称':mode==='transfer'?'移交团队所有权':'退出团队')}</h3>{mode==='rename'?<label>{t('团队名称')}<input value={name} maxLength={80} disabled={busy||uncertain} onChange={e=>{setName(e.target.value);setConfirmed(false)}}/></label>:mode==='transfer'?<><p>{t('请先通过可信渠道与目标成员确认交接。新所有者将管理全部团队权限，原所有者成为管理员；作品仍留在此团队。')}</p><label>{t('新的团队所有者')}<select value={target} disabled={busy||uncertain} onChange={e=>{setTarget(e.target.value);setConfirmed(false)}}><option value="">{t('选择已加入团队的成员')}</option>{members.filter(m=>m.id!==access?.owner_id).map(m=><option key={m.id} value={m.id}>{m.email} · {m.id}</option>)}</select></label>{target&&<p className="transfer-identity"><strong>{members.find(m=>m.id===target)?.email}</strong><code>{target}</code></p>}</>:<p>{t('退出后将失去团队作品和模型的访问权限。不会删除团队作品；重新加入需要管理员授权。')}</p>}
 {access&&<p>{t('当前团队：{name} · 版本 {version}',{name:access.name,version:access.version})}</p>}
 {!uncertain&&<label><input type="checkbox" checked={confirmed} disabled={busy||!access} onChange={e=>setConfirmed(e.target.checked)}/>{t('我已核对并确认此操作')}</label>}
 <div className="media-actions"><button className="primary" disabled={busy||uncertain||!access||!confirmed||(mode==='rename'&&(!name.trim()||name.trim()===access.name))||(mode==='transfer'&&!target)} onClick={()=>void save()}>{t(busy?'正在保存…':mode==='rename'?'保存团队名称':mode==='transfer'?'确认移交所有权':'确认退出团队')}</button>{uncertain?<button disabled={busy} onClick={()=>{refreshSpaces();void open(mode)}}>{t('重新读取团队状态')}</button>:<button disabled={busy} onClick={()=>{setMode('');setAccess(null);setName(space.name)}}>{t('取消')}</button>}</div></section>}
 {error&&<p role="alert" className="error">{error}</p>}{notice&&<p role="status">{notice}</p>}
 </div>
}
