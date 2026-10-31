import { useEffect, useState } from 'react'
import { request, errorMessage } from '../api'
import { useAppWorkspace } from '../app/workspace'
import { useI18n } from '../i18n'
import { formatMinorCurrency } from '../money'
import { previewFeature, readinessNames } from '../gatewayModels'

import { initialRuntimeChoice, runtimeFingerprint, type RuntimeChoice, type RuntimeModelOption, type RuntimePreview } from '../runtimeSelection'

export function RuntimeModelPicker({space, capability, value, onChange, preview, disabled = false}: {
  space: string; capability: string; value: RuntimeChoice | null; onChange: (value: RuntimeChoice | null) => void; disabled?: boolean; preview?: RuntimePreview
}) {
  const {t, locale, formatDate} = useI18n()
  const demo = useAppWorkspace()?.session.mode === 'demo'
  const [catalog, setCatalog] = useState<{governance_ready: boolean; models: RuntimeModelOption[]; bindings?: {feature_key: string; model_config_id: string; allow_override: boolean; enabled: boolean; voice?: string}[]} | null>(null)
  const [revision, setRevision] = useState(0)
  const [error, setError] = useState(false)
  const [quoteError, setQuoteError] = useState('')
  const [quoting, setQuoting] = useState(false)
  const feature = previewFeature(preview)
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
  useEffect(() => {
    if (!value?.quoteReady || !value.model.quote) return
    const delay = Math.max(0, Math.min(2147483647, Date.parse(value.model.quote.expires_at) - Date.now()))
    const timer = setTimeout(() => onChange({...value, confirmed: false, quoteReady: false}), delay)
    return () => clearTimeout(timer)
  }, [value, onChange])
  async function previewCost() {
    if (!value || !preview || quoting) return
    const selected = value
    const fingerprint = runtimeFingerprint(preview, selected.voice)
    setQuoting(true); setQuoteError('')
    onChange({...selected, confirmed: false, quoteReady: false})
    try {
      const quote = await request<NonNullable<RuntimeModelOption['quote']>>(`/runtime-quote?${new URLSearchParams({space_id: space})}`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({...preview, model_config_id: selected.model.id, model_config_version: selected.model.version, ...(selected.voice ? {voice: selected.voice} : {})})})
      onChange({...selected, model: {...selected.model, quote}, confirmed: false, quoteReady: true, fingerprint})
    } catch (reason) {setQuoteError(errorMessage(reason, t('当前请求没有可用报价，请调整请求或联系管理员。')))} finally {setQuoting(false)}
  }
  // Legacy demo mode has no runtime catalog. Its layout and local behavior remain unchanged.
  if (demo) return null
  if (!catalog) return error ? <p role="alert">{t('模型目录读取失败，请刷新后重试。')}</p> : null
  const requiredCapability = feature === 'image_edit' ? 'image_edit' : feature === 'subtitle_alignment' ? 'asr_timestamps' : capability
  const options = catalog.models.filter(model => model.capability === capability && (!model.gateway_id || (model.capabilities?.includes(requiredCapability) && (!binding || binding.allow_override || model.id === binding.model_config_id))) && (!binding || !!model.gateway_id))
  const inputChanged = !!value && !!preview && value.fingerprint !== runtimeFingerprint(preview, value.voice)
  const stale = !!value && !options.some(model => model.id === value.model.id && model.version === value.model.version && model.available === value.model.available)
  return <fieldset className="runtime-model-picker" disabled={disabled || quoting}>
    <legend>{t('本次执行模型')}</legend>
    {binding && <p>{t('已使用此功能的默认绑定，切换仅影响本次生成。')}</p>}
    <label>{t('AI 网关')}<select value={value?.model.gateway_id ?? ''} disabled={!!binding && !binding.allow_override} onChange={event => {const model = options.find(item => item.gateway_id === event.target.value); onChange(model ? initialRuntimeChoice(model) : null)}}><option value="">{t('选择网关')}</option>{[...new Map(options.filter(item => item.gateway_id).map(item => [item.gateway_id!, item.gateway_name!])).entries()].map(([id, name]) => <option key={id} value={id}>{name}</option>)}</select></label>
    <label>{t('模型选择')}<select value={value?.model.id ?? ''} disabled={!!binding && !binding.allow_override} onChange={event => {const model = options.find(item => item.id === event.target.value); onChange(model ? initialRuntimeChoice(model) : null)}}>
      <option value="" disabled={!!binding}>{t(binding ? '请选择此功能的模型' : '保留当前本地或演示模式')}</option>
      {options.filter(model => !value?.model.gateway_id || model.gateway_id === value.model.gateway_id).map(model => <option key={model.id} value={model.id}>{model.name ?? model.provider} / {model.model}{model.is_default ? ` · ${t('组织默认')}` : ''}{!model.available ? ` · ${t('暂不可执行')}` : ''}</option>)}
    </select></label>
    <button type="button" onClick={() => {setCatalog(null); onChange(null); setRevision(previous => previous + 1)}}>{t('刷新模型目录')}</button>
    {options.length === 0 && <p>{t('此能力尚无启用的组织模型。')}</p>}
    {value?.model.reason_code && <p role="status">{t(readinessNames[value.model.reason_code] ?? value.model.reason_code)}</p>}
    {(error || stale) && <p role="alert">{t('模型目录已失效，请刷新并重新确认。')}</p>}
    {value && <><button type="button" disabled={!value.model.available || !preview || quoting} onClick={() => {void previewCost()}}>{quoting ? t('正在读取费用报价…') : t('获取当前请求报价（不调用模型）')}</button>{quoteError && <p role="alert">{quoteError}</p>}{inputChanged && value.fingerprint && <p role="status">{t('请求内容已改变，请重新获取并确认报价。')}</p>}<p>{t('配置版本：{version}', {version: value.model.version})}</p>
      {capability === 'speech' && <label>{t('声音标识')}<input value={value.voice} maxLength={128} autoComplete="off" onChange={event => onChange({...value, voice: event.target.value, confirmed: false, quoteReady: false})} /></label>}
      <p role="status">{!catalog.governance_ready ? t('费用与审核治理尚未就绪，远端执行不可用。') : value.model.quote ? t('单次预留上限：{amount}；报价来源：{source}；有效至：{expiry}', {amount: formatMinorCurrency(value.model.quote.maximum_minor, value.model.quote.currency, value.model.quote.minor_units, locale), source: value.model.quote.source, expiry: formatDate(value.model.quote.expires_at)}) : t('尚未获取本次请求的可信费用上限，远端提交保持禁用。')}</p>
      <p>{t('模型调用可能计费。取消、超时或结果未知不代表免费；费用按原币种预留，不自动兑换或重试。')}</p>
      <label><input type="checkbox" checked={value.confirmed} disabled={!value.quoteReady || stale || inputChanged} onChange={event => onChange({...value, confirmed: event.target.checked})} />{t('确认本次报价与可能产生的费用')}</label>
    </>}
  </fieldset>
}
