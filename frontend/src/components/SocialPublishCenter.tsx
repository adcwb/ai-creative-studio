import { useEffect, useRef, useState } from 'react'
import { ApiError, baseURL, errorMessage, request } from '../api'
import { useI18n } from '../i18n'
import { socialDownloadPath, socialPlatformLabels, socialSources, socialStatusLabels, type SocialMedia, type SocialPlatform, type SocialState } from '../social'
import './social.css'
import { SocialTikTokPanel, SocialJobActions } from './SocialTikTokPanel'
import { DouyinHandoffPanel } from './DouyinHandoffPanel'

export function SocialPublishCenter({projectId, space}: {projectId: string; space: string}) {
  const {t, formatDate} = useI18n()
  const [state, setState] = useState<SocialState | null>(null)
  const [media, setMedia] = useState<SocialMedia>({assets: [], exports: []})
  const [platform, setPlatform] = useState<SocialPlatform>('tiktok')
  const [source, setSource] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [revision, setRevision] = useState(0)
  const inFlight = useRef(false)
  const pending = useRef<{body: string; key: string} | null>(null)
  const query = new URLSearchParams({space_id: space}).toString()
  const path = `/projects/${encodeURIComponent(projectId)}`
  useEffect(() => {
    const controller = new AbortController()
    void Promise.all([request<SocialState>(`${path}/social?${query}`, {signal: controller.signal}), request<SocialMedia>(`${path}/media?${query}`, {signal: controller.signal})]).then(([result, assets]) => {
      if (controller.signal.aborted) return
      setState(result); setMedia(assets); setError('')
    }).catch(reason => {if (!controller.signal.aborted) setError(errorMessage(reason, t('发布中心读取失败')))})
    return () => controller.abort()
    // Reload explicitly; language changes must not reset the user's selection.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path, query, revision])
  const sources = socialSources(media)
  const selected = sources.find(item => `${item.kind}:${item.id}` === source)
  const capability = state?.platforms.find(item => item.id === platform)
  function label(value: string) {return t(socialStatusLabels[value] ?? '当前不可用')}
  async function prepare() {
    if (!state || !selected || !confirmed || inFlight.current || uncertain) return
    inFlight.current = true; setBusy(true); setError(''); setNotice('')
    try {
      const attempt = {key: crypto.randomUUID(), body: JSON.stringify({expected_version: state.version, platform, mode: 'export', ...(selected.kind === 'asset' ? {asset_id: selected.id} : {export_id: selected.id}), title: '', caption: '', confirmed: true})}
      pending.current = attempt
      await request(`${path}/social/jobs?${query}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': attempt.key}, body: attempt.body})
      pending.current = null
      setConfirmed(false); setNotice(t('素材交接记录已保存。请下载后自行在目标平台检查并确认发布。')); setRevision(value => value + 1)
    } catch (reason) {
      setError(errorMessage(reason, t('操作结果未确认，请先刷新发布记录。')))
      // A transport/server error may have happened after persistence. No automatic
      // POST replay or fresh idempotency key until the user has checked records.
      if (!(reason instanceof ApiError) || reason.status >= 500) setUncertain(true)
      else pending.current = null
    } finally {inFlight.current = false; setBusy(false)}
  }
  return <section className="panel social-center" aria-labelledby={`social-heading-${projectId}`}>
    <div className="section-heading"><h2 id={`social-heading-${projectId}`}>{t('社交发布中心')}</h2><button type="button" disabled={busy} onClick={() => setRevision(value => value + 1)}>{t('刷新素材与发布记录')}</button></div>
    <p>{t('按平台权限选择交付方式。下载、平台草稿与转到 App 均不等于已发布；只有平台确认后才显示发布成功。')}</p>
    {error && <p role="alert" className="error">{error}</p>}{notice && <p role="status" className="notice">{notice}</p>}
    {!state && !error && <p role="status">{t('正在读取发布能力…')}</p>}
    {state && <>
      <div className="social-platforms">{state.platforms.map(item => <article key={item.id}><h3>{t(socialPlatformLabels[item.id])}</h3><p>{label(item.connection_status)}</p><dl><dt>{t('直接发布')}</dt><dd>{label(item.direct_status)}</dd><dt>{t(item.id === 'tiktok' ? '上传平台草稿' : '转到平台 App')}</dt><dd>{label(item.handoff_status)}</dd></dl></article>)}</div>
      <p className="drama-help">{t('账号连接须由部署管理员配置已获批准的应用，再由账号本人授权。不会自动发起 OAuth 或上传；未就绪时仅提供素材导出。')}</p>
      <form onSubmit={event => {event.preventDefault(); void prepare()}}><fieldset disabled={busy || uncertain}>
        <div className="social-fields"><label>{t('目标平台')}<select aria-label={t('目标平台')} value={platform} onChange={event => {setPlatform(event.target.value as SocialPlatform); setConfirmed(false)}}>{state.platforms.map(item => <option key={item.id} value={item.id}>{t(socialPlatformLabels[item.id])}</option>)}</select></label>
        <label>{t('待交付素材')}<select aria-label={t('待交付素材')} value={source} onChange={event => {setSource(event.target.value); setConfirmed(false)}}><option value="">{t('选择图片、视频或已完成成片')}</option>{sources.map(item => <option key={`${item.kind}:${item.id}`} value={`${item.kind}:${item.id}`}>{item.kind === 'export' ? t('已完成成片') : item.mime_type} · {item.name}</option>)}</select></label></div>
        {sources.length === 0 && <p>{t('尚无可交付素材。请先上传图片或视频，或完成 MP4 导出，再刷新。')}</p>}
        <label>{t('交付方式')}<select aria-label={t('交付方式')} value="export" onChange={() => {}}><option value="export">{t('导出素材（不对外发布）')}</option><option value="direct" disabled>{t('直接发布')} — {label(capability?.direct_status ?? 'unavailable')}</option><option value="draft" disabled>{t('平台草稿，尚未发布')} — {t('当前不可用')}</option>{platform !== 'tiktok' && <option value="handoff" disabled>{t('转到平台 App')} — {label(capability?.handoff_status ?? 'unavailable')}</option>}</select></label>
        {platform === 'xiaohongshu' && <><p>{t('小红书当前暂停新接入。导出不自动填写标题、文案或话题；请在 App 内自行编辑。')}</p><XiaohongshuManualCaption key={`${space}:${projectId}`} /></>}
        {platform === 'douyin' && <p>{t('抖音分享需获批应用与相应权限，交接后仍由用户在 App 确认；未验证的结果查询不会标记已发布。')}</p>}
        {platform === 'tiktok' && <p>{t('TikTok 直发需应用审核及账号授权。未获批时不能把素材导出当作直发。')}</p>}
        <label className="social-consent"><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} />{t('我确认目标平台与素材，并理解此操作只准备下载，不会自动发布。')}</label>
        <button type="submit" className="primary" disabled={!selected || !confirmed}>{busy ? t('正在准备素材…') : t('准备下载与手动发布')}</button>
      </fieldset></form>
      {uncertain && <p role="status">{t('请求结果未知。请刷新并检查已有记录，不要重复提交或重新上传。')}</p>}
      <DouyinHandoffPanel projectId={projectId} space={space} state={state} sources={sources} reload={() => setRevision(value => value + 1)} />
      <SocialTikTokPanel projectId={projectId} space={space} state={state} sources={sources} reload={() => setRevision(value => value + 1)} />
      <h3>{t('交付与发布记录')}</h3>{state.jobs.length === 0 ? <p>{t('尚无交付记录。')}</p> : <ul className="social-jobs">{state.jobs.map(job => {
        const download = socialDownloadPath(job.download_url)
        return <li key={job.id}><strong>{t(socialPlatformLabels[job.platform])} · {label(job.status)}</strong><p>{label(job.mode)} · <time dateTime={job.created_at}>{formatDate(job.created_at)}</time></p><code>{job.id}</code>{download && <a href={`${baseURL}${download}`} download>{t('下载交付素材')}</a>}<SocialJobActions sources={sources} job={job} projectId={projectId} space={space} version={state.version} reload={() => setRevision(value => value + 1)} />{job.status === 'unknown' && <p>{t('请求结果未知。请刷新并检查已有记录，不要重复提交或重新上传。')}</p>}</li>
      })}</ul>}
    </>}
  </section>
}


function XiaohongshuManualCaption() {
  const {t} = useI18n()
  const [caption, setCaption] = useState('')
  const [status, setStatus] = useState<'idle' | 'copied' | 'failed'>('idle')
  const [copying, setCopying] = useState(false)
  const inFlight = useRef(false)
  async function copy() {
    if (!caption.trim() || inFlight.current) return
    inFlight.current = true; setCopying(true); setStatus('idle')
    try {
      if (!navigator.clipboard?.writeText) throw new Error('clipboard_unavailable')
      await navigator.clipboard.writeText(caption)
      setStatus('copied')
    } catch {setStatus('failed')} finally {inFlight.current = false; setCopying(false)}
  }
  return <div className="social-manual-caption">
    <label>{t('小红书手动发布文案（仅本页）')}<textarea disabled={copying} aria-label={t('小红书手动发布文案（仅本页）')} maxLength={2200} value={caption} onChange={event => {setCaption(event.target.value.slice(0, 2200)); setStatus('idle')}} /></label>
    <p>{t('文案不保存、不发送到服务器或平台。仅点击复制后写入剪贴板，由你在小红书内自行粘贴；切换项目或关闭此编辑区会清空。')}</p>
    <button type="button" disabled={copying || !caption.trim()} onClick={() => void copy()}>{copying ? t('正在复制文案…') : t('复制文案到剪贴板')}</button>
    {status === 'copied' && <p role="status">{t('文案已复制；尚未发送或发布，请自行在 App 内粘贴并检查。')}</p>}
    {status === 'failed' && <p role="alert">{t('无法访问剪贴板。请手动选择并复制上方文案；尚未发送或发布。')}</p>}
  </div>
}
