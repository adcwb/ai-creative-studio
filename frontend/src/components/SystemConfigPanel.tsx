import { useCallback, useEffect, useState } from 'react'
import { errorMessage, request } from '../api'
import { useI18n } from '../i18n'
import './systemConfig.css'

interface ConfigField {key: string; group: string; secret: boolean; read_only: boolean; value: string; configured: boolean; source: string; pending: boolean}
interface ConfigReport {version: string; fields: ConfigField[]; restart_required: boolean}
const groups: Record<string, string> = {service: '服务与访问', authentication: '认证与登录', storage: '数据库与存储', gateway: '网关与执行', providers: '模型与本地服务', references: '参考素材存储', tracing: '追踪与诊断'}

export function SystemConfigPanel({onDirtyChange}: {onDirtyChange?: (dirty: boolean) => void}) {
  const {t} = useI18n()
  const [report, setReport] = useState<ConfigReport | null>(null)
  const [draft, setDraft] = useState<Record<string, string | null>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [revision, setRevision] = useState(0)
  const dirty = busy || Object.keys(draft).length > 0
  useEffect(() => {onDirtyChange?.(dirty); return () => onDirtyChange?.(false)}, [dirty, onDirtyChange])
  useEffect(() => {
    const controller = new AbortController()
    void request<ConfigReport>('/system-config', {signal: controller.signal}).then(value => {if (!controller.signal.aborted) {setReport(value); setDraft({}); setError('')}}).catch(reason => {if (!controller.signal.aborted) setError(errorMessage(reason, t('无法读取后端配置')))})
    return () => controller.abort()
  }, [revision, t])
  const change = useCallback((key: string, value: string | null) => setDraft(old => ({...old, [key]: value})), [])
  async function save() {
    if (!report || busy) return
    setBusy(true); setError(''); setNotice('')
    const changes = {...draft}
    setDraft(old => Object.fromEntries(Object.entries(old).filter(([key]) => !report.fields.some(field => field.key === key && field.secret))))
    try {
      const value = await request<ConfigReport>('/system-config', {method: 'PATCH', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({expected_version: report.version, changes})})
      setReport(value); setDraft({}); setNotice(t('后端配置已保存，请重启 API 与 worker 后生效。'))
    } catch (reason) {setError(errorMessage(reason, t('配置保存未确认，请重新读取后核对')))} finally {setBusy(false)}
  }
  return <section className="system-config-panel" aria-labelledby="system-config-title">
    <div className="section-heading"><div><h2 id="system-config-title">{t('后端配置')}</h2><p>{t('此处修改全局配置，保存后需重启 API 与 worker。敏感值仅可替换或清除，不会回显。')}</p></div><button type="button" disabled={busy || Object.keys(draft).length > 0} onClick={() => setRevision(value => value + 1)}>{t('重新读取')}</button></div>
    {error && <p className="error" role="alert">{error} <button type="button" disabled={busy} onClick={() => setRevision(value => value + 1)}>{t('重新读取')}</button></p>}
    {notice && <p className="notice" role="status">{notice}</p>}
    {!report ? <p role="status">{t('正在读取后端配置…')}</p> : <form onSubmit={event => {event.preventDefault(); void save()}}><fieldset disabled={busy}>
      {report.restart_required && <p className="notice">{t('存在尚未生效的配置，请重启相关进程。')}</p>}
      {Object.entries(groups).map(([group, name]) => <details key={group} open={group === 'service' || undefined}><summary>{t(name)}</summary><div className="system-config-fields">{report.fields.filter(field => field.group === group).map(field => {
        const changed = field.key in draft
        const value = changed ? draft[field.key] ?? '' : field.value
        return <div className="system-config-field" key={field.key}><label htmlFor={`config-${field.key}`}><code>{field.key}</code>{field.pending && <span>{t('待重启')}</span>}</label>
          <input aria-label={field.key} id={`config-${field.key}`} type={field.secret ? 'password' : 'text'} autoComplete={field.secret ? 'new-password' : 'off'} spellCheck={false} disabled={field.read_only || changed && draft[field.key] === null} maxLength={8192} placeholder={field.secret ? t(field.configured ? '已配置；留空保留现有值' : '未配置') : undefined} value={value} onChange={event => {if (field.secret && !event.target.value) setDraft(old => {const next = {...old}; delete next[field.key]; return next}); else change(field.key, event.target.value)}} />
          <div className="config-field-footer"><small>{t(field.read_only ? '由部署环境管理' : field.source === 'file' ? '配置文件' : '环境变量')}{changed && ` · ${t(draft[field.key] === null ? '将恢复环境值' : '未保存')}`}</small>{!field.read_only && <div>{field.secret && <button type="button" onClick={() => change(field.key, '')}>{t('清除值')}</button>}<button type="button" onClick={() => change(field.key, null)}>{t('恢复环境值')}</button>{changed && <button type="button" onClick={() => setDraft(old => {const next = {...old}; delete next[field.key]; return next})}>{t('撤销修改')}</button>}</div>}</div>
        </div>
      })}</div></details>)}
      <div className="system-config-save"><p>{t('配置文件优先于环境变量。恢复环境值会删除该项的文件覆盖。')}</p><button type="submit" className="primary" disabled={Object.keys(draft).length === 0}>{t('保存后端配置')}</button></div>
    </fieldset></form>}
  </section>
}
