import { useEffect, useState } from 'react'
import { errorMessage, request, type Space } from '../api'
import { useI18n } from '../i18n'
import { formatMinorCurrency } from '../money'
interface Entry {id: string; provider: string; model: string; currency: string; reserved_minor: number; actual_minor: number; state: string; evidence_ref?: string; updated_at: string}
interface BudgetChange {actor: string; currency: string; before_minor: number; after_minor: number; at: string}
interface Report {currency: string; minor_units: number; limit_minor: number; exposure_minor: number; available_minor: number; entries: Entry[]; total_entries: number; next_offset: number | null; budget_changes: BudgetChange[]; live_execution_enabled: boolean; fx_available: boolean}
const stateLabels: Record<string, string> = {reserved: '已预留', unknown: '结果不明', settled: '已结算', released: '已释放'}
const currencies = ['USD', 'EUR', 'GBP', 'CNY', 'JPY', 'KRW', 'KWD', 'BHD', 'CAD', 'AUD', 'CHF', 'HKD', 'SGD']
export function GovernancePanel({space}: {space: Space; onDirtyChange?: (dirty: boolean) => void}) {
  const {t, locale, formatDate} = useI18n()
  const [currency, setCurrency] = useState('USD')
  const [offset, setOffset] = useState(0)
  const [report, setReport] = useState<Report | null>(null)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    const params = new URLSearchParams({space_id: space.id, currency, offset: String(offset)})
    void request<Report>(`/governance?${params}`, {signal: controller.signal}).then(value => {if (!controller.signal.aborted) {setReport(value); setError('')}}).catch(reason => {if (!controller.signal.aborted) {setReport(null); setError(errorMessage(reason, t('无法读取费用记录')))}})
    return () => controller.abort()
  }, [space.id, currency, offset, revision, t])
  const amount = (value: number) => report && Number.isSafeInteger(value) ? formatMinorCurrency(value, report.currency, report.minor_units, locale) : '—'
  return <section className="panel organization-panel governance-panel"><h2>{t('历史费用记录')}</h2><p>{t('成本管理已移除。历史预算和费用记录仅供查阅，不限制新的模型调用；实际费用以供应商账单为准。')}</p><label>{t('账本币种')}<select value={currency} onChange={event => {setCurrency(event.target.value); setOffset(0); setReport(null)}}>{currencies.map(item => <option key={item}>{item}</option>)}</select></label>
    {error && <p className="error" role="alert">{error}<button onClick={() => setRevision(value => value + 1)}>{t('重新读取')}</button></p>}
    {report && <><p>{t('历史预算：{limit} · 历史记录金额：{exposure}', {limit: amount(report.limit_minor), exposure: amount(report.exposure_minor)})}</p><h3>{t('费用流水')}</h3>
      {report.entries.length === 0 ? <p>{t('此币种尚无费用记录。')}</p> : <div className="table-scroll"><table><thead><tr><th>{t('模型')}</th><th>{t('状态')}</th><th>{t('预留金额')}</th><th>{t('已确认金额')}</th><th>{t('更新时间')}</th></tr></thead><tbody>{report.entries.map(entry => <tr key={entry.id}><td>{entry.provider} / {entry.model}</td><td>{t(stateLabels[entry.state] ?? entry.state)}</td><td>{amount(entry.reserved_minor)}</td><td>{entry.state === 'settled' ? amount(entry.actual_minor) : '—'}</td><td>{formatDate(entry.updated_at)}</td></tr>)}</tbody></table></div>}
      <div className="media-actions"><button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 100))}>{t('上一页')}</button><button disabled={report.next_offset === null} onClick={() => {if (report.next_offset !== null) setOffset(report.next_offset)}}>{t('下一页')}</button></div><h3>{t('预算变更审计')}</h3>{report.budget_changes.length === 0 ? <p>{t('暂无预算变更。')}</p> : <ul>{report.budget_changes.map((change, index) => <li key={`${change.at}-${index}`}>{formatDate(change.at)} · {change.actor} · {amount(change.before_minor)} → {amount(change.after_minor)}</li>)}</ul>}
    </>}
  </section>
}
