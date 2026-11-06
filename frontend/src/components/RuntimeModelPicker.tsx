import {gatewayCreationRequired} from '../creationGateway'
import {Link} from 'react-router-dom'
import { useEffect, useRef, useState } from 'react'
import { request } from '../api'
import { useAppWorkspace } from '../app/workspace'
import { useI18n } from '../i18n'
import { previewFeature, readinessNames } from '../gatewayModels'

import { initialRuntimeChoice, type RuntimeChoice, type RuntimeModelOption, type RuntimePreview } from '../runtimeSelection'

export function RuntimeModelPicker({space, capability, value, onChange, preview, disabled = false, gatewayOnly = false, fixedVoice = false}: {
  space: string; capability: string; value: RuntimeChoice | null; onChange: (value: RuntimeChoice | null) => void; disabled?: boolean; preview?: RuntimePreview; gatewayOnly?: boolean; fixedVoice?: boolean
}) {
  const {t} = useI18n()
  const requireGateway = gatewayOnly && gatewayCreationRequired
  const demo = useAppWorkspace()?.session.mode === 'demo'
  const [catalog, setCatalog] = useState<{governance_ready: boolean; models: RuntimeModelOption[]; bindings?: {feature_key: string; model_config_id: string; allow_override: boolean; enabled: boolean; voice?: string}[]} | null>(null)
  const [revision, setRevision] = useState(0)
  const [error, setError] = useState(false)
  const feature = previewFeature(preview)
  const previousFeature = useRef(feature)
  useEffect(() => {
    if (requireGateway && previousFeature.current !== feature) {previousFeature.current = feature; onChange(null)}
  }, [feature, onChange, requireGateway])
  const binding = catalog?.bindings?.find(item => item.feature_key === feature && item.enabled)
  useEffect(() => {
    if (demo) return
    const controller = new AbortController()
    void request<NonNullable<typeof catalog>>(`/runtime-models?${new URLSearchParams({space_id: space, feature_key: feature})}`, {signal: controller.signal})
      .then(result => {if (!controller.signal.aborted) {setCatalog(result); setError(false)}})
      .catch(() => {if (!controller.signal.aborted) setError(true)})
    return () => controller.abort()
  }, [space, revision, feature, demo])
  useEffect(() => {
    if (!catalog || value || !binding) return
    const model = catalog.models.find(item => item.id === binding.model_config_id && item.capability === capability)
    if (model) onChange({...initialRuntimeChoice(model), voice: binding.voice ?? ''})
  }, [catalog, value, binding, capability, onChange])
  // Legacy demo mode has no runtime catalog. Its layout and local behavior remain unchanged.
  if (demo) return requireGateway ? <p role="alert">{t('真实网关创作需要登录组织账户，请切换到安全登录模式。')}</p> : null
  if (!catalog) return <div className="runtime-model-picker" role={error ? 'alert' : 'status'}>{t(error ? '模型目录读取失败，请刷新后重试。' : '正在读取模型配置…')}{error && <button type="button" onClick={() => setRevision(previous => previous + 1)}>{t('刷新模型目录')}</button>}</div>
  const requiredCapability = feature === 'image_edit' ? 'image_edit' : feature === 'subtitle_alignment' ? 'asr_timestamps' : capability
  const options = catalog.models.filter(model => (!requireGateway || !!model.gateway_id) && model.capability === capability && (!model.gateway_id || (model.capabilities?.includes(requiredCapability) && (!binding || binding.allow_override || model.id === binding.model_config_id))) && (!binding || !!model.gateway_id)).map(model => requireGateway && !binding ? {...model, available: false} : model)
  const stale = !!value && !options.some(model => model.id === value.model.id && model.version === value.model.version && model.available === value.model.available)
  return <fieldset className="runtime-model-picker" disabled={disabled}>
    <legend>{t('本次执行模型')}</legend>
    {requireGateway && !binding && <p role="alert">{t('此功能尚未绑定网关模型，请联系组织管理员配置。')} <Link to="/settings/models">{t('模型管理')}</Link></p>}
    {binding && <p>{t('已使用此功能的默认绑定，切换仅影响本次生成。')}</p>}
    <label>{t('AI 网关')}<select value={value?.model.gateway_id ?? ''} disabled={!!binding && !binding.allow_override} onChange={event => {const model = options.find(item => item.gateway_id === event.target.value); onChange(model ? initialRuntimeChoice(model) : null)}}><option value="">{t('选择网关')}</option>{[...new Map(options.filter(item => item.gateway_id).map(item => [item.gateway_id!, item.gateway_name!])).entries()].map(([id, name]) => <option key={id} value={id}>{name}</option>)}</select></label>
    <label>{t('模型选择')}<select aria-label={t('模型选择')} value={value?.model.id ?? ''} disabled={!!binding && !binding.allow_override} onChange={event => {const model = options.find(item => item.id === event.target.value); onChange(model ? initialRuntimeChoice(model) : null)}}>
      <option value="" disabled={!!binding}>{t(binding || requireGateway ? '请选择此功能的模型' : '保留当前本地或演示模式')}</option>
      {options.filter(model => !value?.model.gateway_id || model.gateway_id === value.model.gateway_id).map(model => <option key={model.id} value={model.id}>{model.name ?? model.provider} / {model.model}{model.is_default ? ` · ${t('组织默认')}` : ''}{!model.available ? ` · ${t('暂不可执行')}` : ''}</option>)}
    </select></label>
    <button type="button" onClick={() => {setCatalog(null); onChange(null); setRevision(previous => previous + 1)}}>{t('刷新模型目录')}</button>
    {options.length === 0 && <p>{t('此能力尚无启用的组织模型。')}</p>}
    {value?.model.reason_code && <p role="status">{t(readinessNames[value.model.reason_code] ?? value.model.reason_code)}</p>}
    {(error || stale) && <p role="alert">{t('模型目录已失效，请刷新并重新确认。')}</p>}
    {value && <><p>{t('配置版本：{version}', {version: value.model.version})}</p>
      {capability === 'speech' && <label>{t('声音标识')}<input readOnly={fixedVoice} value={value.voice} maxLength={128} autoComplete="off" onChange={event => onChange({...value, voice: event.target.value})} /></label>}
      {!catalog.governance_ready && <p role="status">{t('内容审核尚未就绪，请联系部署管理员。')}</p>}
    </>}
  </fieldset>
}
