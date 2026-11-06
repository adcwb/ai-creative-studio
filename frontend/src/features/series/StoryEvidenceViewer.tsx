import {useEffect,useRef,useState} from 'react'
import {request,errorMessage} from '../../api'
import {useI18n} from '../../i18n'
import {directoryURL} from './types'
import type {StoryBody,StoryEvidenceScript} from './storyTypes'

// Read-only jump to the exact historical scene, never the latest similarly
// named scene. Viewing evidence neither restores a draft nor calls a model.
export function StoryEvidenceViewer({root,space,value}:{root:string;space:string;value:StoryBody['evidence']}){
 const {t}=useI18n(),[source,setSource]=useState<StoryEvidenceScript|null>(null),[sceneID,setSceneID]=useState(''),[busy,setBusy]=useState(false),[error,setError]=useState('')
 const abort=useRef<AbortController|null>(null);useEffect(()=>()=>abort.current?.abort(),[])
 async function read(evidence:StoryBody['evidence'][number]){
  abort.current?.abort();const next=new AbortController();abort.current=next;setBusy(true);setError('')
  try{const v=await request<StoryEvidenceScript>(directoryURL(`/series/${root}/story-evidence/episodes/${evidence.episode_id}/scripts/${evidence.script_version}`,space),{signal:next.signal});if(v.script_sha256!==evidence.script_sha256||!v.script.scenes.some(v=>v.id===evidence.scene_id))throw new Error(t('固定来源不匹配，未改为显示当前稿。'));if(!next.signal.aborted){setSource(v);setSceneID(evidence.scene_id)}}catch(e){if(!next.signal.aborted)setError(errorMessage(e,t('来源剧本读取失败，请保留草稿并重试。')))}finally{if(!next.signal.aborted)setBusy(false)}
 }
 if(!value.length)return null
 const scene=source?.script.scenes.find(s=>s.id===sceneID)
 return <section className="story-history" aria-label={t('节拍来源原文')}><h4>{t('节拍来源原文')}</h4>{value.map((v,i)=><article key={i}><blockquote>{v.quote}</blockquote><p>{t('来源剧本版本 {p0}',{p0:v.script_version})}</p><button type="button" disabled={busy} onClick={()=>void read(v)}>{t('查看固定场次原文')}</button></article>)}{error&&<p className="error" role="alert">{error}</p>}{source&&scene&&<article className="editing-note"><h4>{source.script.title} · {t('来源剧本版本 {p0}',{p0:source.script_version})}</h4><h5>{scene.heading}</h5><p>{scene.action}</p>{scene.dialogue.map((v,i)=><p key={i}>{v.character}：{v.text}</p>)}<p>{t('旧版本只供引用；这里的操作不会恢复或修改剧本。')}</p></article>}</section>
}
