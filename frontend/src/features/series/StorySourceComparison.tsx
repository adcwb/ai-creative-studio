import {useEffect,useRef,useState} from 'react'
import {request,errorMessage} from '../../api'
import {Link} from 'react-router-dom'
import {useI18n} from '../../i18n'
import {directoryURL} from './types'
import {StoryBodyView} from './StoryBodyView'
import type {StoryFindingReview,StorySummary,StoryVersion} from './storyTypes'
export function StorySourceComparison({view,space}:{view:StoryFindingReview;space:string}){
 const{t}=useI18n()
 return <section aria-label={t('对照检查后的来源变更')}>
 {view.project_changed&&!view.script_changed&&<p>{t('本集项目资料已更新，请核对当前创作要求。')} <Link to={`/projects/${view.snapshot.episode_id}/script`} target="_blank" rel="noopener noreferrer">{t('在新窗口查看当前项目')}</Link></p>}
 {view.directory_changed&&<p className="editing-note">{t('剧集目录或顺序已变化，请重新确认本集的叙事位置。')}</p>}
 {view.script_changed&&<details><summary>{t('对照检查时与本次核对的剧本')}</summary><Script label={t('检查时剧本版本 {p0}',{p0:view.snapshot.script_version})} script={view.snapshot.script}/><Script label={t('本次核对的剧本版本 {p0}',{p0:view.current_script_version})} script={view.current_script}/></details>}
 {view.changed_sources.map(change=><SourceChange key={(change.current?.id??change.previous!.id)+':'+(change.current?.version_id??'missing')} change={change} root={view.snapshot.series_id} space={space}/>)}
 </section>
}
function Script({label,script}:{label:string;script:StoryFindingReview['current_script']}){const{t}=useI18n();return <section><h4>{label}</h4>{script?<><p>{script.title}</p>{script.scenes.map(s=><section key={s.id}><h4>{s.heading}</h4><p>{s.action}</p>{s.dialogue.map((d,i)=><p key={i}>{d.character}：{d.text}</p>)}</section>)}</>:<p>{t('尚未保存剧本')}</p>}</section>}
function SourceChange({change,root,space}:{change:{previous?:StorySummary;current?:StorySummary};root:string;space:string}){
 const{t}=useI18n();const[data,setData]=useState<{previous?:StoryVersion;current?:StoryVersion}|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false);const abort=useRef<AbortController|null>(null);useEffect(()=>()=>abort.current?.abort(),[])
 async function read(){abort.current?.abort();const next=new AbortController();abort.current=next;setBusy(true);setError('');try{const get=(s?:StorySummary)=>s?request<StoryVersion>(directoryURL(`/series/${root}/story-records/${s.id}/versions/${s.version_id}`,space),{signal:next.signal}):Promise.resolve(undefined);const[previous,current]=await Promise.all([get(change.previous),get(change.current)]);if(!next.signal.aborted)setData({previous,current})}catch(e){if(!next.signal.aborted)setError(errorMessage(e,t('变更资料读取失败，请保留草稿并重试。')))}finally{if(!next.signal.aborted)setBusy(false)}}
 return <details><summary>{change.current?.title??change.previous?.title} · {t('资料来源已变化')}</summary><p>{change.previous?t('检查时版本 {p0}',{p0:change.previous.number}):t('检查后新增的资料')} · {change.current?t('本次核对版本 {p0}',{p0:change.current.number}):t('当前资料已不可用')}</p><button type="button" disabled={busy} onClick={()=>void read()}>{busy?t('正在读取故事资料…'):t('对照这两个资料版本')}</button>{error&&<p className="error" role="alert">{error}</p>}{data&&<>{data.previous&&<section><h4>{t('检查时的资料')}</h4><StoryBodyView body={data.previous.body}/></section>}{data.current&&<section><h4>{t('本次核对的资料')}</h4><StoryBodyView body={data.current.body}/></section>}</>}</details>
}
