import {useState} from 'react'
import {Link} from 'react-router-dom'
import {useAppWorkspace} from '../app/workspace'
import {errorMessage, request} from '../api'
import {useI18n} from '../i18n'
export function SpacesPanel() {
  const {spaces,space,refreshSpaces,switchSpace,setDirty} = useAppWorkspace()
  const {t}=useI18n()
  const [name,setName]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState(''),[notice,setNotice]=useState('')
  async function create() {
    if(busy||!name.trim())return
    setBusy(true);setError('');setNotice('')
    try {await request('/organizations',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:name.trim()})});setName('');setDirty(false);refreshSpaces();setNotice(t('团队空间已创建，可在下方选择进入。'))}catch(reason){setError(errorMessage(reason,t('空间创建失败')))}finally{setBusy(false)}
  }
  return <><section className="panel space-create"><h2>{t('一起创作，从一个团队开始')}</h2><p>{t('个人空间仅自己可见；团队内容按成员角色共享。切换空间不会移动已有作品。')}</p><form onSubmit={event=>{event.preventDefault();void create()}}><label>{t('团队名称')}<input required maxLength={80} value={name} disabled={busy} onChange={e=>{setName(e.target.value);setDirty(Boolean(e.target.value))}} /></label><button className="primary" disabled={busy||!name.trim()}>{t(busy?'正在创建…':'创建团队空间')}</button></form>{error&&<p role="alert" className="error">{error}</p>}{notice&&<p role="status" className="notice">{notice}</p>}</section><div className="space-grid">{spaces.map(item=><section className="panel space-card" key={item.id}><span className="eyebrow">{t(item.type==='personal'?'个人空间':'团队空间')}</span><h2>{item.name}</h2><p>{t(item.type==='personal'?'专属于你的创作环境':'与成员协作，共享这个空间的作品与模型配置')}</p>{space?.id===item.id?<><span className="notice">{t('当前空间')}</span><Link to="/settings/organization">{t('查看成员与权限')}</Link></>:<button onClick={()=>switchSpace(item.id)}>{t('进入空间')}</button>}</section>)}</div></>
}
