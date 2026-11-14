import {useRef,useState} from 'react'
import {baseURL} from '../api'
import {useI18n} from '../i18n'
export interface AvatarTake {id:string;asset_id:string;name:string;file_url:string;model?:string}
export function AvatarTakeCompare({output,takes,value,change,locked}:{output:AvatarTake;takes:AvatarTake[];value:string;change:(v:string)=>void;locked:boolean}){
 const {t}=useI18n(),a=useRef<HTMLVideoElement>(null),b=useRef<HTMLVideoElement>(null),[sound,setSound]=useState('a'),[error,setError]=useState('')
 const other=takes.find(v=>v.id===value)
 async function play(){setError('');const players=[a.current,b.current].filter((v):v is HTMLVideoElement=>Boolean(v));for(const video of players){video.pause();video.currentTime=0};const results=await Promise.allSettled(players.map(v=>v.play()));if(results.some(v=>v.status==='rejected')){for(const video of players)video.pause();setError(t('播放器尚未就绪，请检查素材后重试。'))}}
 function choose(id:string){a.current?.pause();b.current?.pause();if(!id)setSound('a');setError('');change(id)}
 return <div className="avatar-take-compare"><label>{t('对比另一个版本')}<select value={value} disabled={locked} onChange={e=>choose(e.target.value)}><option value="">{t('仅审阅当前版本')}</option>{takes.map(take=><option key={take.id} value={take.id}>{take.name}{take.model?' · '+take.model:''}</option>)}{value&&!other&&<option value={value}>{t('原对比版本不可用')}</option>}</select></label>
 <div className={other?'avatar-review-sources':'avatar-take-single'}><div><p>{t('当前版本')} · {output.name}</p><video ref={a} controls muted={Boolean(other)&&sound==='b'} preload="metadata" src={baseURL+output.file_url}/></div>{other&&<div key={other.id}><p>{t('对比版本')} · {other.name}</p><video ref={b} controls muted={sound==='a'} preload="metadata" src={baseURL+other.file_url}/></div>}</div>
 {other&&<><p className="drama-help">{t('两个版本使用同一形象和配音；同时从头播放辅助人工比较，不代表逐帧同步或自动质量评分。')}</p><label>{t('试听声音')}<select value={sound} onChange={e=>setSound(e.target.value)}><option value="a">{t('当前版本')}</option><option value="b">{t('对比版本')}</option></select></label><div className="media-actions"><button type="button" onClick={()=>void play()}>{t('同时从头播放')}</button><button type="button" onClick={()=>{a.current?.pause();b.current?.pause()}}>{t('暂停两个版本')}</button></div></>}{error&&<p role="alert">{error}</p>}
 </div>
}
