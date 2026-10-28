import { useEffect, useRef, useState } from 'react'
import { ApiError, baseURL, errorMessage, request } from '../api'
import { useI18n } from '../i18n'
import { safeTikTokAuthorization, socialDownloadPath, type SocialJob, type SocialSource, type SocialState } from '../social'

interface Creator {
  creator_username: string; creator_nickname: string; privacy_level_options: string[]
  comment_disabled: boolean; duet_disabled: boolean; stitch_disabled: boolean; max_video_post_duration_sec: number
}
const callbackKey = 'studio.social.oauth'
export function SocialTikTokPanel({projectId, space, state, sources, reload}: {projectId: string; space: string; state: SocialState; sources: SocialSource[]; reload: () => void}) {
  const {t} = useI18n()
  const [connectionId, setConnectionId] = useState('')
  const [creator, setCreator] = useState<Creator | null>(null)
  const [source, setSource] = useState('')
  const [mode, setMode] = useState('')
  const [privacy, setPrivacy] = useState('')
  const [caption, setCaption] = useState('')
  const [comment, setComment] = useState(false)
  const [duet, setDuet] = useState(false)
  const [stitch, setStitch] = useState(false)
  const [aigc, setAigc] = useState(false)
  const [brandContent, setBrandContent] = useState(false)
  const [brandOrganic, setBrandOrganic] = useState(false)
  const [rights, setRights] = useState(false)
  const [consent, setConsent] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [uncertain, setUncertain] = useState(false)
  const inFlight = useRef(false)
  const pending = useRef<{suffix: string; body: string; key: string} | null>(null)
  const path = `/projects/${encodeURIComponent(projectId)}/social`
  const query = new URLSearchParams({space_id: space}).toString()
  const capability = state.platforms.find(item => item.id === 'tiktok')
  const configured = capability?.connection_status === 'configured'
  const connections = state.connections.filter(item => item.platform === 'tiktok' && item.status === 'connected')
  const videos = sources.filter(item => item.mime_type === 'video/mp4')
  const selected = videos.find(item => `${item.kind}:${item.id}` === source)
  const preview = socialDownloadPath(selected?.file_url)
  async function mutate(suffix: string, body: object): Promise<unknown> {
    const attempt = {suffix, body: JSON.stringify(body), key: crypto.randomUUID()}
    pending.current = attempt
    const result = await request(`${path}${suffix}?${query}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': attempt.key}, body: attempt.body})
    pending.current = null
    return result
  }
  async function action(run: () => Promise<void>) {
    if (inFlight.current || uncertain) return
    inFlight.current = true; setBusy(true); setError('')
    try {await run()} catch (reason) {setError(errorMessage(reason, t('操作结果未确认，请先刷新发布记录。'))); if (!(reason instanceof ApiError) || reason.status >= 500) setUncertain(true)} finally {inFlight.current = false; setBusy(false)}
  }
  async function connect() {
    await action(async () => {
      const result = await mutate('/connections/start', {expected_version: state.version, platform: 'tiktok'}) as {authorization_url: string}
      if (!safeTikTokAuthorization(result.authorization_url)) throw new Error(t('授权地址不符合平台白名单，已停止跳转。'))
      sessionStorage.setItem(callbackKey, JSON.stringify({projectId, space}))
      window.location.assign(result.authorization_url)
    })
  }
  async function creatorInfo() {
    await action(async () => {
      const value = await request<Creator>(`${path}/connections/${encodeURIComponent(connectionId)}/creator?${query}`)
      setCreator(value); setPrivacy(''); setComment(false); setDuet(false); setStitch(false); setConsent(false)
    })
  }
  async function prepare() {
    if (!selected || !creator || !mode || (mode === 'direct' && (!privacy || (brandContent && privacy === 'SELF_ONLY'))) || !rights || !consent) return
    await action(async () => {
      await mutate('/jobs', {expected_version: state.version, platform: 'tiktok', mode, connection_id: connectionId, ...(selected.kind === 'asset' ? {asset_id: selected.id} : {export_id: selected.id}), title: '', caption, privacy_level: mode === 'direct' ? privacy : '', allow_comment: mode === 'direct' && comment, allow_duet: mode === 'direct' && duet, allow_stitch: mode === 'direct' && stitch, is_aigc: mode === 'direct' && aigc, brand_content: mode === 'direct' && brandContent, brand_organic: mode === 'direct' && brandOrganic, rights_confirmed: rights, confirmed: consent})
      setConsent(false); reload()
    })
  }
  return <details className="social-tiktok"><summary>{t('TikTok 账号与发布确认')}</summary>
    <p>{t('只有获批应用与账号授权均就绪后才能发送。创建发布记录不会立即上传；请再次检查记录后显式发送。')}</p>
    {error && <p className="error" role="alert">{error}</p>}
    <button type="button" disabled={!configured || busy || uncertain} onClick={() => void connect()}>{t('前往 TikTok 授权连接账号')}</button>
    {!configured && <p>{t('应用尚未配置或批准，账号授权与平台发送均已禁用。')}</p>}
    {connections.length > 0 && <><label>{t('目标 TikTok 账号')}<select aria-label={t('目标 TikTok 账号')} value={connectionId} disabled={busy} onChange={event => {setConnectionId(event.target.value); setCreator(null); setPrivacy(''); setConsent(false)}}><option value="">{t('请选择目标账号')}</option>{connections.map(item => <option key={item.id} value={item.id}>{item.display_name || item.username || item.id}</option>)}</select></label><div className="media-actions"><button type="button" disabled={!connectionId || busy || uncertain} onClick={() => void creatorInfo()}>{t('读取账号发布权限')}</button><button type="button" disabled={!connectionId || busy || uncertain} onClick={() => {setCreator(null); setConsent(false); void action(async () => {await mutate(`/connections/${encodeURIComponent(connectionId)}/revoke`, {expected_version: state.version}); setConnectionId(''); reload()})}}>{t('撤销此账号连接')}</button></div></>}
    {creator && <form onSubmit={event => {event.preventDefault(); void prepare()}}><fieldset disabled={busy || uncertain}>
      <p>{t('将发送至账号：{account}', {account: creator.creator_nickname || creator.creator_username})}</p><p>{t('平台视频时长上限：{seconds} 秒', {seconds: creator.max_video_post_duration_sec})}</p>
      <label>{t('待发布 MP4')}<select aria-label={t('待发布 MP4')} required value={source} onChange={event => {setSource(event.target.value); setRights(false); setConsent(false)}}><option value="">{t('选择图片、视频或已完成成片')}</option>{videos.map(item => <option key={`${item.kind}:${item.id}`} value={`${item.kind}:${item.id}`}>{item.name}</option>)}</select></label>
      {preview && <video className="social-preview" controls preload="metadata" src={`${baseURL}${preview}`} aria-label={t('待发布视频预览')} />}
      <label>{t('发布模式')}<select aria-label={t('发布模式')} required value={mode} onChange={event => {setMode(event.target.value); setConsent(false)}}><option value="">{t('请选择发布模式')}</option><option value="direct" disabled={capability?.direct_status !== 'available'}>{t('直接发布')}</option><option value="draft">{t('上传到 TikTok 收件箱草稿')}</option></select></label>
      {mode === 'draft' && <p>{t('收件箱上传仅交付视频草稿；文案、隐私与披露须在 TikTok 内重新编辑和确认，尚未发布。')}</p>}
      <fieldset disabled={mode !== 'direct'}><label>{t('发布文案')}<textarea maxLength={2200} value={caption} onChange={event => {setCaption(event.target.value); setConsent(false)}} /></label>
      <label>{t('谁可以观看')}<select aria-label={t('谁可以观看')} required={mode === 'direct'} value={privacy} onChange={event => {setPrivacy(event.target.value); setConsent(false)}}><option value="">{t('请选择隐私范围（无默认值）')}</option>{creator.privacy_level_options.map(item => <option key={item} value={item}>{item === 'PUBLIC_TO_EVERYONE' ? t('所有人') : item === 'MUTUAL_FOLLOW_FRIENDS' ? t('互相关注的好友') : item === 'FOLLOWER_OF_CREATOR' ? t('关注者') : item === 'SELF_ONLY' ? t('仅自己') : item}</option>)}</select></label>
      <div className="social-options"><label><input type="checkbox" checked={comment} disabled={creator.comment_disabled} onChange={event => {setComment(event.target.checked); setConsent(false)}} />{t('允许评论')}</label><label><input type="checkbox" checked={duet} disabled={creator.duet_disabled} onChange={event => {setDuet(event.target.checked); setConsent(false)}} />{t('允许合拍')}</label><label><input type="checkbox" checked={stitch} disabled={creator.stitch_disabled} onChange={event => {setStitch(event.target.checked); setConsent(false)}} />{t('允许拼接')}</label></div>
      <label className="social-consent"><input type="checkbox" checked={aigc} onChange={event => {setAigc(event.target.checked); setConsent(false)}} />{t('此内容由 AI 生成或修改')}</label>
      <label className="social-consent"><input type="checkbox" checked={brandOrganic} onChange={event => {setBrandOrganic(event.target.checked); setConsent(false)}} />{t('推广自己的品牌')}</label>
      <label className="social-consent"><input type="checkbox" checked={brandContent} onChange={event => {setBrandContent(event.target.checked); setConsent(false)}} />{t('与第三方品牌的商业合作')}</label>
      {brandContent && privacy === 'SELF_ONLY' && <p role="alert">{t('商业合作内容不能选择仅自己，请显式修改隐私或商业披露。')}</p>}</fieldset>
      {(brandContent || brandOrganic) && mode === 'direct' && <p>{t('提交商业内容即确认遵守 TikTok 品牌内容政策。')} <a href="https://www.tiktok.com/legal/page/global/bc-policy/en" target="_blank" rel="noreferrer">{t('阅读品牌内容政策')}</a></p>}
      <label className="social-consent"><input type="checkbox" checked={rights} onChange={event => {setRights(event.target.checked); setConsent(false)}} />{t('我拥有此视频及音频的必要权利，并同意 TikTok 音乐使用确认。')}</label><a href="https://www.tiktok.com/legal/page/global/music-usage-confirmation/en" target="_blank" rel="noreferrer">{t('阅读 TikTok 音乐使用确认')}</a>
      <label className="social-consent"><input type="checkbox" checked={consent} onChange={event => setConsent(event.target.checked)} />{t('我已预览视频并确认目标账号、文案、隐私、互动与商业披露设置。')}</label>
      <button type="submit" disabled={!selected || !mode || (mode === 'direct' && (!privacy || (brandContent && privacy === 'SELF_ONLY'))) || !rights || !consent}>{t('保存待发送发布记录')}</button>
    </fieldset></form>}
    {uncertain && <p role="status">{t('请求结果未知。请刷新并检查已有记录，不要重复提交或重新上传。')}</p>}
  </details>
}

export function SocialOAuthCallback() {
  const {t} = useI18n()
  const [status, setStatus] = useState('')
  const [busy, setBusy] = useState(false)
  const [complete, setComplete] = useState(false)
  const [callback] = useState(() => {const query = new URLSearchParams(window.location.search); return {code: query.get('code'), state: query.get('state'), error: query.has('error')}})
  useEffect(() => {history.replaceState(null, '', '/social-callback')}, [])
  const attempted = useRef(false)
  const [attemptedOnce, setAttemptedOnce] = useState(false)
  async function finish() {
    if (attempted.current) return
    attempted.current = true; setAttemptedOnce(true); setBusy(true)
    try {
      const context = JSON.parse(sessionStorage.getItem(callbackKey) ?? 'null') as {projectId?: string; space?: string} | null
      const {code, state} = callback
      if (!context?.projectId || !context.space || !code || !state || callback.error) throw new Error(t('授权回调缺失或已被拒绝，请返回发布中心重新检查连接。'))
      // The address was scrubbed on mount. Keep the one-time code only in
      // component memory; never automatically retry an uncertain exchange.
      await request(`/projects/${encodeURIComponent(context.projectId)}/social/connections/complete?${new URLSearchParams({space_id: context.space})}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID()}, body: JSON.stringify({code, state})})
      sessionStorage.removeItem(callbackKey); setComplete(true); setStatus(t('账号连接已保存；尚未上传或发布任何内容。'))
    } catch (reason) {setStatus(errorMessage(reason, t('授权结果未确认，请返回发布中心检查连接。')))} finally {setBusy(false)}
  }
  return <main className="panel social-center"><h1>{t('完成 TikTok 账号授权')}</h1><p>{t('确认后仅交换本次授权码并保存服务端连接，不上传或发布内容。')}</p>{status && <p role={complete ? 'status' : 'alert'}>{status}</p>}<button disabled={busy || attemptedOnce} onClick={() => void finish()}>{t('确认完成账号连接')}</button><p><a href="/">{t('返回创作工作室')}</a></p></main>
}

export function SocialJobActions({job, projectId, space, version, reload}: {job: SocialJob; projectId: string; space: string; version: number; reload: () => void}) {
  const {t, formatDate} = useI18n()
  const [clock, setClock] = useState(() => Date.now())
  const pollAfter = Math.max(Date.parse(job.next_poll_at ?? '') || 0, Date.parse(job.lease_until ?? '') || 0)
  useEffect(() => {
    const delay = pollAfter - Date.now()
    if (delay <= 0) return
    const timer = setTimeout(() => setClock(Date.now()), Math.min(delay + 50, 2147483647))
    return () => clearTimeout(timer)
  }, [pollAfter])
  const [confirmed, setConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [error, setError] = useState('')
  const inFlight = useRef(false)
  if (job.platform !== 'tiktok' || !['direct', 'draft'].includes(job.mode)) return null
  async function act(operation: 'dispatch' | 'refresh') {
    if (inFlight.current || (operation === 'dispatch' && (!confirmed || uncertain))) return
    inFlight.current = true; setBusy(true); setError('')
    try {await request(`/projects/${encodeURIComponent(projectId)}/social/jobs/${encodeURIComponent(job.id)}/${operation}?${new URLSearchParams({space_id: space})}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID()}, body: JSON.stringify({expected_version: version})}); setConfirmed(false); reload()} catch (reason) {setError(errorMessage(reason, t('操作结果未确认，请先刷新发布记录。'))); if (operation === 'dispatch') setUncertain(true)} finally {inFlight.current = false; setBusy(false)}
  }
  const intent = job.intent ?? {}
  return <div className="social-job-actions"><p>{t('目标账号记录：{account}', {account: job.connection_id ?? '—'})} · {intent.privacy_level}</p>{job.caption && <p>{job.caption}</p>}
    <p>{t('互动与披露：评论 {comment}；合拍 {duet}；拼接 {stitch}；AI {ai}；商业合作 {brand}；自有品牌 {organic}', {comment: intent.allow_comment ? t('开启') : t('关闭'), duet: intent.allow_duet ? t('开启') : t('关闭'), stitch: intent.allow_stitch ? t('开启') : t('关闭'), ai: intent.is_aigc ? t('是') : t('否'), brand: intent.brand_content ? t('是') : t('否'), organic: intent.brand_organic ? t('是') : t('否')})}</p>
    {error && <p role="alert">{error}</p>}{job.status === 'prepared' && <><label className="social-consent"><input type="checkbox" checked={confirmed} disabled={busy || uncertain} onChange={event => setConfirmed(event.target.checked)} />{t('确认发送此记录到目标账号（会上传素材）')}</label><button disabled={!confirmed || busy || uncertain} onClick={() => void act('dispatch')}>{t('发送到 TikTok')}</button></>}
    {['processing', 'dispatching', 'unknown', 'draft'].includes(job.status) && <button disabled={busy || pollAfter > clock} onClick={() => void act('refresh')}>{t('仅查询平台状态（不重新发送）')}</button>}
    {pollAfter > clock && <p role="status">{t('平台查询冷却中，请在 {time} 后查询。', {time: formatDate(new Date(pollAfter))})}</p>}
    {(uncertain || job.status === 'unknown') && <p>{t('请求结果未知。请刷新并检查已有记录，不要重复提交或重新上传。')}</p>}
  </div>
}
