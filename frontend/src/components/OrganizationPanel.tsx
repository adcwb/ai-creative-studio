import {translate as t,useI18n} from '../i18n'
import {useEffect,useState} from 'react'
import {Link} from 'react-router-dom'
import {errorMessage,request,type Space} from '../api'
import {hasPermission} from '../permissions'
interface Member {id:string;email:string;role:string}
export function OrganizationPanel({space,onChange,userID,onDirtyChange}:{space?:Space;onChange:()=>void;userID?:string;onDirtyChange?:(dirty:boolean)=>void}) {
  useI18n()
  const labels:Record<string,string>={owner:t('所有者'),admin:t('管理员'),editor:t('编辑者'),viewer:t('只读成员')}
  const [members,setMembers]=useState<Member[]>([]),[access,setAccess]=useState<Space|null>(null),[name,setName]=useState(''),[email,setEmail]=useState(''),[memberID,setMemberID]=useState(''),[role,setRole]=useState('editor'),[error,setError]=useState(''),[notice,setNotice]=useState(''),[busy,setBusy]=useState(false),[revision,setRevision]=useState(0),[loaded,setLoaded]=useState(false)
  const current=access??space
  const canManage=current?.type!=='personal'&&hasPermission(current,'members.manage')
  const dirty=busy||Boolean(name||email||memberID)
  useEffect(()=>{onDirtyChange?.(dirty);return()=>onDirtyChange?.(false)},[dirty,onDirtyChange])
  useEffect(()=>{
    if(!space)return
    const c=new AbortController(),path=`/organizations/${encodeURIComponent(space.id)}`
    void Promise.all([request<Member[]>(path+'/members',{signal:c.signal}),request<Space>(path+'/access',{signal:c.signal})]).then(([list,info])=>{if(!c.signal.aborted){setMembers(list);setAccess(info);setLoaded(true);setError('')}}).catch(reason=>{if(!c.signal.aborted){setLoaded(false);setError(errorMessage(reason,t('成员读取失败')))}})
    return()=>c.abort()
  },[space,revision])
  async function mutate(path:string,data:unknown,method='PUT') {
    if(busy)return
    setBusy(true);setError('');setNotice('')
    try{await request(path,{method,headers:{'Content-Type':'application/json'},body:JSON.stringify(data)});setRevision(x=>x+1);onChange();setNotice(t('组织变更已保存。'));setName('');setEmail('');setMemberID('')}catch(reason){setError(errorMessage(reason,t('组织变更失败')));setRevision(x=>x+1)}finally{setBusy(false)}
  }
  return <section className="panel organization-panel"><div className="section-heading"><div><span className="eyebrow">{t(current?.type==='personal'?'个人空间':'团队空间')}</span><h2>{space?.name??t('创建第一个组织')}</h2><p>{current?.role&&labels[current.role]} · {t('权限仅在当前空间生效')}</p></div><Link className="button-link" to="/settings/spaces">{t('管理团队空间')}</Link></div>{userID&&<p className="account-id">{t('我的账号 ID：')}<code>{userID}</code></p>}{error&&<p role="alert" className="error">{error} <button disabled={busy} onClick={()=>setRevision(x=>x+1)}>{t('重新读取')}</button></p>}{notice&&<p role="status" className="notice">{notice}</p>}
    {current?.type==='personal'&&<p className="notice">{t('个人空间仅你自己可访问。需要共同创作时，请创建团队空间。')}</p>}
    <details className="role-guide"><summary>{t('了解成员权限')}</summary><dl><dt>{t('所有者')}</dt><dd>{t('管理空间、模型与所有成员；所有者身份不可在此移除。')}</dd><dt>{t('管理员')}</dt><dd>{t('管理模型与普通成员，不能授予或修改管理员身份。')}</dd><dt>{t('编辑者')}</dt><dd>{t('使用模型创作和编辑内容，不能管理模型或成员。')}</dd><dt>{t('只读成员')}</dt><dd>{t('查看空间内容与成员，不能生成或修改。')}</dd></dl></details>
    <fieldset disabled={busy}><form onSubmit={e=>{e.preventDefault();void mutate('/organizations',{name:name.trim()},'POST')}}><label>{t('新组织名称')}<input style={{maxWidth:560}} value={name} maxLength={80} required onChange={e=>setName(e.target.value)} /></label><button disabled={!name.trim()} type="submit">{t('创建组织')}</button></form>{space&&<><h3>{t('当前组织成员')}</h3>{!loaded&&!error&&<p role="status">{t('正在读取成员…')}</p>}<ul className="member-list">{members.map(member=><li key={member.id}><span className="member-avatar" aria-hidden="true">{member.email.slice(0,1).toUpperCase()}</span><div><strong>{member.email}</strong><small>{labels[member.role]??member.role}</small></div>{canManage&&member.role!=='owner'&&(hasPermission(current,'members.manage_admin')||member.role!=='admin')&&<button type="button" onClick={()=>{setEmail(member.email);setMemberID(member.id);setRole(member.role)}}>{t('调整权限')}</button>}</li>)}</ul>{canManage&&<form onSubmit={e=>{e.preventDefault();void mutate(`/organizations/${encodeURIComponent(space.id)}/members`,{email:email.trim(),role,user_id:memberID.trim()||members.find(m=>m.email.toLowerCase()===email.trim().toLowerCase())?.id,expected_version:access?.version})}}><h3>{t('添加成员或调整权限')}</h3><p className="drama-help">{t('通过已注册邮箱添加成员；SSO 成员需提供稳定账号 ID。移除后立即失去此空间访问权限。')}</p><div className="storyboard-fields"><label>{t('成员邮箱')}<input type="email" required value={email} onChange={e=>setEmail(e.target.value)} /></label><label>{t('成员账号 ID（SSO必填）')}<input value={memberID} maxLength={64} onChange={e=>setMemberID(e.target.value)} /></label><label>{t('成员权限')}<select value={role} onChange={e=>setRole(e.target.value)}>{hasPermission(current,'members.manage_admin')&&<option value="admin">{t('管理员')}</option>}<option value="editor">{t('编辑者')}</option><option value="viewer">{t('只读成员')}</option><option value="remove">{t('移除成员')}</option></select></label></div>{role==='remove'&&<p className="notice">{t('保存后该成员将无法访问此团队，已有作品仍归团队所有。')}</p>}<button className="primary" disabled={!loaded} type="submit">{t(busy?'正在保存…':'保存成员权限')}</button></form>}</>}</fieldset></section>
}
