import {useCallback,useEffect,useState} from 'react'
import {request,errorMessage,type Space} from '../api'
import {hasPermission} from '../permissions'
import {useAppWorkspace} from '../app/workspace'
import {useI18n} from '../i18n'
import {useSeriesWrite} from '../features/series/useSeriesWrite'

export interface CustomRole {id:string;name:string;base_role:string;version:number;permissions:string[]}
interface RolesView {id:string;version:number;items:CustomRole[];permission_options:Record<string,string[]>}
const permissionNames:Record<string,string>={'workspace.read':'查看作品','workspace.write':'编辑作品','workspace.manage':'管理空间','members.read':'查看成员','members.manage':'管理普通成员','models.manage':'管理模型','models.execute':'执行模型','usage.read':'查看使用记录'}
export function CustomRolesPanel({space,revision,onChanged}:{space:Space;revision:number;onChanged:()=>void}){
 const {t}=useI18n(),{requestLeave}=useAppWorkspace(),[view,setView]=useState<RolesView|null>(null),[error,setError]=useState(''),[reload,setReload]=useState(0),[editing,setEditing]=useState<CustomRole|'new'|null>(null)
 useEffect(()=>{const c=new AbortController();void request<RolesView>(`/organizations/${space.id}/roles`,{signal:c.signal}).then(v=>{if(!c.signal.aborted){setView(v);setError('')}}).catch(e=>{if(!c.signal.aborted)setError(errorMessage(e,t('角色读取失败')))});return()=>c.abort()},[space.id,revision,reload,t])
 const saved=useCallback(()=>{setEditing(null);setReload(v=>v+1);onChanged()},[onChanged])
 const manage=hasPermission(space,'members.manage_admin')
 return <section className="custom-role-panel"><div className="section-heading"><div><h3>{t('团队自定义角色')}</h3><p>{t('按协作职责选择实际权限。权限仅属于本团队，所有者专属能力不能授予自定义角色。')}</p></div>{manage&&view&&<button type="button" onClick={()=>requestLeave(()=>setEditing('new'))}>{t('添加权限角色')}</button>}</div>
 {error&&<p role="alert" className="error">{error}<button type="button" onClick={()=>setReload(v=>v+1)}>{t('重新读取')}</button></p>}
 {!view&&!error&&<p role="status">{t('正在读取角色…')}</p>}
 {view&&<><ul className="member-list">{view.items.map(role=><li key={role.id}><div><strong>{role.name}</strong><small>{role.permissions.map(p=>t(permissionNames[p]??p)).join(' · ')} · {t('版本 {p0}',{p0:role.version})}</small></div>{manage&&<button type="button" onClick={()=>requestLeave(()=>setEditing(role))}>{t('编辑权限角色')}</button>}</li>)}</ul>{!view.items.length&&<p className="drama-help">{t('还没有自定义角色，可继续使用管理员、编辑者和只读成员。')}</p>}{editing&&manage&&<RoleEditor key={editing==='new'?'new':editing.id+':'+editing.version} space={space} view={view} item={editing==='new'?undefined:editing} onSaved={saved} onClose={()=>requestLeave(()=>setEditing(null))}/>}</>}
 </section>
}
function RoleEditor({space,view,item,onSaved,onClose}:{space:Space;view:RolesView;item?:CustomRole;onSaved:()=>void;onClose:()=>void}){
 const {requestLeave}=useAppWorkspace(),[method,setMethod]=useState<'POST'|'PUT'|'DELETE'>(item?'PUT':'POST')
 return <RoleForm key={method} space={space} view={view} item={item} method={method} onSaved={onSaved} onClose={onClose} onDelete={()=>requestLeave(()=>setMethod('DELETE'))}/>
}
function RoleForm({space,view,item,method,onSaved,onClose,onDelete}:{space:Space;view:RolesView;item?:CustomRole;method:'POST'|'PUT'|'DELETE';onSaved:()=>void;onClose:()=>void;onDelete:()=>void}){
 const {t}=useI18n()
 const saved=useCallback(()=>onSaved(),[onSaved])
 const write=useSeriesWrite<RolesView>(`custom-role:${space.id}:${item?.id??'new'}:${method}`,`/organizations/${space.id}/roles`,method,saved)
 const seed=write.recovery,[id]=useState(()=>String(seed?.id??item?.id??crypto.randomUUID().replaceAll('-',''))),[name,setName]=useState(()=>String(seed?.name??item?.name??'')),[base,setBase]=useState(()=>String(seed?.base_role??item?.base_role??'editor')),[grants,setGrants]=useState<string[]>(()=>seed?.permissions as string[]??item?.permissions??view.permission_options.editor),[confirmed,setConfirmed]=useState(false)
 const locked=write.busy||write.uncertain
 const changeGrant=(p:string,checked:boolean)=>{let next=checked?[...grants,p]:grants.filter(v=>v!==p);if(!checked&&p==='workspace.write')next=next.filter(v=>!['models.execute','models.manage','members.manage','workspace.manage'].includes(v));if(checked&&['models.execute','models.manage','members.manage','workspace.manage'].includes(p))next=[...new Set([...next,'workspace.write',...(p==='members.manage'?['members.read']:[])])];setGrants(next);write.markDirty();setConfirmed(false)}
 return <form className="custom-role-form series-form" onSubmit={e=>{e.preventDefault();void write.save({id,name,base_role:base,permissions:grants,expected_version:view.version,expected_role_version:item?.version??0})}}><h4>{t(method==='DELETE'?'删除权限角色':item?'编辑权限角色':'添加权限角色')}</h4><fieldset disabled={locked}>
 {method==='DELETE'?<p>{t('删除后不能再分配此角色。仍有成员使用时会拒绝删除，请先调整成员权限。')}</p>:<><label>{t('角色名称')}<input required maxLength={80} value={name} onChange={e=>{setName(e.target.value);write.markDirty();setConfirmed(false)}}/></label><label>{t('权限范围')}<select value={base} disabled={!!item} onChange={e=>{setBase(e.target.value);setGrants(view.permission_options[e.target.value]);write.markDirty();setConfirmed(false)}}><option value="editor">{t('创作协作')}</option><option value="admin">{t('团队管理')}</option><option value="viewer">{t('只读协作')}</option></select></label><p className="drama-help">{t('创建后范围保持固定，可继续减少或调整范围内的具体权限。模型执行需要作品编辑权限。')}</p><div className="custom-role-grants">{(view.permission_options[base]??[]).map(p=><label key={p}><input type="checkbox" checked={grants.includes(p)} disabled={p==='workspace.read'} onChange={e=>changeGrant(p,e.target.checked)}/>{t(permissionNames[p]??p)}</label>)}</div></>}
 <label className="custom-role-confirm"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/>{t('我已核对权限变化及受影响成员')}</label></fieldset>
 {write.error&&<p role="alert" className="error">{write.error}</p>}{write.storageNotice&&<p role="status">{write.storageNotice}</p>}{write.uncertain&&<p className="editing-note">{t('角色保存结果未确认，请使用原请求核对；不会重复创建角色或覆盖后来的角色修改。')}</p>}
 <div className="media-actions"><button type="submit" className="primary" disabled={write.busy||(!write.uncertain&&(!confirmed||(method!=='DELETE'&&!name.trim())))}>{t(write.uncertain?'使用原请求核对结果':method==='DELETE'?'确认删除角色':'保存权限角色')}</button><button type="button" disabled={locked} onClick={onClose}>{t('取消')}</button>{item&&method==='PUT'&&<button type="button" disabled={locked} onClick={onDelete}>{t('删除权限角色')}</button>}</div></form>
}
