import { useCallback, useEffect, useState } from 'react'
import { errorMessage, request, type Space } from '../api'
import { useI18n } from '../i18n'
import { capabilityNames, featureModelStatus, type AIGateway, type FeatureBinding, type FeatureDefinition, type GatewayModel, type ModelManagementReport } from '../gatewayModels'
import { minorAmountInput, parseMinorAmount } from '../money'
import { ModelConfigPanel } from './ModelConfigPanel'
import './modelManagement.css'

const blankGateway: AIGateway = {id: '', name: '', base_url: '', enabled: true, timeout_seconds: 120, version: 0, secret_status: 'not_configured'}
const blankModel: GatewayModel = {id: '', gateway_id: '', name: '', model: '', capabilities: ['text'], enabled: true, version: 0, max_output_tokens: 3000, json_output: true, video_seconds: 4, video_size: '1280x720', currency: 'USD', maximum_minor: 0, price_source: ''}
const currencyUnits = (currency: string) => ({JPY: 0, KRW: 0, KWD: 3, BHD: 3}[currency] ?? 2)

export function ModelManagementPanel({space, onDirtyChange}: {space: Space; onDirtyChange?: (dirty: boolean) => void}) {
  const {t} = useI18n()
  const [report, setReport] = useState<ModelManagementReport | null>(null)
  const [tab, setTab] = useState<'bindings' | 'gateways' | 'models'>('bindings')
  const [gateway, setGateway] = useState<AIGateway | null>(null)
  const [model, setModel] = useState<GatewayModel | null>(null)
  const [filter, setFilter] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const [dirtyRows, setDirtyRows] = useState<Record<string, boolean>>({})
  const dirty = busy || Object.values(dirtyRows).some(Boolean)
  useEffect(() => {onDirtyChange?.(dirty); return () => onDirtyChange?.(false)}, [dirty, onDirtyChange])
  const markDirty = useCallback((key: string, value: boolean) => setDirtyRows(old => old[key] === value ? old : {...old, [key]: value}), [])
  const query = `?${new URLSearchParams({space_id: space.id})}`
  useEffect(() => {
    const controller = new AbortController()
    void request<ModelManagementReport>(`/model-management${query}`, {signal: controller.signal}).then(value => {if (!controller.signal.aborted) {setReport(value); setError('')}}).catch(reason => {if (!controller.signal.aborted) setError(errorMessage(reason, '无法读取模型管理配置'))})
    return () => controller.abort()
  }, [query, revision])
  async function save(path: string, method: string, body: unknown) {
    setBusy(true); setError(''); setNotice('')
    try {const next = await request<ModelManagementReport>(`${path}${query}`, {method, headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)}); setReport(next); if (path.startsWith('/ai-gateways')) setGateway(null); if (path.startsWith('/gateway-models')) setModel(null); setNotice(t('配置已保存，新任务使用最新绑定。')); return true}
    catch (reason) {setError(errorMessage(reason, t('保存未确认，请刷新配置后核对。'))); return false}
    finally {setBusy(false)}
  }
  async function check(item: AIGateway) {
    setBusy(true); setError(''); setNotice('')
    try {const result = await request<{reachable: boolean; models: string[]; http_status?: number; error_code?: string}>(`/ai-gateways/${item.id}/check${query}`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({expected_version: item.version})}); setNotice(result.reachable ? t('连接成功，发现 {count} 个模型；生成能力尚未验证。', {count: result.models.length}) : t('模型目录检查失败，可手动添加模型。状态：{status}', {status: result.http_status ?? result.error_code ?? 'unknown'}))}
    catch (reason) {setError(errorMessage(reason, t('网关检查失败')))} finally {setBusy(false)}
  }
  return <section className="model-management">
    <div className="model-management-overview"><p>{t('先连接 AI 网关，再添加模型，最后为每个创作功能指定模型。')}</p>{report && <dl><div><dt>{t('AI 网关')}</dt><dd>{report.gateways.length}</dd></div><div><dt>{t('网关模型')}</dt><dd>{report.models.length}</dd></div><div><dt>{t('已绑定功能')}</dt><dd>{report.bindings.filter(item => item.enabled).length} / {report.features.length}</dd></div></dl>}</div>
    <div className="model-management-tabs" role="tablist" aria-label={t('模型管理')}>
      {([['bindings', '功能绑定'], ['gateways', 'AI 网关'], ['models', '网关模型']] as const).map(([key, name]) => <button key={key} id={`model-tab-${key}`} type="button" role="tab" aria-selected={tab === key} aria-controls={`model-panel-${key}`} onClick={() => setTab(key)}>{t(name)}</button>)}
    </div>
    {error && <p className="error" role="alert">{error} <button type="button" disabled={busy} onClick={() => setRevision(value => value + 1)}>{t('重新读取')}</button></p>}
    {notice && <p className="notice" role="status">{notice}</p>}
    {!report ? <p role="status">{t('正在读取模型配置…')}</p> : <>
      <div role="tabpanel" id="model-panel-bindings" aria-labelledby="model-tab-bindings" hidden={tab !== 'bindings'} className="panel model-management-panel">
        <div className="section-heading"><div><h2>{t('功能使用哪个模型')}</h2><p>{t('每个功能独立选择网关与模型，同一个模型可以被多个功能共用。')}</p></div></div>
        {!report.gateways.length && <div className="model-management-empty"><p>{t('尚未配置 AI 网关。先添加网关，再添加可用模型。')}</p><button onClick={() => {setTab('gateways'); setGateway({...blankGateway})}}>{t('添加 AI 网关')}</button></div>}
        <div className="table-scroll"><table className="feature-binding-table"><thead><tr><th>{t('创作功能')}</th><th>{t('AI 网关')}</th><th>{t('模型')}</th><th>{t('调用设置')}</th><th>{t('状态')}</th><th>{t('操作')}</th></tr></thead><tbody>{report.features.map(feature => {const binding = report.bindings.find(item => item.feature_key === feature.key); return <FeatureBindingRow key={`${feature.key}-${binding?.version ?? 0}`} feature={feature} binding={binding} report={report} busy={busy} onDirty={markDirty} onSave={value => save(`/model-features/${feature.key}`, 'PUT', {expected_version: binding?.version ?? 0, binding: value})} />})}</tbody></table></div>
      </div>
      <div role="tabpanel" id="model-panel-gateways" aria-labelledby="model-tab-gateways" hidden={tab !== 'gateways'} className="panel model-management-panel">
        <div className="section-heading"><div><h2>{t('AI 网关')}</h2><p>{t('每个网关独立保存服务地址和加密凭据，其下模型共用连接。')}</p></div><button disabled={busy} onClick={() => setGateway({...blankGateway})}>{t('添加 AI 网关')}</button></div>
        {!report.secret_storage_available && <p className="notice">{t('部署尚未配置凭据加密密钥，可以保存连接信息，配置密钥后再录入 API Key。')}</p>}
        {!report.gateways.length ? <p className="model-management-empty">{t('尚无 AI 网关。')}</p> : <div className="table-scroll"><table><thead><tr><th>{t('网关名称')}</th><th>{t('服务地址')}</th><th>{t('凭据')}</th><th>{t('模型数')}</th><th>{t('状态')}</th><th>{t('操作')}</th></tr></thead><tbody>{report.gateways.map(item => <tr key={item.id}><td><strong>{item.name}</strong><small>OpenAI compatible</small></td><td className="gateway-address">{item.base_url}</td><td>{t(item.secret_status === 'encrypted' ? '已加密' : item.secret_status === 'revoked' ? '已撤销' : '未配置')}</td><td>{report.models.filter(value => value.gateway_id === item.id).length}</td><td>{t(item.enabled ? '已启用' : '已停用')}</td><td><div className="media-actions"><button disabled={busy} onClick={() => setGateway({...item})}>{t('编辑')}</button><button disabled={busy || item.secret_status !== 'encrypted'} onClick={() => void check(item)}>{t('检查连接')}</button><button disabled={busy} onClick={() => {setTab('models'); setFilter(item.id); setModel({...blankModel, gateway_id: item.id})}}>{t('添加模型')}</button></div></td></tr>)}</tbody></table></div>}
        {gateway && <GatewayForm key={`${gateway.id}-${gateway.version}`} value={gateway} secretStorage={report.secret_storage_available} busy={busy} onDirty={markDirty} onCancel={() => setGateway(null)} onSave={(value, credential) => save(value.id ? `/ai-gateways/${value.id}` : '/ai-gateways', value.id ? 'PATCH' : 'POST', {expected_version: gateway.version, gateway: value, credential})} />}
      </div>
      <div role="tabpanel" id="model-panel-models" aria-labelledby="model-tab-models" hidden={tab !== 'models'} className="panel model-management-panel">
        <div className="section-heading"><div><h2>{t('网关模型')}</h2><p>{t('填写网关实际接受的模型 ID，并声明已支持的能力。')}</p></div><button disabled={busy || !report.gateways.length} onClick={() => setModel({...blankModel, gateway_id: filter || report.gateways[0]?.id || ''})}>{t('添加模型')}</button></div>
        <label className="model-gateway-filter">{t('按网关筛选')}<select value={filter} onChange={event => setFilter(event.target.value)}><option value="">{t('全部网关')}</option>{report.gateways.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        {!report.models.length ? <p className="model-management-empty">{t('尚无网关模型。')}</p> : <div className="table-scroll"><table><thead><tr><th>{t('模型')}</th><th>{t('AI 网关')}</th><th>{t('模型能力')}</th><th>{t('功能引用')}</th><th>{t('状态')}</th><th>{t('操作')}</th></tr></thead><tbody>{report.models.filter(item => !filter || item.gateway_id === filter).map(item => <tr key={item.id}><td><strong>{item.name}</strong><small>{item.model}</small></td><td>{report.gateways.find(value => value.id === item.gateway_id)?.name}</td><td><div className="model-capability-tags">{item.capabilities.map(capability => <span key={capability}>{t(capabilityNames[capability] ?? capability)}</span>)}</div></td><td>{report.bindings.filter(value => value.model_config_id === item.id).map(value => t(report.features.find(feature => feature.key === value.feature_key)?.name ?? value.feature_key)).join(' / ') || '—'}</td><td>{t(item.enabled ? '已启用' : '已停用')}</td><td><button disabled={busy} onClick={() => setModel({...item})}>{t('编辑')}</button></td></tr>)}</tbody></table></div>}
        {model && <GatewayModelForm key={`${model.id}-${model.version}`} value={model} gateways={report.gateways} busy={busy} onDirty={markDirty} onCancel={() => setModel(null)} onSave={value => save(value.id ? `/gateway-models/${value.id}` : '/gateway-models', value.id ? 'PATCH' : 'POST', {expected_version: model.version, model: value})} />}
      </div>
      {<details className="panel legacy-model-config"><summary>{t('已有供应商配置（兼容保留）')}</summary><ModelConfigPanel space={space} onDirtyChange={value => markDirty('legacy', value)} /></details>}
    </>}
  </section>
}

function FeatureBindingRow({feature, binding, report, busy, onDirty, onSave}: {feature: FeatureDefinition; binding?: FeatureBinding; report: ModelManagementReport; busy: boolean; onDirty: (key: string, value: boolean) => void; onSave: (value: FeatureBinding) => Promise<boolean>}) {
  const {t} = useI18n()
  const initial = binding ?? {feature_key: feature.key, model_config_id: '', enabled: true, allow_override: true, version: 0, voice: ''}
  const [draft, setDraft] = useState<FeatureBinding>(initial)
  const [gatewayID, setGatewayID] = useState(report.models.find(item => item.id === initial.model_config_id)?.gateway_id ?? '')
  const changed = JSON.stringify(draft) !== JSON.stringify(initial) || gatewayID !== (report.models.find(item => item.id === initial.model_config_id)?.gateway_id ?? '')
  useEffect(() => {onDirty(feature.key, changed); return () => onDirty(feature.key, false)}, [changed, feature.key, onDirty])
  const models = report.models.filter(item => item.gateway_id === gatewayID && item.capabilities.includes(feature.capability))
  const status = !draft.enabled ? '已停用' : featureModelStatus(report, draft.model_config_id, feature.capability)
  return <tr><td><strong>{t(feature.name)}</strong><small>{t(capabilityNames[feature.capability] ?? feature.capability)}</small></td>
    <td><select aria-label={t('{feature} AI 网关', {feature: t(feature.name)})} value={gatewayID} disabled={busy} onChange={event => {setGatewayID(event.target.value); setDraft({...draft, model_config_id: ''})}}><option value="">{t('选择网关')}</option>{report.gateways.map(item => <option key={item.id} value={item.id} disabled={!item.enabled}>{item.name}{!item.enabled ? ` (${t('已停用')})` : ''}</option>)}</select></td>
    <td><select aria-label={t('{feature} 模型', {feature: t(feature.name)})} value={draft.model_config_id} disabled={busy || !gatewayID} onChange={event => setDraft({...draft, model_config_id: event.target.value})}><option value="">{t('选择模型')}</option>{models.map(item => <option key={item.id} value={item.id} disabled={!item.enabled}>{item.name} / {item.model}</option>)}</select>{gatewayID && !models.length && <small>{t('此网关没有满足能力的模型')}</small>}</td>
    <td><div className="feature-binding-options"><label><input type="checkbox" checked={draft.enabled} disabled={busy} onChange={event => setDraft({...draft, enabled: event.target.checked})} />{t('启用')}</label><label><input type="checkbox" checked={draft.allow_override} disabled={busy} onChange={event => setDraft({...draft, allow_override: event.target.checked})} />{t('允许本次切换')}</label>{feature.capability === 'speech' && <input aria-label={t('默认音色')} placeholder={t('默认音色')} value={draft.voice ?? ''} maxLength={128} disabled={busy} onChange={event => setDraft({...draft, voice: event.target.value})} />}</div></td>
    <td><span className={`model-readiness ${status === '可执行' ? 'is-ready' : ''}`}>{t(status)}</span></td><td><button disabled={busy || !draft.model_config_id || !changed} onClick={() => void onSave(draft)}>{t('保存绑定')}</button></td>
  </tr>
}

function GatewayForm({value, secretStorage, busy, onDirty, onCancel, onSave}: {value: AIGateway; secretStorage: boolean; busy: boolean; onDirty: (key: string, value: boolean) => void; onCancel: () => void; onSave: (value: AIGateway, credential: {action: string; secret?: string}) => Promise<boolean>}) {
  const {t} = useI18n()
  const [draft, setDraft] = useState(value)
  const [secret, setSecret] = useState('')
  const [action, setAction] = useState('keep')
  const dirty = JSON.stringify(value) !== JSON.stringify(draft) || !!secret || action !== 'keep'
  useEffect(() => {onDirty('gateway-form', dirty); return () => onDirty('gateway-form', false)}, [dirty, onDirty])
  async function submit() {const credential = action === 'set' ? {action, secret} : {action}; setSecret(''); await onSave(draft, credential)}
  return <form className="model-editor-form" onSubmit={event => {event.preventDefault(); void submit()}}><fieldset disabled={busy}><legend>{t(value.id ? '编辑 AI 网关' : '添加 AI 网关')}</legend><div className="model-editor-grid">
    <label>{t('网关名称')}<input required maxLength={80} value={draft.name} onChange={event => setDraft({...draft, name: event.target.value})} /></label>
    <label>{t('服务地址')}<input required type="url" placeholder="https://gateway.example.com/v1" maxLength={2048} value={draft.base_url} onChange={event => setDraft({...draft, base_url: event.target.value})} /></label>
    <label>{t('请求超时（秒）')}<input required type="number" min={5} max={600} value={draft.timeout_seconds} onChange={event => setDraft({...draft, timeout_seconds: Number(event.target.value)})} /></label>
    <label>{t('凭据操作')}<select value={action} onChange={event => {setAction(event.target.value); setSecret('')}}><option value="keep">{t('保留现有凭据')}</option><option value="set" disabled={!secretStorage}>{t('设置 API Key')}</option><option value="revoke">{t('撤销凭据并停用')}</option></select></label>
    {action === 'set' && <label>{t('新的 API Key')}<input type="password" autoComplete="new-password" required value={secret} onChange={event => setSecret(event.target.value)} /></label>}
  </div><p className="drama-help">{t('地址包含 API 前缀，例如 /v1。更换地址需重新设置凭据；内网地址需部署方授权。')}</p><label className="model-check-label"><input type="checkbox" checked={draft.enabled} onChange={event => setDraft({...draft, enabled: event.target.checked})} />{t('启用此网关')}</label><div className="media-actions"><button className="primary" type="submit">{t('保存网关')}</button><button type="button" onClick={onCancel}>{t('取消')}</button></div></fieldset></form>
}

function GatewayModelForm({value, gateways, busy, onDirty, onCancel, onSave}: {value: GatewayModel; gateways: AIGateway[]; busy: boolean; onDirty: (key: string, value: boolean) => void; onCancel: () => void; onSave: (value: GatewayModel) => Promise<boolean>}) {
  const {t} = useI18n()
  const [draft, setDraft] = useState(value)
  const [maximum, setMaximum] = useState(minorAmountInput(value.maximum_minor, currencyUnits(value.currency)))
  const [error, setError] = useState('')
  const dirty = JSON.stringify(value) !== JSON.stringify(draft) || maximum !== minorAmountInput(value.maximum_minor, currencyUnits(value.currency))
  useEffect(() => {onDirty('model-form', dirty); return () => onDirty('model-form', false)}, [dirty, onDirty])
  function submit() {const amount = parseMinorAmount(maximum, currencyUnits(draft.currency)); if (amount === null) {setError(t('请输入有效金额')); return} setError(''); void onSave({...draft, maximum_minor: amount})}
  return <form className="model-editor-form" onSubmit={event => {event.preventDefault(); submit()}}><fieldset disabled={busy}><legend>{t(value.id ? '编辑网关模型' : '添加网关模型')}</legend>{error && <p className="error" role="alert">{error}</p>}<div className="model-editor-grid">
    <label>{t('所属网关')}<select required disabled={!!value.id} value={draft.gateway_id} onChange={event => setDraft({...draft, gateway_id: event.target.value})}><option value="">{t('选择网关')}</option>{gateways.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
    <label>{t('模型显示名称')}<input required maxLength={80} value={draft.name} onChange={event => setDraft({...draft, name: event.target.value})} /></label>
    <label>{t('上游模型 ID')}<input required maxLength={200} placeholder="vendor/model-name" value={draft.model} onChange={event => setDraft({...draft, model: event.target.value})} /></label>
    <label>{t('最大输出 token')}<input required type="number" min={1} max={6000} value={draft.max_output_tokens} onChange={event => setDraft({...draft, max_output_tokens: Number(event.target.value)})} /></label>
  </div><fieldset className="model-capabilities"><legend>{t('模型能力')}</legend>{Object.entries(capabilityNames).map(([key, label]) => <label key={key}><input type="checkbox" checked={draft.capabilities.includes(key)} onChange={event => setDraft({...draft, capabilities: event.target.checked ? [...draft.capabilities, key] : draft.capabilities.filter(item => item !== key)})} />{t(label)}</label>)}</fieldset>
  <label className="model-check-label"><input type="checkbox" checked={draft.json_output} onChange={event => setDraft({...draft, json_output: event.target.checked})} />{t('文本请求使用 JSON 输出模式')}</label>
  {draft.capabilities.includes('video') && <div className="model-editor-grid"><label>{t('视频时长（秒）')}<select value={draft.video_seconds} onChange={event => setDraft({...draft, video_seconds: Number(event.target.value)})}>{[4, 8, 12].map(item => <option key={item}>{item}</option>)}</select></label><label>{t('视频分辨率')}<select value={draft.video_size} onChange={event => setDraft({...draft, video_size: event.target.value})}><option>1280x720</option><option>720x1280</option></select></label><p className="drama-help">{t('视频使用兼容 /videos 的创建、查询和内容接口。')}</p></div>}
  <div className="model-editor-grid"><label>{t('费用币种')}<select value={draft.currency} onChange={event => {setDraft({...draft, currency: event.target.value}); setMaximum('0')}}>{['USD', 'CNY', 'EUR', 'SGD', 'JPY', 'HKD'].map(item => <option key={item}>{item}</option>)}</select></label><label>{t('单次费用上限')}<input required inputMode="decimal" value={maximum} onChange={event => setMaximum(event.target.value)} /></label><label>{t('费用上限依据')}<input maxLength={256} placeholder={t('填写价格来源或内部调用限额说明')} value={draft.price_source} onChange={event => setDraft({...draft, price_source: event.target.value})} /></label></div>
  <p className="drama-help">{t('这是管理员设置的保守预留上限，实际费用以账单为准。留为零时无法提交真实调用。')}</p><label className="model-check-label"><input type="checkbox" checked={draft.enabled} onChange={event => setDraft({...draft, enabled: event.target.checked})} />{t('启用此模型')}</label><div className="media-actions"><button className="primary" type="submit" disabled={!draft.capabilities.length}>{t('保存模型')}</button><button type="button" onClick={onCancel}>{t('取消')}</button></div></fieldset></form>
}
