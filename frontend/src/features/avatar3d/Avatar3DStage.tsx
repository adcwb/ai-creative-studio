import {useEffect,useRef,useState} from 'react'
import type {TalkingHead} from '@met4citizen/talkinghead'
import {ApiError,baseURL,errorMessage,request} from '../../api'
import {useI18n} from '../../i18n'
import {audioJawMotion} from './audioMotion'
function release(head:TalkingHead|null){if(!head)return;try{head.dispose()}catch{/* Async loaders may settle after disposal. */}finally{if(head.audioCtx.state!=='closed')void head.audioCtx.close().catch(()=>{})}}
export interface Avatar3DModel {id:string;name:string;builtin?:string;body:'M'|'F';model_sha256:string;license_reference:string;consent_reference:string;revoked_at?:string}
interface AudioAsset {id:string;file_url:string;name:string}
export function Avatar3DStage({project,space,model,audio,voice,disabled}:{project:string;space:string;model:Avatar3DModel;audio?:AudioAsset;voice?:string;disabled:boolean}){
 const {t}=useI18n(),host=useRef<HTMLDivElement>(null),engine=useRef<TalkingHead|null>(null),alive=useRef(false),flight=useRef(false),operation=useRef(0),audioAbort=useRef<AbortController|null>(null)
 const [ready,setReady]=useState(false),[loading,setLoading]=useState(false),[playing,setPlaying]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(''),[revision,setRevision]=useState(0)
 const [quality,setQuality]=useState(()=>matchMedia('(max-width:720px)').matches?'low':'standard')
 const prefix=`/projects/${encodeURIComponent(project)}/avatar-3d-models/${encodeURIComponent(model.id)}`,query=new URLSearchParams({space_id:space}).toString()
 function stop(){operation.current++;audioAbort.current?.abort();engine.current?.stopSpeaking();engine.current?.setFixedValue('jawOpen',0,0);setPlaying(false)}
 useEffect(()=>{
  let mounted=true,visible=true,h:TalkingHead|null=null,url='';const abort=new AbortController();alive.current=true;setLoading(true);setReady(false);setError('')
  async function load(){try{
   const {TalkingHead}=await import('@met4citizen/talkinghead');if(!mounted||!host.current)return
   h=new TalkingHead(host.current,{lipsyncModules:[],ttsEndpoint:'',ttsApikey:null,dracoEnabled:false,cameraView:'head',cameraRotateEnable:true,cameraPanEnable:false,cameraZoomEnable:false,modelPixelRatio:(quality==='low'?1:Math.min(devicePixelRatio,1.5))/Math.max(1,devicePixelRatio),modelFPS:quality==='low'?24:30,avatarIdleEyeContact:0,avatarSpeakingEyeContact:0,lightAmbientIntensity:.7,lightDirectIntensity:4})
   engine.current=h
   host.current.querySelector('canvas')?.addEventListener('webglcontextlost',()=>{if(mounted){h?.stopSpeaking();h?.stop();setReady(false);setPlaying(false);setError(t('浏览器图形资源已中断，请用轻量模式重新加载。'))}})
   const source=model.builtin==='mpfb-cc0-v1'?'/avatars/mpfb-63c645a2.glb.gz':`${baseURL}${prefix}/file?${query}`
   const response=await fetch(source,{credentials:'include',signal:abort.signal});if(!response.ok){if(response.status===401)window.dispatchEvent(new Event('studio-session-expired'));throw new ApiError(t('三维模型加载失败，请检查模型骨架、贴图和浏览器WebGL支持。'),response.status,'model_unavailable')}
   let blob=await response.blob()
   if(model.builtin==='mpfb-cc0-v1'){const magic=new Uint8Array(await blob.slice(0,2).arrayBuffer());if(magic[0]===31&&magic[1]===139)blob=await new Response(blob.stream().pipeThrough(new DecompressionStream('gzip'))).blob()}
   if(!mounted)return;url=URL.createObjectURL(blob)
   await h.showAvatar({url,body:model.body,avatarMood:'neutral',lipsyncLang:'en'},undefined,gltf=>{if(!mounted)throw new DOMException('Aborted model load','AbortError');gltf.scene.traverse(node=>{if(node.isBone&&node.name.startsWith('mixamorig:'))node.name=node.name.replace(/^mixamorig:/,'')})});if(!mounted){release(h);return}
   h.renderer?.render(h.scene,h.camera);if(!visible||document.hidden)h.stop();setReady(true);setLoading(false)
  }catch(e){if(mounted){release(h);engine.current=null;setError(e instanceof ApiError?errorMessage(e,t('三维模型加载失败，请检查模型骨架、贴图和浏览器WebGL支持。')):t('三维模型加载失败，请检查模型骨架、贴图和浏览器WebGL支持。'));setLoading(false)}}finally{if(url){URL.revokeObjectURL(url);url=''}}}
  void load()
  const visibility=()=>{if(document.hidden){operation.current++;audioAbort.current?.abort();h?.stopSpeaking();h?.stop();if(mounted)setPlaying(false)}else h?.start()}
  document.addEventListener('visibilitychange',visibility)
  const observer=new IntersectionObserver(entries=>{visible=Boolean(entries[0]?.isIntersecting);if(visible&&!document.hidden)h?.start();else{operation.current++;audioAbort.current?.abort();h?.stopSpeaking();h?.stop();if(mounted)setPlaying(false)}})
  const resize=new ResizeObserver(()=>{if(mounted)h?.renderer?.render(h.scene,h.camera)});if(host.current){observer.observe(host.current);resize.observe(host.current)}
  return()=>{mounted=false;alive.current=false;audioAbort.current?.abort();abort.abort();observer.disconnect();resize.disconnect();document.removeEventListener('visibilitychange',visibility);release(h);engine.current=null;if(url)URL.revokeObjectURL(url)}
 },[model.id,model.model_sha256,model.body,model.builtin,quality,revision,prefix,query,t])
 useEffect(()=>{operation.current++;audioAbort.current?.abort();engine.current?.stopSpeaking();setPlaying(false)},[disabled,model.revoked_at,audio?.id,voice])
 useEffect(()=>{
  if(!playing)return
  const abort=new AbortController();let checking=false
  const timer=setInterval(()=>{
   if(!engine.current?.isSpeaking&&!engine.current?.isAudioPlaying){setPlaying(false);return}
   if(checking)return;checking=true
   void request(`${prefix}/playbacks?${query}`,{method:'POST',signal:abort.signal,headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({audio_asset_id:audio?.id,voice_authorization_id:voice})}).catch(e=>{if(!abort.signal.aborted){engine.current?.stopSpeaking();setPlaying(false);setError(errorMessage(e,t('授权核对失败，已停止播放。')))}}).finally(()=>{checking=false})
  },2000)
  return()=>{clearInterval(timer);abort.abort()}
 },[playing,prefix,query,audio?.id,voice,t])
 async function play(){
  const h=engine.current;if(!h||!audio||!voice||disabled||flight.current)return
  flight.current=true;const token=++operation.current,abort=new AbortController();audioAbort.current=abort;setBusy(true);setError('')
  try{
   await h.audioCtx.resume()
   const check=()=>request(`${prefix}/playbacks?${query}`,{method:'POST',signal:abort.signal,headers:{'Content-Type':'application/json','Idempotency-Key':crypto.randomUUID()},body:JSON.stringify({audio_asset_id:audio.id,voice_authorization_id:voice})})
   await check();const response=await fetch(`${baseURL}${audio.file_url}`,{credentials:'include',signal:abort.signal});if(!response.ok){if(response.status===401)window.dispatchEvent(new Event('studio-session-expired'));throw new ApiError(t('配音无法解码或授权不可用，请重新选择音频。'),response.status,'audio_unavailable')}
   const decoded=await h.audioCtx.decodeAudioData(await response.arrayBuffer());await check()
   if(!alive.current||engine.current!==h||token!==operation.current||document.hidden)return
   h.stopSpeaking();h.setFixedValue('jawOpen',null,0);h.start();h.speakAudio({audio:decoded,anim:audioJawMotion(decoded)},{isRaw:true});setPlaying(true)
  }catch(e){if(alive.current&&!abort.signal.aborted){h.stopSpeaking();setError(errorMessage(e,t('配音无法解码或授权不可用，请重新选择音频。')))}}finally{flight.current=false;if(alive.current)setBusy(false)}
 }
 return <section className="avatar3d-stage panel" aria-label={t('网页三维角色预览')}><div className="section-heading"><h3>{model.name}</h3><label>{t('显示质量')}<select value={quality} onChange={e=>{stop();setQuality(e.target.value)}} disabled={busy}><option value="low">{t('轻量（手机优先）')}</option><option value="standard">{t('标准')}</option></select></label></div>
 <div ref={host} className="avatar3d-viewport" data-webgl-ready={ready} aria-label={t('三维角色画面')}/>
 {loading&&<p role="status">{t('正在加载三维模型，首次加载可能需要一些时间…')}</p>}{error&&<div role="alert" className="error">{error}<button onClick={()=>setRevision(v=>v+1)}>{t('重新加载模型')}</button></div>}
 <div className="avatar3d-playback"><span className="eyebrow">{t('基础音量口型 · 非中文音素对齐')}</span><button className="primary" disabled={!ready||!audio||!voice||disabled||!!model.revoked_at||busy} onClick={()=>void play()}>{t(busy?'正在核对授权…':playing?'从头播放口播':'播放三维口播')}</button><button disabled={!playing&&!busy} onClick={stop}>{t('停止播放')}</button><span role="status">{t(playing?'正在播放':ready?'模型已就绪':'模型尚未就绪')}</span></div>
 <p className="drama-help">{t('三维角色在浏览器实时渲染；嘴部按声音强弱变化，不代表中文发音级口型。当前不将网页预览导出为视频，也不把照片转换成写实三维模型。')}</p>
 </section>
}
