import {useEffect,useState,type ReactNode} from 'react'
import {Link} from 'react-router-dom'
import {useI18n} from '../../i18n'
import {errorMessage,request} from '../../api'
import {CurrentSeriesEpisode,useSeriesEpisode} from './episodeContext'
import {directoryURL,type EpisodeContext} from './types'
import './series.css'
export function SeriesProjectBoundary({episodeId,space,children}:{episodeId?:string;space:string;children:ReactNode}){
 const {t}=useI18n();const [context,setContext]=useState<EpisodeContext|null>(null),[error,setError]=useState(''),[revision,setRevision]=useState(0)
 useEffect(()=>{
  if(!episodeId)return
  const controller=new AbortController()
  void request<EpisodeContext>(directoryURL(`/episodes/${episodeId}`,space),{signal:controller.signal}).then(result=>{if(!controller.signal.aborted){setContext(result);setError('')}}).catch(reason=>{if(!controller.signal.aborted)setError(errorMessage(reason,t('剧集信息读取失败')))})
  return()=>controller.abort()
  // Language changes must not reset the current project edit.
  // eslint-disable-next-line react-hooks/exhaustive-deps
 },[episodeId,space,revision])
 if(episodeId&&error)return <section className="panel"><Link to="/projects?view=series">{t('返回系列目录')}</Link><div className="error" role="alert">{error}<button onClick={()=>setRevision(v=>v+1)}>{t('重新读取目录')}</button></div></section>
 if(episodeId&&!context)return <p className="loading-block" role="status">{t('正在读取剧集信息…')}</p>
 return <CurrentSeriesEpisode.Provider value={context}>{context&&<div className="series-context"><Link to={`/series/${context.series.id}`}>{context.series.title}</Link>{' / '}{t('第 {p0} 季',{p0:context.season.ordinal??1})}{' · '}{t('第 {p0} 集',{p0:context.episode.ordinal??1})}{' · '}{t('目标 {p0} 秒 / 集',{p0:context.episode.profile.duration_seconds})}</div>}{children}</CurrentSeriesEpisode.Provider>
}
export function SeriesProjectBackLink(){const {t}=useI18n();const c=useSeriesEpisode();return <Link className="project-back" to={c?`/series/${c.series.id}`:'/projects'}>{t(c?'返回系列目录':'返回项目列表')}</Link>}
