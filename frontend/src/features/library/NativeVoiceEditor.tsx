import {useEffect,useState} from 'react'
import {request,errorMessage} from '../../api'
import {useI18n} from '../../i18n'
import type {RuntimeModelOption} from '../../runtimeSelection'
import type {NativeVoicePreset} from './types'

export function NativeVoiceEditor({space,value,disabled,onChange}:{space:string;value?:NativeVoicePreset;disabled:boolean;onChange:(value:NativeVoicePreset|undefined)=>void}) {
 const {t}=useI18n()
 const [models,setModels]=useState<RuntimeModelOption[]>([]),[error,setError]=useState(''),[revision,setRevision]=useState(0)
 useEffect(()=>{const abort=new AbortController();void request<{models:RuntimeModelOption[]}>(`/runtime-models?${new URLSearchParams({space_id:space,feature_key:'speech_synthesis'})}`,{signal:abort.signal}).then(v=>{if(!abort.signal.aborted){setModels(v.models.filter(m=>m.capability==='speech'&&['gateway-speech','minimax-speech','gemini-speech','bytedance-speech'].includes(m.provider)));setError('')}}).catch(e=>{if(!abort.signal.aborted)setError(errorMessage(e,t('音色模型读取失败')))});return()=>abort.abort()},[space,revision,t])
 const matched=value&&models.find(m=>m.id===value.model_config_id&&m.provider===value.provider&&m.model===value.model)
 return <div className="library-wide library-native-voice"><label><input type="checkbox" disabled={disabled} checked={!!value} onChange={e=>onChange(e.target.checked?{model_config_id:'',provider:'',model:'',voice:''}:undefined)}/>{t('保存可复用的原生音色')}</label><p className="drama-help">{t('原生音色与模型固定关联；不上传录音，不克隆声音。表演、语速与发音备注仅作创作资料，当前不会作为语音参数发送。')}</p>
 {error&&<p role="alert" className="error">{error}</p>}{(error||value)&&<button type="button" disabled={disabled} onClick={()=>setRevision(v=>v+1)}>{t('刷新音色模型')}</button>}
 {value&&<><label>{t('原生音色模型')}<select aria-label={t('原生音色模型')} required disabled={disabled} value={value.model_config_id} onChange={e=>{const model=models.find(m=>m.id===e.target.value);if(model)onChange({model_config_id:model.id,provider:model.provider,model:model.model,voice:''})}}><option value="">{t('选择配音模型')}</option>{!matched&&value.model_config_id&&<option value={value.model_config_id}>{t('已保存模型')} / {value.model}</option>}{models.map(m=><option key={m.id} value={m.id}>{m.name??m.provider} / {m.model}</option>)}</select></label>{!matched&&value.model_config_id&&<p role="status">{t('保存的原生音色模型当前不在目录中；原记录保留，使用前需核对匹配配置。')} {value.model} · {value.voice}</p>}<label>{t('原生音色标识')}<input required disabled={disabled} maxLength={128} autoComplete="off" value={value.voice} onChange={e=>onChange({...value,voice:e.target.value})}/></label></>}
 </div>
}
