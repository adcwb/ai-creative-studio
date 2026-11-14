import {useEffect,useRef,useState} from 'react'
import {errorMessage,request} from '../api'
import {useI18n} from '../i18n'
import {avatarSplitInput,avatarCutLabel,type AvatarSplitForm,type AvatarSegmentProgress} from './avatarCuts'
interface Preview {id:string;duration_seconds:number;segments:AvatarSegmentProgress[]}
export function AvatarSplitControls({project,space,audio,value,change,disabled}:{project:string;space:string;audio:string;value:AvatarSplitForm;change:(v:AvatarSplitForm)=>void;disabled:boolean}){
 const {t}=useI18n(),[preview,setPreview]=useState<Preview|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(''),alive=useRef(true)
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[])
 const fields=avatarSplitInput(value)
 function edit(next:AvatarSplitForm){change({...next,approved:undefined});setPreview(null);setError('')}
 async function check(){
  if(!fields.valid||!audio||busy)return
  setBusy(true);setError('')
  try{
   const result=await request<Preview>(`/projects/${encodeURIComponent(project)}/avatar-cut-preview?${new URLSearchParams({space_id:space})}`,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({audio_asset_id:audio,avatar_split_mode:value.mode,...(value.mode==='manual'?{avatar_cut_points:fields.input.avatar_cut_points}:{})})})
   if(alive.current){setPreview(result);change({...value,approved:result.id})}
  }catch(err){if(alive.current)setError(errorMessage(err,t('切点预览失败，请检查音频与每段时长。')))}finally{if(alive.current)setBusy(false)}
 }
 return <section className="editing-note avatar-split" aria-label={t('配音切点')}><h4>{t('配音切点')}</h4><p>{t('停顿检测只寻找安静位置，不理解句意。请试听并用人工切点避开词句；相邻片段的动作仍需人工检查。')}</p>
 <fieldset disabled={disabled||busy}><label>{t('分段方式')}<select value={value.mode} onChange={e=>edit({...value,mode:e.target.value as AvatarSplitForm['mode']})}><option value="pause">{t('优先停顿位置')}</option><option value="fixed">{t('固定18秒窗口')}</option><option value="manual">{t('人工指定秒数')}</option></select></label>
 {value.mode==='manual'&&<label>{t('切点秒数（逗号分隔）')}<input value={value.manual} placeholder="12.5, 27.8, 43.2" onChange={e=>edit({...value,manual:e.target.value})}/><span className="drama-help">{t('不填写起点或结尾；每段须不足20秒且至少有一帧。切点按48kHz采样固定。')}</span></label>}
 <button disabled={!audio||!fields.valid} onClick={()=>void check()}>{t(busy?'正在分析本地音频…':'预览并固定切点（不调用模型）')}</button></fieldset>
 {!fields.valid&&<p role="alert">{t('切点须为递增的正数。')}</p>}{error&&<p role="alert">{error}</p>}
 {preview&&<div role="status"><p>{t('切点已固定；若音频或计划变化，生成将停止。')}</p><ol>{preview.segments.map(seg=><li key={seg.index}>{t('第 {index} 段',{index:seg.index})} · {seg.start_seconds.toFixed(3)}s–{(seg.start_seconds+seg.duration_seconds).toFixed(3)}s · {t(avatarCutLabel(seg.cut_reason))}</li>)}</ol></div>}
 </section>
}
