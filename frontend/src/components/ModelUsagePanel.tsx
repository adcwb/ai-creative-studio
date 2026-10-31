import { useEffect, useState } from 'react'
import { errorMessage, request, type Space } from '../api'
import { useI18n } from '../i18n'
import { capabilityNames } from '../gatewayModels'
import { formatMinorCurrency } from '../money'
import { GovernancePanel } from './GovernancePanel'
import './modelManagement.css'

interface ModelCall {id: string; gateway_id: string; gateway_name: string; model_config_id: string; model_name: string; model: string; feature_key: string; capability: string; state: string; error_code?: string; request_id?: string; http_calls: number; input_tokens: number | null; output_tokens: number | null; duration_ms: number; started_at: string; currency: string; maximum_minor: number}
interface Summary {gateway_id: string; gateway_name: string; model_config_id: string; model_name: string; model: string; capability: string; currency: string; calls: number; succeeded: number; failed: number; unknown: number; average_ms: number; p95_ms: number; input_tokens: number; output_tokens: number; metered_calls: number; maximum_minor: number}
interface UsageReport {items: ModelCall[]; summary: Summary[]; total: number; next_offset: number | null}
const stateNames: Record<string, string> = {succeeded: '成功', failed: '失败', unknown: '结果未知'}
const featureNames: Record<string, string> = {brief_analysis: '创意分析', script_generation: '剧本生成', storyboard_planning: '分镜规划', image_generation: '图像创作', character_image: '角色参考图', shot_image: '镜头图片', image_edit: '图片编辑', shot_video: '镜头视频', speech_synthesis: '配音', speech_transcription: '语音转写', subtitle_alignment: '字幕识别', vision_analysis: '图片分析', consistency_check: '跨镜头一致性检查'}
export function ModelUsagePanel({space, onDirtyChange}: {space: Space; onDirtyChange?: (dirty: boolean) => void}) {
  const {t, locale, formatDate, formatNumber} = useI18n()
  const [report, setReport] = useState<UsageReport | null>(null)
  const [facets, setFacets] = useState<Summary[]>([])
  const [gateway, setGateway] = useState('')
  const [model, setModel] = useState('')
  const [feature, setFeature] = useState('')
  const [state, setState] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [offset, setOffset] = useState(0)
  const [revision, setRevision] = useState(0)
  const [tab, setTab] = useState<'summary' | 'calls'>('summary')
  const [error, setError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    const params = new URLSearchParams({space_id: space.id, offset: String(offset), gateway_id: gateway, model_config_id: model, feature_key: feature, state})
    if (from) params.set('from', new Date(`${from}T00:00:00`).toISOString())
    if (to) {const end = new Date(`${to}T00:00:00`); end.setDate(end.getDate() + 1); params.set('to', end.toISOString())}
    void request<UsageReport>(`/model-usage?${params}`, {signal: controller.signal}).then(value => {if (!controller.signal.aborted) {setReport(value); setError(''); if (!gateway && !model && !feature && !state && !from && !to) setFacets(value.summary)}}).catch(reason => {if (!controller.signal.aborted) {setReport(null); setError(errorMessage(reason, '无法读取模型使用统计'))}})
    return () => controller.abort()
  }, [space.id, gateway, model, feature, state, from, to, offset, revision])
  const gateways = [...new Map(facets.map(item => [item.gateway_id, item.gateway_name])).entries()]
  const models = [...new Map(facets.filter(item => !gateway || item.gateway_id === gateway).map(item => [item.model_config_id, item.model_name])).entries()]
  const amount = (minor: number, currency: string) => formatMinorCurrency(minor, currency, ({JPY: 0, KRW: 0, KWD: 3, BHD: 3}[currency] ?? 2), locale)
  const duration = (ms: number) => `${formatNumber(ms / 1000, {maximumFractionDigits: 2})} s`
  const count = (value: number | null) => value === null ? t('未知') : formatNumber(value)
  return <div className="model-management">
    <section className="panel model-management-panel"><div className="section-heading"><div><h2>{t('模型使用情况')}</h2><p>{t('按网关、模型和创作功能统计真实调用；轮询次数与生成任务分别记录。')}</p></div><button onClick={() => setRevision(value => value + 1)}>{t('刷新统计')}</button></div>
      <div className="model-usage-filters">
        <label>{t('开始日期')}<input type="date" value={from} onChange={event => {setFrom(event.target.value); setOffset(0)}} /></label>
        <label>{t('结束日期')}<input type="date" value={to} min={from || undefined} onChange={event => {setTo(event.target.value); setOffset(0)}} /></label>
        <label>{t('AI 网关')}<select value={gateway} onChange={event => {setGateway(event.target.value); setModel(''); setOffset(0)}}><option value="">{t('全部网关')}</option>{gateways.map(([id, name]) => <option key={id} value={id}>{name}</option>)}</select></label>
        <label>{t('模型')}<select value={model} onChange={event => {setModel(event.target.value); setOffset(0)}}><option value="">{t('全部模型')}</option>{models.map(([id, name]) => <option key={id} value={id}>{name}</option>)}</select></label>
        <label>{t('创作功能')}<select value={feature} onChange={event => {setFeature(event.target.value); setOffset(0)}}><option value="">{t('全部功能')}</option>{Object.entries(featureNames).map(([key, name]) => <option key={key} value={key}>{t(name)}</option>)}</select></label>
        <label>{t('执行状态')}<select value={state} onChange={event => {setState(event.target.value); setOffset(0)}}><option value="">{t('全部状态')}</option>{Object.entries(stateNames).map(([key, name]) => <option key={key} value={key}>{t(name)}</option>)}</select></label>
      </div>
      <div className="model-usage-tabs"><button aria-pressed={tab === 'summary'} onClick={() => setTab('summary')}>{t('模型汇总')}</button><button aria-pressed={tab === 'calls'} onClick={() => setTab('calls')}>{t('调用明细')}</button></div>
      {error && <p role="alert" className="error">{error}</p>}
      {!report && !error && <p role="status">{t('正在读取使用统计…')}</p>}
      {report && (report.total === 0 ? <div className="model-management-empty"><p>{t('当前条件下尚无真实模型调用。')}</p><p>{t('配置网关和功能绑定后，从创作页面提交真实生成，调用记录会显示在这里。')}</p></div> : tab === 'summary' ? <div className="table-scroll"><table><thead><tr><th>{t('模型 / 网关')}</th><th>{t('模型能力')}</th><th>{t('调用次数')}</th><th>{t('成功率')}</th><th>{t('失败 / 未知')}</th><th>{t('平均耗时 / P95')}</th><th>{t('输入 / 输出 token')}</th><th>{t('用量已报告')}</th><th>{t('累计调用上限')}</th></tr></thead><tbody>{report.summary.map(item => <tr key={`${item.gateway_id}-${item.model_config_id}-${item.capability}-${item.currency}`}><td><strong>{item.model_name}</strong><small>{item.gateway_name} / {item.model}</small></td><td>{t(capabilityNames[item.capability] ?? item.capability)}</td><td>{formatNumber(item.calls)}</td><td>{formatNumber(item.succeeded / item.calls, {style: 'percent', maximumFractionDigits: 1})}</td><td>{item.failed} / {item.unknown}</td><td>{duration(item.average_ms)} / {duration(item.p95_ms)}</td><td>{item.metered_calls ? `${formatNumber(item.input_tokens)} / ${formatNumber(item.output_tokens)}` : t('未知')}</td><td>{item.metered_calls} / {item.calls}</td><td>{amount(item.maximum_minor, item.currency)}</td></tr>)}</tbody></table></div> : <><div className="table-scroll"><table><thead><tr><th>{t('调用时间')}</th><th>{t('模型 / 网关')}</th><th>{t('创作功能')}</th><th>{t('状态')}</th><th>{t('耗时')}</th><th>{t('输入 / 输出 token')}</th><th>{t('HTTP 请求数')}</th><th>{t('调用上限')}</th></tr></thead><tbody>{report.items.map(item => <tr key={item.id}><td>{formatDate(item.started_at)}</td><td><strong>{item.model_name}</strong><small>{item.gateway_name} / {item.model}</small></td><td>{t(featureNames[item.feature_key] ?? capabilityNames[item.capability] ?? item.capability)}</td><td>{t(stateNames[item.state] ?? item.state)}{item.error_code && <small>{item.error_code}</small>}</td><td>{duration(item.duration_ms)}</td><td>{count(item.input_tokens)} / {count(item.output_tokens)}</td><td>{item.http_calls}</td><td>{amount(item.maximum_minor, item.currency)}</td></tr>)}</tbody></table></div><div className="media-actions"><button disabled={!offset} onClick={() => setOffset(Math.max(0, offset - 100))}>{t('上一页')}</button><span>{t('共 {count} 条调用', {count: report.total})}</span><button disabled={report.next_offset === null} onClick={() => {if (report.next_offset !== null) setOffset(report.next_offset)}}>{t('下一页')}</button></div></>)}
      <p className="drama-help">{t('调用上限用于预留，不能视为实际费用；缺失用量显示未知，各币种分别统计。')}</p>
    </section>
    <details className="panel legacy-model-config"><summary>{t('调用限制与费用账本')}</summary><GovernancePanel space={space} onDirtyChange={onDirtyChange} /></details>
  </div>
}
