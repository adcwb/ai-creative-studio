import { useEffect, useState } from 'react'
import { errorMessage, request, type Space } from '../api'
import { useI18n } from '../i18n'
import { minorAmountInput, parseMinorAmount } from '../money'
interface Entry {id: string; provider: string; model: string; currency: string; reserved_minor: number; actual_minor: number; state: string; evidence_ref?: string; updated_at: string}
interface BudgetChange {actor: string; currency: string; before_minor: number; after_minor: number; at: string}
interface Report {currency: string; minor_units: number; limit_minor: number; exposure_minor: number; available_minor: number; entries: Entry[]; total_entries: number; next_offset: number | null; budget_changes: BudgetChange[]; live_execution_enabled: boolean; fx_available: boolean}
const stateLabels: Record<string, string> = {reserved: '已预留', unknown: '结果不明', settled: '已结算', released: '已释放'}
const currencies = ['USD', 'EUR', 'GBP', 'CNY', 'JPY', 'KRW', 'KWD', 'BHD', 'CAD', 'AUD', 'CHF', 'HKD', 'SGD']
export function GovernancePanel({space}: {space: Space}) {
  const {t, formatCurrency, formatDate} = useI18n()
  const [currency, setCurrency] = useState('USD')
  const [offset, setOffset] = useState(0)
  const [report, setReport] = useState<Report | null>(null)
  const [limit, setLimit] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    const params = new URLSearchParams({space_id: space.id, currency, offset: String(offset)})
    void request<Report>(`/governance?${params}`, {signal: controller.signal}).then(value => {if (!controller.signal.aborted) {setReport(value); setLimit(minorAmountInput(value.limit_minor, value.minor_units)); setError('')}}).catch(reason => {if (!controller.signal.aborted) {setReport(null); setError(errorMessage(reason, t('无法读取费用记录')))}})
    return () => controller.abort()
    // Locale changes format existing data without resetting an in-progress amount edit.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [space.id, currency, offset, revision])
  async function saveBudget() {
    if (!report || busy) return
    const minor = parseMinorAmount(limit, report.minor_units)
    if (minor === null) {setError(t('请输入符合币种精度的非负金额，不支持指数或超过安全范围的数值。')); return}
    setBusy(true); setError(''); setNotice('')
    try {await request(`/governance/budget?${new URLSearchParams({space_id: space.id})}`, {method: 'PUT', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({currency, limit_minor: minor, expected_limit_minor: report.limit_minor})}); setRevision(value => value + 1); setNotice(t('预算已保存。此操作不会启用真实模型调用。'))} catch (reason) {setError(errorMessage(reason, t('预算保存失败；请重新读取最新预算后再提交。')))} finally {setBusy(false)}
  }
  const amount = (value: number) => report && Number.isSafeInteger(value) ? formatCurrency(value / 10 ** report.minor_units, report.currency) : '—'
  return <details className="panel organization-panel governance-panel"><summary>{t('预算与费用记录')}</summary><p>{t('账本按原币种分别记录。语言切换不改变币种；无可靠汇率时不显示换算总额。')}</p><label>{t('账本币种')}<select value={currency} disabled={busy} onChange={event => {setCurrency(event.target.value); setOffset(0); setReport(null); setNotice('')}}>{currencies.map(item => <option key={item}>{item}</option>)}</select></label>{error && <p className="error" role="alert">{error}<button onClick={() => setRevision(value => value + 1)}>{t('重新读取')}</button></p>}{notice && <p className="notice" role="status">{notice}</p>}{report && <><p>{t('预算上限：{limit} · 已占用：{exposure} · 可用：{available}', {limit: amount(report.limit_minor), exposure: amount(report.exposure_minor), available: amount(report.available_minor)})}</p><p className="drama-help">{report.live_execution_enabled ? t('真实执行已启用，提交前仍须确认具体模型与预算授权。') : t('真实执行仍被禁用；管理预算不会发送供应商请求。')}{!report.fx_available && ` ${t('汇率不可用；各币种金额不合并。')}`}</p>{space.role === 'owner' && <form onSubmit={event => {event.preventDefault(); void saveBudget()}}><label>{t('新的预算上限（{currency}）', {currency})}<input inputMode="decimal" value={limit} disabled={busy} onChange={event => setLimit(event.target.value)} /></label><button disabled={busy} type="submit">{t('保存预算')}</button></form>}<h3>{t('费用流水')}</h3><p className="drama-help">{t('预留与结果不明金额继续占用预算；取消或失败不表示供应商没有计费。')}</p>{report.entries.length === 0 ? <p>{t('此币种尚无费用记录。')}</p> : <div className="table-scroll"><table><thead><tr><th>{t('模型')}</th><th>{t('状态')}</th><th>{t('预留金额')}</th><th>{t('已确认金额')}</th><th>{t('更新时间')}</th></tr></thead><tbody>{report.entries.map(entry => <tr key={entry.id}><td>{entry.provider} / {entry.model}</td><td>{t(stateLabels[entry.state] ?? entry.state)}</td><td>{amount(entry.reserved_minor)}</td><td>{entry.state === 'settled' ? amount(entry.actual_minor) : '—'}</td><td>{formatDate(entry.updated_at)}</td></tr>)}</tbody></table></div>}<div className="media-actions"><button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 100))}>{t('上一页')}</button><button disabled={report.next_offset === null} onClick={() => {if (report.next_offset !== null) setOffset(report.next_offset)}}>{t('下一页')}</button></div><h3>{t('预算变更审计')}</h3>{report.budget_changes.length === 0 ? <p>{t('暂无预算变更。')}</p> : <ul>{report.budget_changes.map((change, index) => <li key={`${change.at}-${index}`}>{formatDate(change.at)} · {change.actor} · {amount(change.before_minor)} → {amount(change.after_minor)}</li>)}</ul>}</>}</details>
}
