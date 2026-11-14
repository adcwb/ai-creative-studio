import {useEffect,useRef,useState} from 'react'
import {Link} from 'react-router-dom'
import {request,errorMessage} from '../../api'
import {useI18n} from '../../i18n'

interface Impact {project_id:string;title:string;episode_id?:string;episode_number?:number;series_id?:string;season_id?:string;media_version:number;binding_count:number;media_count:number;pinned_version_ids:string[];outdated:boolean}
interface View {head_version_id:string;asset_status:string;authorization_status:string;authorization_revision:number;items:Impact[];next_cursor:string}
export function LibraryImpact({space,asset}:{space:string;asset:string}){
 const {t}=useI18n(),[view,setView]=useState<View|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(''),flight=useRef(false),live=useRef(true)
 useEffect(()=>{live.current=true;return()=>{live.current=false}},[])
 async function load(more=false){
  if(flight.current)return;flight.current=true;setBusy(true);setError('')
  const old=view
  try{
   const q=new URLSearchParams({space_id:space,limit:'20',...(more&&old?.next_cursor?{cursor:old.next_cursor}:{})})
   const next=await request<View>(`/library/assets/${asset}/impact?${q}`)
   if(!live.current)return
   if(more&&old&&(old.head_version_id!==next.head_version_id||old.authorization_revision!==next.authorization_revision)){setError(t('资产或授权已更新，请重新核对影响范围。'));return}
   setView({...next,items:more&&old?[...old.items.filter(v=>!next.items.some(n=>n.project_id===v.project_id)),...next.items]:next.items})
  }catch(e){if(live.current)setError(errorMessage(e,t('影响范围读取失败，请重试。')))}finally{flight.current=false;if(live.current)setBusy(false)}
 }
 return <section className="library-impact" aria-label={t('资产影响范围')}><details><summary>{t('资产引用与影响')}</summary><p className="drama-help">{t('按已记录的固定绑定和素材来源核对，不会自动替换版本或重新生成。旧素材缺少来源时不会猜测关联。')}</p><button type="button" disabled={busy} onClick={()=>void load()}>{t(busy?'正在核对影响范围…':view?'重新核对影响范围':'查看影响范围')}</button>{error&&<p role="alert" className="error">{error}</p>}{view&&<>
  {(view.asset_status==='archived'||(view.authorization_status==='revoked'||view.authorization_status==='expired'))&&<p className="editing-note">{t('资产已归档，或使用声明已撤销、已过期；已有作品保留，新的使用仍须通过当前授权检查。')}</p>}
  {!view.items.length&&<p role="status">{t('当前空间尚未记录这份资产的作品引用。')}</p>}
  <ul className="drama-history">{view.items.map(v=><li key={v.project_id}><strong>{v.title}</strong>{v.episode_number?` · ${t('第 {number} 集',{number:v.episode_number})}`:''}<p>{t('固定绑定 {bindings} 处，记录来源素材 {media} 份',{bindings:v.binding_count,media:v.media_count})}</p><p>{t(v.outdated?'部分引用仍固定旧版本，请按作品需要决定是否重做。':'已记录引用与当前资产版本一致。')}</p><div className="media-actions"><Link to={`/projects/${v.project_id}/references`}>{t('核对作品引用')}</Link><Link to={`/projects/${v.project_id}/media`}>{t('查看素材与剪辑')}</Link>{v.series_id&&<Link to={`/series/${v.series_id}`}>{t('返回系列')}</Link>}</div></li>)}</ul>
  {view.next_cursor&&<button type="button" disabled={busy} onClick={()=>void load(true)}>{t('查看更多引用作品')}</button>}
 </>}</details></section>
}
