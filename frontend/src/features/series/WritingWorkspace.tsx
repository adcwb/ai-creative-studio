import {StoryEvidenceViewer} from './StoryEvidenceViewer'
import {OutlineCandidates} from './OutlineCandidates'
import {useCallback,useEffect,useState} from 'react'
import {Link} from 'react-router-dom'
import {request,errorMessage} from '../../api'
import {useI18n} from '../../i18n'
import {useAppWorkspace} from '../../app/workspace'
import {useDirectory} from './useDirectory'
import {StoryEditor} from './StoryWorkspace'
import {StoryBodyView} from './StoryBodyView'
import {directoryURL,type SeriesEntry} from './types'
import type {StoryVersion} from './storyTypes'
import {readSeriesRecovery} from './useSeriesWrite'

export function WritingWorkspace({root,season}:{root:string;season:SeriesEntry}){
 const {t}=useI18n();const [open,setOpen]=useState(false)
 return <details className="panel project-tools story-workspace" onToggle={e=>{if(e.currentTarget.open)setOpen(true)}}><summary>{t('编剧工作台 · 季与集的节拍')}</summary>{open&&<WritingBoard key={season.id} root={root} season={season}/>}</details>
}
function WritingBoard({root,season}:{root:string;season:SeriesEntry}){
 const {t}=useI18n(),{space,requestLeave}=useAppWorkspace()
 const episodes=useDirectory<SeriesEntry>(`/seasons/${season.id}/episodes?limit=100`,space!.id,0)
 const [episode,setEpisode]=useState('')
 const selected=episodes.items.find(x=>x.id===episode)
 return <section className="series-form"><h2>{t('先定节奏，再写这一集')}</h2><p>{t('本季计划决定人物如何变化；本集计划明确开场、阻碍、选择和结尾钩子。保存只追加正式资料，不生成内容，也不会把计划当成已发生的事实。')}</p>
 <label>{t('规划范围')}<select aria-label={t('规划范围')} value={episode} onChange={e=>requestLeave(()=>setEpisode(e.target.value))}><option value="">{t('本季主线与节拍')}</option>{episodes.items.map(n=><option key={n.id} value={n.id}>{t('第 {p0} 集',{p0:n.ordinal??1})} · {n.title}</option>)}</select></label>
 {episodes.error&&<p className="error" role="alert">{episodes.error}</p>}{episodes.loading&&<p role="status">{t('正在读取剧集…')}</p>}{episodes.next_cursor&&<button type="button" disabled={episodes.more} onClick={()=>void episodes.loadMore()}>{t('加载更多剧集')}</button>}
 {selected&&<p><Link to={`/projects/${selected.project_id}/script`}>{t('进入这一集写剧本')}</Link> · {t('目标 {p0} 秒 / 集',{p0:selected.profile.duration_seconds})}</p>}
 <BeatScope key={episode} root={root} season={season} episode={selected} known={episodes.items}/>
 </section>
}
function BeatScope({root,season,episode,known}:{root:string;season:SeriesEntry;episode?:SeriesEntry;known:SeriesEntry[]}){
 const {t}=useI18n(),{space,session,requestLeave}=useAppWorkspace(),[current,setCurrent]=useState<StoryVersion|null>(null),[loading,setLoading]=useState(true),[error,setError]=useState(''),[revision,setRevision]=useState(0)
 const identity=`writing-beats:${season.id}:${episode?.id??'season'}`
 const [forceNew,setForceNew]=useState(()=>Boolean(readSeriesRecovery(session.user?.id??'demo-user',space!.id,identity,`/series/${root}/story-records`,'POST')))
 const [editing,setEditing]=useState(forceNew)
 useEffect(()=>{const abort=new AbortController();void request<{current?:StoryVersion}>(directoryURL(`/seasons/${season.id}/writing-beats${episode?'?episode_id='+episode.id:''}`,space!.id),{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setCurrent(v.current??null);setLoading(false);if(v.current&&readSeriesRecovery(session.user?.id??'demo-user',space!.id,identity,`/series/${root}/story-records/${v.current.id}`,'PUT'))setEditing(true);setError('')}}).catch(e=>{if(!abort.signal.aborted){setError(errorMessage(e,t('节拍读取失败')));setLoading(false)}});return()=>abort.abort()
 // Keep local writing when the UI language changes.
 // eslint-disable-next-line react-hooks/exhaustive-deps
 },[space?.id,season.id,episode?.id,revision])
 const saved=useCallback(()=>{setEditing(false);setForceNew(false);setRevision(v=>v+1)},[])
 const episodes=known.map(n=>({id:n.id,season_id:season.id,season_ordinal:season.ordinal??1,ordinal:n.ordinal??1,title:n.title}))
 return <section aria-label={t('正式节拍计划')}><h3>{episode?episode.title:t('本季主线与节拍')}</h3>{error&&<p className="error" role="alert">{error}<button type="button" onClick={()=>setRevision(v=>v+1)}>{t('重新读取资料')}</button></p>}{loading&&<p role="status">{t('正在读取故事资料…')}</p>}
 {!loading&&!error&&editing?<StoryEditor key={current?.id??'new'} root={root} kind="outline" item={forceNew?undefined:current??undefined} identity={identity} space={space!.id} characters={[]} episodes={episodes} outlineScope={{season_id:season.id,...(episode?{episode_id:episode.id}:{})}} initialTitle={`${episode?.title??season.title??t('本季主线与节拍')} · ${t('季集节拍')}`} onSaved={saved} onClose={()=>requestLeave(()=>setEditing(false))}/>:<>{current?<><p>{t('版本 {p0}',{p0:current.number})} · {current.status==='active'?t('正式资料'):t('已归档')}</p><StoryBodyView body={current.body}/><StoryEvidenceViewer root={root} space={space!.id} value={current.body.evidence}/></>:!loading&&!error&&<p className="page-empty">{t('还没有保存节拍计划。可以先写最重要的人物目标，其他节奏逐步补充。')}</p>}{!loading&&!error&&space?.role!=='viewer'&&<button type="button" onClick={()=>setEditing(true)}>{t(current?'编辑节拍与查看历史':'写下节拍计划')}</button>}</>}
 <OutlineCandidates season={season.id} episode={episode?.id} onAdopted={saved}/>
 </section>
}
