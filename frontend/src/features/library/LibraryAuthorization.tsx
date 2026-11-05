import {useEffect, useRef, useState} from 'react'
import {ApiError, errorMessage, request} from '../../api'
import {useNavigationTask} from '../../app/navigationGuard'
import {useI18n} from '../../i18n'
import type {LibraryKind} from './types'
import {readAuthorizationPending,pendingKey} from './authorizationPending'

interface Authorization {revision:number;status:'granted'|'revoked';version_ids:string[];purposes:string[];rights_holder:string;basis:string;evidence:string;attested:boolean;expires_at?:string;reason:string;updated_at:string}
interface View {asset_id:string;head_version_id:string;authorization:Authorization|null}
export function LibraryAuthorization({space,asset,version,number,versions,kind,archived,readOnly,onPending}:{space:string;asset:string;version:string;number:number;versions:{id:string;number:number}[];kind:LibraryKind;archived:boolean;readOnly:boolean;onPending:(v:boolean)=>void}) {
 const {t,formatDate}=useI18n()
 const [current,setCurrent]=useState<View|null>(null),[error,setError]=useState(''),[busy,setBusy]=useState(false)
 const [pending,setPending]=useState(()=>readAuthorizationPending(space,asset))
 const [selectedVersions,setSelectedVersions]=useState([version]),[dirty,setDirty]=useState(false)
 const versionOptions=[...new Map([{id:version,number},...versions].map(v=>[v.id,v])).values()]
 const [holder,setHolder]=useState(''),[basis,setBasis]=useState('own_work'),[evidence,setEvidence]=useState(''),[expiry,setExpiry]=useState(''),[reason,setReason]=useState(''),[attested,setAttested]=useState(false),[purposes,setPurposes]=useState<string[]>([])
 const mounted=useRef(true)
 const path=`/library/assets/${asset}/use-authorization?${new URLSearchParams({space_id:space})}`
 useNavigationTask(dirty||busy||!!pending)
 useEffect(()=>{onPending(busy||!!pending)},[busy,pending,onPending])
 useEffect(()=>{mounted.current=true;const abort=new AbortController();void request<View>(path,{signal:abort.signal}).then(v=>{if(!abort.signal.aborted)setCurrent(v)}).catch(e=>{if(!abort.signal.aborted)setError(errorMessage(e,t('授权读取失败')))});return()=>{mounted.current=false;abort.abort()}},[path,t])
 async function refresh(){setError('');try{const v=await request<View>(path);if(mounted.current)setCurrent(v)}catch(e){if(mounted.current)setError(errorMessage(e,t('授权读取失败')))}}
 async function submit(status:'granted'|'revoked') {
  if(busy||readOnly||!current)return
  let intent=pending
  if(!intent){const body={expected_revision:current.authorization?.revision??0,status,reason,...(status==='granted'?{version_ids:selectedVersions,purposes,rights_holder:holder,basis,evidence,attested,...(expiry?{expires_at:new Date(expiry).toISOString()}:{} )}:{})};intent={key:crypto.randomUUID(),body:JSON.stringify(body)};try{sessionStorage.setItem(pendingKey(space,asset),JSON.stringify(intent))}catch{setError(t('无法保存请求恢复记录，请允许此站点使用会话存储后重试。'));return}setPending(intent)}
  setBusy(true);setError('')
  try {
   await request<View>(path,{method:'POST',headers:{'Content-Type':'application/json','Idempotency-Key':intent.key},body:intent.body})
   // A historical receipt may precede a newer revocation. Only GET is current.
   const fresh=await request<View>(path)
   sessionStorage.removeItem(pendingKey(space,asset))
   if(mounted.current){setCurrent(fresh);setPending(null);setAttested(false);setDirty(false)}
  }catch(e){if(mounted.current){if(e instanceof ApiError&&e.status<500&&e.status!==401&&e.status!==403){sessionStorage.removeItem(pendingKey(space,asset));setPending(null)}setError(errorMessage(e,t('授权保存结果未确认，请使用原请求核对。')))}}finally{if(mounted.current)setBusy(false)}
 }
 const available=kind==='voice'?[['audio_analysis','配音识别与对齐'],['prompt_context','文字创作意图'],['speech_preset','原生音色配置'],['voice_reference','声音参考']]:[['audio_analysis','衍生配音识别与对齐'],['prompt_context','文字创作意图'],['image_reference','图像参考生成'],['video_reference','视频参考生成']]
 const locked=readOnly||busy||!!pending||!current
 const authorized=current?.authorization
 return <section className="library-authorization" aria-label={t('资产使用授权')}><h3>{t('资产使用授权')}</h3><p>{t('授权仅是创作者的来源声明，不能替代真实权利或模型能力。撤销会阻止新的生成派发，已保存作品与历史版本仍保留。')}</p><p role="status">{t(!current?'正在读取授权…':!authorized?'尚未声明使用授权':authorized.status==='revoked'?'使用授权已撤销':authorized.expires_at&&new Date(authorized.expires_at)<=new Date()?'使用授权已过期':'已有使用授权')}{authorized&&` · ${formatDate(authorized.updated_at)}`}</p>{authorized?.expires_at&&<p>{t('声明有效至 {p0}',{p0:formatDate(authorized.expires_at)})}</p>}{authorized?.status==='granted'&&<p>{t('授权范围')}：{t('版本')} {authorized.version_ids.includes(version)?number:t('其他历史版本')} · {authorized.purposes.map(p=>t(available.find(([key])=>key===p)?.[1]??p)).join('、')}</p>}
 {error&&<p role="alert" className="error">{error}</p>}<button type="button" disabled={busy} onClick={()=>void refresh()}>{t('刷新授权状态')}</button>
 {pending?<><p>{t('授权操作结果未确认，请保留原请求核对；不能直接改成另一个操作。')}</p><button type="button" disabled={busy||readOnly||!current} onClick={()=>void submit('granted')}>{t('核对原授权请求')}</button></>:<form onChange={()=>setDirty(true)} onSubmit={e=>{e.preventDefault();void submit('granted')}}><fieldset className="library-card-fields" disabled={locked||archived}><label>{t('权利人')}<input required maxLength={200} value={holder} onChange={e=>setHolder(e.target.value)}/></label><label>{t('授权依据')}<select value={basis} onChange={e=>setBasis(e.target.value)}><option value="own_work">{t('本人原创')}</option><option value="licensed">{t('已获许可')}</option><option value="consent">{t('当事人同意')}</option></select></label><label className="library-wide">{t('来源与授权证据')}<textarea required maxLength={4000} value={evidence} onChange={e=>setEvidence(e.target.value)}/></label><label>{t('有效期（留空表示未约定）')}<input type="datetime-local" value={expiry} onChange={e=>setExpiry(e.target.value)}/></label><div><p>{t('授权本次勾选的版本；未来版本需另行确认，重新授权将替换旧范围。')}</p>{versionOptions.map(v=><label key={v.id}><input type="checkbox" checked={selectedVersions.includes(v.id)} onChange={e=>setSelectedVersions(ids=>e.target.checked?[...ids,v.id]:ids.filter(id=>id!==v.id))}/>{t('版本')} {v.number}</label>)}{available.map(([key,label])=><label key={key}><input type="checkbox" checked={purposes.includes(key)} onChange={e=>setPurposes(p=>e.target.checked?[...p,key]:p.filter(v=>v!==key))}/>{t(label)}</label>)}</div><label className="library-wide"><input type="checkbox" checked={attested} onChange={e=>setAttested(e.target.checked)}/>{t('我确认具备所选用途的权利，并承担来源声明责任。')}</label><button type="submit" disabled={!attested||!purposes.length||!selectedVersions.length}>{t('确认使用授权')}</button></fieldset></form>}
 {!pending&&<fieldset className="library-card-fields" disabled={locked} onChange={()=>setDirty(true)}><label className="library-wide">{t('变更或撤销说明')}<textarea maxLength={2000} value={reason} onChange={e=>setReason(e.target.value)}/></label><button type="button" disabled={!reason.trim()||authorized?.status==='revoked'} onClick={()=>void submit('revoked')}>{t('撤销使用授权')}</button></fieldset>}
 {kind==='voice'&&<p>{t('声明不会创建声音身份或克隆权限，也不会启用尚未接入的模型参数。')}</p>}</section>
}
