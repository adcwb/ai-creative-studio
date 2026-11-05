import {useState} from 'react'
import {Link} from 'react-router-dom'
import {useI18n} from '../../i18n'
import {useAppWorkspace} from '../../app/workspace'
import {Icon} from '../../components/Icon'
import {useDirectory} from './useDirectory'
import './series.css'
export function SeriesList(){
 const {t}=useI18n();const [status,setStatus]=useState('active')
 return <><div className="project-list-heading"><label>{t('查看系列')}<select value={status} onChange={e=>setStatus(e.target.value)}><option value="active">{t('创作中')}</option><option value="archived">{t('已归档')}</option></select></label><span>{t('按系列、季和集组织长篇故事')}</span></div><SeriesCards key={status} status={status}/></>
}
function SeriesCards({status}:{status:string}){
 const {t,formatDate}=useI18n();const {space}=useAppWorkspace();const [revision,setRevision]=useState(0)
 const list=useDirectory(`/series?status=${status}`,space!.id,revision)
 return <>{list.loading&&<p className="loading-block" role="status">{t('正在读取系列目录…')}</p>}{list.error&&<div className="error" role="alert">{list.error}<button onClick={()=>setRevision(v=>v+1)}>{t('重新读取目录')}</button></div>}
 {!list.loading&&!list.error&&!list.items.length&&<section className="panel page-empty"><Icon name="clapper"/><h2>{t(status==='archived'?'还没有归档系列':'从一个系列开始')}</h2><p>{t('先记录故事方向，再逐季、逐集完善剧本与镜头。')}</p>{status==='active'&&space?.role!=='viewer'&&<Link className="button-link" to="/series/new">{t('新建系列')}</Link>}</section>}
 <div className="project-grid">{list.items.map(n=><Link className="project-card" key={n.id} to={`/series/${n.id}`}><div className="project-card-icon"><Icon name="clapper"/><span>{t('系列')}</span></div><h2>{n.title}</h2><p>{n.summary}</p><div className="project-card-footer"><span>{n.profile.output_resolution} · {n.profile.aspect_ratio}</span><time>{formatDate(n.updated_at,{dateStyle:'medium'})}</time></div></Link>)}</div>
 {list.next_cursor&&<button disabled={list.more||list.loading} onClick={()=>void list.loadMore()}>{list.more?t('正在读取…'):t('加载更多系列')}</button>}</>
}
