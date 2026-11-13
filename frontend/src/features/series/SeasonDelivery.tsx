import {useEffect,useRef,useState} from 'react'
import {Link} from 'react-router-dom'
import {baseURL,errorMessage,request} from '../../api'
import {useI18n} from '../../i18n'
import {directoryURL} from './types'

interface Episode {episode_id:string;project_id:string;episode_number:number;title:string;export_id?:string;duration_seconds:number;output_resolution?:string;issue?:string}
interface Preview {season_id:string;series_id:string;title:string;episodes:Episode[];ready:boolean;selection_sha256:string}

export function SeasonDelivery({space,season}:{space:string;season:string}){
 const {t}=useI18n(),[preview,setPreview]=useState<Preview|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(''),[confirmed,setConfirmed]=useState(false),flight=useRef(false),live=useRef(true)
 useEffect(()=>{live.current=true;return()=>{live.current=false}},[])
 async function load(){
  if(flight.current)return;flight.current=true;setBusy(true);setError('');setConfirmed(false);setPreview(null)
  try{const next=await request<Preview>(directoryURL(`/seasons/${season}/delivery`,space),{timeoutMs:60000});if(live.current)setPreview(next)}catch(e){if(live.current)setError(errorMessage(e,t('交付清单读取失败，请重试。')))}finally{flight.current=false;if(live.current)setBusy(false)}
 }
 return <details className="panel project-tools story-workspace season-delivery"><summary>{t('本季交付 · 正式成片与字幕')}</summary>
  <p>{t('检查本季全部剧集的最新正式成片，确认后下载同一个交付包。每集附带 SRT、VTT、固定时间轴及文件校验值，不会自动发布到外部平台。')}</p>
  <button type="button" disabled={busy} onClick={()=>void load()}>{t(busy?'正在检查成片…':preview?'重新检查本季交付':'检查本季交付')}</button>
  {error&&<p role="alert" className="error">{error}</p>}
  {preview&&<>
   {!preview.episodes.length&&<p className="page-empty">{t('本季还没有剧集，先添加并完成第一集。')}</p>}
   <ul className="drama-history">{preview.episodes.map(item=><li key={item.episode_id}><strong>{t('第 {number} 集',{number:item.episode_number})} · {item.title}</strong><p>{item.issue?t(item.issue==='missing_final_export'?'尚无正式成片，请进入审片与成片完成导出。':item.issue==='cover_file_unavailable'?'封面文件不可读取或已变化，请重新选择并保存发布资料。':'成片文件暂时不可读取，请核对存储后重试。'):t('固定成片 {id} · {seconds} 秒 · {resolution}',{id:item.export_id?.slice(0,8)??'',seconds:item.duration_seconds,resolution:item.output_resolution??'—'})}</p><Link to={`/projects/${item.project_id}/media`}>{t('审片与成片')}</Link></li>)}</ul>
   {!!preview.episodes.length&&!preview.ready&&<p role="status" className="editing-note">{t('整季交付需要每一集都有可读取的正式成片；不会跳过未完成的剧集。')}</p>}
   {preview.ready&&<><p className="drama-help">{t('交付使用清单中固定的历史成片，不等于当前剪辑、授权或发布已审核通过。下载期间请保留页面；若目录或成片选择变化，请重新检查清单。')}</p><label className="checkbox-row"><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/>{t('我已核对本季清单及素材发布授权')}</label>{confirmed&&<a className="button-link" href={`${baseURL}/api/v1${directoryURL(`/seasons/${season}/delivery.zip`,space)}&selection_sha256=${preview.selection_sha256}`} download>{t('下载整季交付包')}</a>}</>}
  </>}
 </details>
}
