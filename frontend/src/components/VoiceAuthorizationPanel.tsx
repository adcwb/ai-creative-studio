import {useState,useCallback,useEffect} from 'react'
import {useI18n} from '../i18n'
import {request} from '../api'
import {usePolling} from '../hooks/usePolling'
import {useNavigationTask} from '../app/navigationGuard'
import {useSeriesWrite} from '../features/series/useSeriesWrite'
export interface VoiceAuthorization {id:string;audio_asset_id:string;consent_reference:string;created_by:string;revoked_at?:string}
export function VoiceAuthorizationPanel({project,space,audio,value,disabled,choose,onBusy}:{project:string;space:string;audio:string;value:VoiceAuthorization|null;disabled:boolean;choose:(v:VoiceAuthorization|null)=>void;onBusy:(busy:boolean)=>void}){
 const {t}=useI18n(),[items,setItems]=useState<VoiceAuthorization[]>([]),[reference,setReference]=useState(''),[confirmed,setConfirmed]=useState(false),[error,setError]=useState(''),[revision,setRevision]=useState(0),[revokeConfirmed,setRevokeConfirmed]=useState(false)
 const created=useCallback((v:VoiceAuthorization)=>{choose(v);setReference('');setConfirmed(false);setRevision(n=>n+1)},[choose])
 const revoked=useCallback((v:VoiceAuthorization)=>{choose(v);setRevokeConfirmed(false);setRevision(n=>n+1)},[choose])
 const create=useSeriesWrite<VoiceAuthorization>(`voice-grant:${project}:${audio}`,`/projects/${project}/voice-authorizations`,'POST',created)
 const revoke=useSeriesWrite<VoiceAuthorization>(`voice-revoke:${value?.id??'none'}`,`/projects/${project}/voice-authorizations/${value?.id??'none'}/revoke`,'POST',revoked)
 useEffect(()=>{onBusy(create.busy||create.uncertain||revoke.busy||revoke.uncertain);return()=>onBusy(false)},[onBusy,create.busy,create.uncertain,revoke.busy,revoke.uncertain])
 useNavigationTask(Boolean(reference)||confirmed||create.busy||create.uncertain||revoke.busy||revoke.uncertain)
 usePolling(async signal=>{try{const list=await request<VoiceAuthorization[]>(`/projects/${project}/voice-authorizations?space_id=${space}`,{signal});if(!signal.aborted){setItems(list);setError('');if(value){const updated=list.find(v=>v.id===value.id);if(updated?.revoked_at!==value.revoked_at)choose(updated??null)}}}catch(e){if(!signal.aborted)setError(t('声音授权读取失败，请重新读取或确认后端版本。'));throw e}},2500,`voice-grants:${space}:${project}:${revision}`)
 const locked=disabled||create.busy||create.uncertain||revoke.busy||revoke.uncertain
 return <section className="editing-note" aria-label={t('声音使用授权')}><h4>{t('声音使用授权')}</h4><p>{t('为这份音频保存独立授权，可用于多次口播并随时撤销。声明由创作者负责，不代表平台已核验商业许可。')}</p>{error&&<p role="alert">{error}</p>}
 <label>{t('已保存声音授权')}<select disabled={locked} value={value?.id??''} onChange={e=>choose(items.find(v=>v.id===e.target.value)??null)}><option value="">{t('选择授权或在下方登记')}</option>{items.filter(v=>v.audio_asset_id===audio).map(v=><option key={v.id} value={v.id} disabled={Boolean(v.revoked_at)}>{v.consent_reference}{v.revoked_at?' · '+t('已撤销'):''}</option>)}{value&&!items.some(v=>v.id===value.id)&&<option value={value.id}>{value.consent_reference}</option>}</select></label>
 <fieldset disabled={locked||!audio}><label>{t('声音授权依据')}<input maxLength={300} value={reference} onChange={e=>setReference(e.target.value)}/></label><label><input type="checkbox" checked={confirmed} onChange={e=>setConfirmed(e.target.checked)}/>{t('我确认有权使用所选配音制作此口播')}</label></fieldset>
 <button disabled={disabled||create.busy||!create.uncertain&&(!audio||!confirmed||reference.trim().length<3)} onClick={()=>void create.save({audio_asset_id:audio,consent_reference:reference.trim(),confirmed:true})}>{t(create.uncertain?'使用原请求核对结果':'保存可复用声音授权')}</button>{create.error&&<p role="alert">{create.error}</p>}{create.storageNotice&&<p role="status">{create.storageNotice}</p>}
 {value&&!value.revoked_at&&<><p>{value.consent_reference}</p><label><input type="checkbox" checked={revokeConfirmed} disabled={locked} onChange={e=>setRevokeConfirmed(e.target.checked)}/>{t('确认撤销此声音授权，停止关联的新派发')}</label><button disabled={disabled||revoke.busy||!revokeConfirmed&&!revoke.uncertain} onClick={()=>void revoke.save({confirmed:true})}>{t(revoke.uncertain?'使用原请求核对结果':'撤销此声音授权')}</button>{revoke.error&&<p role="alert">{revoke.error}</p>}</>}{value?.revoked_at&&<p role="status">{t('此声音授权已撤销；历史成片保留，不能继续用此授权生成。')}</p>}
 </section>
}
