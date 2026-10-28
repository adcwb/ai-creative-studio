import { useEffect, useRef, useState } from 'react'
import { ApiError, errorMessage, request } from '../api'
import { useI18n } from '../i18n'
import { safeDouyinHandoff } from '../socialDouyin'
import type { SocialSource, SocialState } from '../social'
interface Prepared {job_id: string; handoff_url: string; expires_at: string; version: number}
interface Attempt {path: string; body: string; key: string; kind: 'prepare' | 'ack' | 'revoke'}
export function DouyinHandoffPanel({projectId, space, state, sources, reload}: {projectId: string; space: string; state: SocialState; sources: SocialSource[]; reload: () => void}) {
  const {t, formatDate} = useI18n()
  const [ready, setReady] = useState(false)
  const [source, setSource] = useState('')
  const [title, setTitle] = useState('')
  const [consent, setConsent] = useState(false)
  const [rights, setRights] = useState(false)
  const [prepared, setPrepared] = useState<Prepared | null>(null)
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [error, setError] = useState('')
  const [clock, setClock] = useState(Date.now())
  const attempt = useRef<Attempt | null>(null)
  const active = useRef(false)
  const path = `/projects/${encodeURIComponent(projectId)}/social/douyin`
  const query = new URLSearchParams({space_id: space}).toString()
  useEffect(() => {
    const controller = new AbortController()
    void request<{ready: boolean}>(`${path}?${query}`, {signal: controller.signal}).then(value => {if (!controller.signal.aborted) setReady(value.ready)}).catch(() => {if (!controller.signal.aborted) setReady(false)})
    return () => controller.abort()
  }, [path, query, state.version])
  useEffect(() => {const timer = setInterval(() => setClock(Date.now()), 1000); return () => clearInterval(timer)}, [])
  async function execute(value: Attempt) {
    if (active.current) return
    active.current = true; setBusy(true); setError(''); attempt.current = value
    try {
      const result = await request<Prepared>(`${value.path}?${query}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': value.key}, body: value.body, keepalive: value.kind === 'ack'})
      if (value.kind === 'prepare') {
        if (!safeDouyinHandoff(result.handoff_url)) {setError(t('分享交接地址无效，未打开外部应用。')); setPrepared(null)}
        else setPrepared(result)
      } else if (value.kind === 'revoke' && prepared?.job_id === value.path.split('/').at(-2)) setPrepared(null)
      attempt.current = null; setUncertain(false); reload()
    } catch (failure) {
      const unknown = !(failure instanceof ApiError) || failure.status >= 500
      setUncertain(unknown); if (!unknown) attempt.current = null
      setError(errorMessage(failure, t('交接操作未确认，请刷新记录或使用原请求核对。')))
    } finally {active.current = false; setBusy(false)}
  }
  function prepare() {
    const selected = sources.find(item => `${item.kind}:${item.id}` === source)
    if (!selected || !consent || !rights || !ready || uncertain) return
    void execute({path: `${path}/prepare`, key: crypto.randomUUID(), kind: 'prepare', body: JSON.stringify({expected_version: state.version, ...(selected.kind === 'asset' ? {asset_id: selected.id} : {export_id: selected.id}), title, confirmed: true, rights_confirmed: true})})
  }
  function change(job: string, kind: 'ack' | 'revoke') {
    if (uncertain) return
    void execute({path: `${path}/${encodeURIComponent(job)}/${kind}`, key: crypto.randomUUID(), kind, body: JSON.stringify({expected_version: state.version, confirmed: true})})
  }
  const valid = prepared && Date.parse(prepared.expires_at) > clock && safeDouyinHandoff(prepared.handoff_url)
  return <section className="social-handoff" aria-label={t('抖音 App 分享交接')}>
    <h3>{t('抖音 App 分享交接')}</h3>
    <p>{t('仅已获批 H5 应用可用。用户在抖音内选择账号并最终确认；交接记录只表示请求打开 App，不证明 App 已打开或内容已发布。')}</p>
    {!ready && <p role="status">{t('抖音应用审批、公开 HTTPS 媒体入口或短期 ticket 未就绪，当前仅可导出素材。')}</p>}
    <p className="drama-help">{t('分享链接最多有效5分钟，持有链接者可读取这份素材；撤销只阻止后续下载，无法收回已下载副本。图片最大20MiB，视频最大128MiB。')}</p>
    {error && <p className="error" role="alert">{error}</p>}
    <fieldset disabled={!ready || busy || uncertain}>
      <label>{t('抖音交接素材')}<select value={source} onChange={event => {setSource(event.target.value); setConsent(false); setRights(false); setPrepared(null)}}><option value="">{t('选择图片、视频或已完成成片')}</option>{sources.map(item => <option key={`${item.kind}:${item.id}`} value={`${item.kind}:${item.id}`}>{item.name}</option>)}</select></label>
      <label>{t('抖音分享标题（可选）')}<input maxLength={90} value={title} onChange={event => {setTitle(event.target.value); setConsent(false); setPrepared(null)}} /></label>
      <label><input type="checkbox" checked={rights} onChange={event => {setRights(event.target.checked); setPrepared(null)}} />{t('我拥有素材及相关人物、音乐的分享权利。')}</label>
      <label><input type="checkbox" checked={consent} onChange={event => {setConsent(event.target.checked); setPrepared(null)}} />{t('我同意为此素材创建短期公开读取链接，并自行在抖音确认目标账号和发布。')}</label>
      <button disabled={!source || !consent || !rights} onClick={prepare}>{t('准备抖音短期分享链接')}</button>
    </fieldset>
    {valid && <p><a href={prepared.handoff_url} referrerPolicy="no-referrer" onClick={event => {if (busy || uncertain || !ready) {event.preventDefault(); return} change(prepared.job_id, 'ack')}}>{t('打开抖音并在 App 内确认')}</a> · {t('链接到期时间：')}{formatDate(prepared.expires_at)}</p>}
    {prepared && !valid && <p role="status">{t('交接链接已过期，请重新确认素材后创建新链接。')}</p>}
    {uncertain && <button disabled={busy || !attempt.current} onClick={() => {if (attempt.current) void execute(attempt.current)}}>{t('使用原请求核对交接结果')}</button>}
    {state.jobs.filter(job => job.platform === 'douyin' && job.mode === 'handoff' && job.status !== 'cancelled').map(job => <p key={job.id}><code>{job.id}</code> <button disabled={busy || uncertain} onClick={() => change(job.id, 'revoke')}>{t('撤销此抖音素材链接')}</button></p>)}
  </section>
}
