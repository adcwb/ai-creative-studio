import { useEffect, useRef, useState } from 'react'
import { TikTokPhotoEditor } from './TikTokPhotoEditor'
import { ApiError, baseURL, errorMessage, request } from '../api'
import { useI18n } from '../i18n'
import { useNavigationTask } from '../app/navigationGuard'
import { safeTikTokAuthorization, socialDownloadPath, type SocialJob, type SocialSource, type SocialState } from '../social'

interface Creator {
  version?: number
  creator_username: string; creator_nickname: string; privacy_level_options: string[]
  comment_disabled: boolean; duet_disabled: boolean; stitch_disabled: boolean; max_video_post_duration_sec: number
}
const callbackKey = 'studio.social.oauth'
export function SocialTikTokPanel({projectId, space, state, sources, reload}: {projectId: string; space: string; state: SocialState; sources: SocialSource[]; reload: () => void}) {
  const {t, formatDate} = useI18n()
  const [mediaType, setMediaType] = useState<'VIDEO' | 'PHOTO'>('VIDEO')
  const [photoIds, setPhotoIds] = useState<string[]>([])
  const [photoCover, setPhotoCover] = useState(0)
  const [photoTitle, setPhotoTitle] = useState('')
  const [photoDescription, setPhotoDescription] = useState('')
  const [autoMusic, setAutoMusic] = useState(false)
  const [publicConsent, setPublicConsent] = useState(false)
  const [connectionId, setConnectionId] = useState('')
  const [creator, setCreator] = useState<Creator | null>(null)
  const [creatorVersion, setCreatorVersion] = useState(0)
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
  useNavigationTask(busy || uncertain)
  const inFlight = useRef(false)
  const pending = useRef<{suffix: string; body: string; key: string} | null>(null)
  const path = `/projects/${encodeURIComponent(projectId)}/social`
  const query = new URLSearchParams({space_id: space}).toString()
  const capability = state.platforms.find(item => item.id === 'tiktok')
  const configured = capability?.connection_status === 'configured'
  const accountStates = state.connections.filter(item => item.platform === 'tiktok')
  const connections = accountStates.filter(item => !item.status.startsWith('revoked'))
  const reconnectNeeded = accountStates.some(item => ['refresh_unknown', 'expired', 'requires_reauth'].includes(item.status))
  const activeConnection = connections.some(item => item.id === connectionId && item.status === 'connected')
  const photoReady = photoIds.length > 0 && photoIds.length <= 35 && photoCover >= 0 && photoCover < photoIds.length && photoIds.every(id => sources.some(item => item.id === id && item.mime_type === 'image/jpeg')) && capability?.photo_status === 'available' && publicConsent
  const mediaReady = mediaType === 'PHOTO' ? photoReady : Boolean(sources.find(item => `${item.kind}:${item.id}` === source && item.mime_type === 'video/mp4'))
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
  async function action(run: () => Promise<void>, allowReconnect = false) {
    if (inFlight.current || (uncertain && !allowReconnect)) return
    inFlight.current = true; setBusy(true); setError('')
    try {await run()} catch (reason) {setError(errorMessage(reason, t('操作结果未确认，请先刷新发布记录。'))); if (pending.current && (!(reason instanceof ApiError) || reason.status >= 500)) setUncertain(true)} finally {inFlight.current = false; setBusy(false)}
  }
  async function connect() {
    await action(async () => {
      const result = await mutate('/connections/start', {expected_version: state.version, platform: 'tiktok'}) as {authorization_url: string}
      if (!safeTikTokAuthorization(result.authorization_url)) throw new Error(t('授权地址不符合平台白名单，已停止跳转。'))
      sessionStorage.setItem(callbackKey, JSON.stringify({projectId, space}))
      window.location.assign(result.authorization_url)
    }, reconnectNeeded)
  }
  async function creatorInfo() {
    await action(async () => {
      setCreator(null); setConsent(false)
      try {
        const value = await mutate(`/connections/${encodeURIComponent(connectionId)}/creator`, {}) as Creator
        setCreator(value); setCreatorVersion(value.version ?? state.version); setPrivacy(''); setComment(false); setDuet(false); setStitch(false); setConsent(false)
      } finally {reload()}
    })
  }
  async function prepare() {
    if (!mediaReady || !activeConnection || state.version < creatorVersion || !creator || !mode || (mode === 'direct' && (!privacy || (brandContent && privacy === 'SELF_ONLY'))) || !rights || !consent) return
    await action(async () => {
      await mutate('/jobs', {expected_version: state.version, platform: 'tiktok', mode, connection_id: connectionId, ...(mediaType === 'PHOTO' ? {media_type: 'PHOTO', photo_asset_ids: photoIds, photo_cover_index: photoCover, auto_add_music: mode === 'direct' && autoMusic, public_media_confirmed: publicConsent, title: photoTitle, caption: photoDescription} : {...(selected?.kind === 'asset' ? {asset_id: selected.id} : {export_id: selected?.id}), title: '', caption}), privacy_level: mode === 'direct' ? privacy : '', allow_comment: mode === 'direct' && comment, allow_duet: mediaType === 'VIDEO' && mode === 'direct' && duet, allow_stitch: mediaType === 'VIDEO' && mode === 'direct' && stitch, is_aigc: (mediaType === 'PHOTO' || mode === 'direct') && aigc, brand_content: mode === 'direct' && brandContent, brand_organic: mode === 'direct' && brandOrganic, rights_confirmed: rights, confirmed: consent})
      setConsent(false); reload()
    })
  }
  return <details className="social-tiktok"><summary>{t('TikTok 账号与发布确认')}</summary>
    <p>{t('只有获批应用与账号授权均就绪后才能发送。创建发布记录不会立即上传；请再次检查记录后显式发送。')}</p>
    {error && <p className="error" role="alert">{error}</p>}
    <details className="social-local-photos"><summary>{t('本地准备 TikTok 图片（无需账号连接）')}</summary><p>{t('转换与预览只处理当前项目素材，不连接平台、不生成公开链接，也不发布。')}</p><TikTokPhotoEditor projectId={projectId} space={space} version={state.version} reload={reload} sources={sources} ids={photoIds} cover={photoCover} disabled={busy || uncertain} onChange={(ids, cover) => {setMediaType('PHOTO'); setPhotoIds(ids); setPhotoCover(cover); setRights(false); setConsent(false); setPublicConsent(false)}} /></details>
    <button type="button" disabled={!configured || busy || (uncertain && !reconnectNeeded)} onClick={() => void connect()}>{t('前往 TikTok 授权连接账号')}</button>
    {!configured && <p>{t('应用尚未配置或批准，账号授权与平台发送均已禁用。')}</p>}
    <p>{t('读取权限或发送时，服务端仅在需要时自动刷新令牌。刷新结果未知或已过期时必须重新授权，不会自动重试或跳转。')}</p>
    <ul className="social-account-states">{accountStates.map(account => <li key={account.id}><strong>{account.display_name || account.username || account.id}</strong> · {t(account.status === 'connected' ? '已连接' : account.status === 'refreshing' ? '账号凭据正在刷新，请稍后重新读取' : account.status === 'refresh_unknown' ? '刷新结果未知，请主动重新授权' : ['expired', 'requires_reauth'].includes(account.status) ? '账号授权已失效，请主动重新授权' : account.status === 'revoked_remote_unconfirmed' ? '本地连接已撤销，平台撤销结果未确认' : '已撤销')}{account.expires_at && Number.isFinite(Date.parse(account.expires_at)) && <span> · {t('访问令牌到期：{time}', {time: formatDate(account.expires_at)})}</span>}</li>)}</ul>
    {connections.length > 0 && <><label>{t('目标 TikTok 账号')}<select aria-label={t('目标 TikTok 账号')} value={connectionId} disabled={busy} onChange={event => {setConnectionId(event.target.value); setCreator(null); setPrivacy(''); setConsent(false)}}><option value="">{t('请选择目标账号')}</option>{connections.map(item => <option key={item.id} value={item.id}>{item.display_name || item.username || item.id}</option>)}</select></label><div className="media-actions"><button type="button" disabled={!activeConnection || busy || uncertain} onClick={() => void creatorInfo()}>{t('读取账号发布权限')}</button><button type="button" disabled={!connectionId || busy || (uncertain && !reconnectNeeded)} onClick={() => {setCreator(null); setConsent(false); void action(async () => {await mutate(`/connections/${encodeURIComponent(connectionId)}/revoke`, {expected_version: state.version}); setConnectionId(''); reload()}, reconnectNeeded)}}>{t('撤销此账号连接')}</button></div></>}
    {creator && activeConnection && state.version < creatorVersion && <p role="status">{t('正在同步刷新后的配置版本，请稍候。')}</p>}
    {creator && activeConnection && state.version >= creatorVersion && <form onSubmit={event => {event.preventDefault(); void prepare()}}><fieldset disabled={busy || uncertain}>
      <p>{t('将发送至账号：{account}', {account: creator.creator_nickname || creator.creator_username})}</p><p>{t('平台视频时长上限：{seconds} 秒', {seconds: creator.max_video_post_duration_sec})}</p>
      <label>{t('TikTok 内容类型')}<select aria-label={t('TikTok 内容类型')} value={mediaType} onChange={event => {setMediaType(event.target.value as 'VIDEO' | 'PHOTO'); setRights(false); setConsent(false); setPublicConsent(false)}}><option value="VIDEO">{t('视频')}</option><option value="PHOTO">{t('图片与海报')}</option></select></label>
      {mediaType === 'VIDEO' && <><label>{t('待发布 MP4')}<select aria-label={t('待发布 MP4')} required value={source} onChange={event => {setSource(event.target.value); setRights(false); setConsent(false)}}><option value="">{t('选择图片、视频或已完成成片')}</option>{videos.map(item => <option key={`${item.kind}:${item.id}`} value={`${item.kind}:${item.id}`}>{item.episode_number && <>{t('第 {p0} 集', {p0:item.episode_number})} · </>}{item.name}</option>)}</select></label>
      {preview && <video className="social-preview" controls preload="metadata" src={`${baseURL}${preview}`} aria-label={t('待发布视频预览')} />}</>}
      {mediaType === 'PHOTO' && <><p>{t('发布顺序与封面使用上方本地图片准备区的选择，请先检查转换后的 JPEG 预览。')}</p><p>{t('图片发布需要已批准的公开媒体入口。短期链接最长有效 65 分钟；持有链接者可读取图片，撤销不能收回已有副本。')}</p>{capability?.photo_status !== 'available' && <p role="status">{t('图片公开媒体入口未配置或未获批准，无法提交图片发布。')}</p>}<label className="social-consent"><input type="checkbox" checked={publicConsent} onChange={event => {setPublicConsent(event.target.checked); setConsent(false)}} />{t('我同意为所选图片创建短期公开链接供 TikTok 下载。')}</label><label className="social-consent"><input type="checkbox" disabled={mode !== 'direct'} checked={autoMusic} onChange={event => {setAutoMusic(event.target.checked); setConsent(false)}} />{t('允许 TikTok 自动添加音乐')}</label></>}
      <label>{t('发布模式')}<select aria-label={t('发布模式')} required value={mode} onChange={event => {setMode(event.target.value); setConsent(false); if (event.target.value === 'draft') {setAutoMusic(false); setBrandContent(false); setBrandOrganic(false); setComment(false); setDuet(false); setStitch(false)}}}><option value="">{t('请选择发布模式')}</option><option value="direct" disabled={capability?.direct_status !== 'available'}>{t('直接发布')}</option><option value="draft">{t('上传到 TikTok 收件箱草稿')}</option></select></label>
      {mode === 'draft' && <p>{t(mediaType === 'PHOTO' ? '图片草稿保留标题、描述与 AI 声明；隐私、互动及商业披露须在 TikTok 内确认，尚未发布。' : '收件箱上传仅交付视频草稿；文案、隐私与披露须在 TikTok 内重新编辑和确认，尚未发布。')}</p>}
      <fieldset disabled={mode !== 'direct' && mediaType !== 'PHOTO'}>{mediaType === 'PHOTO' ? <><label>{t('图片标题（最多 90 字符）')}<input aria-label={t('图片标题（最多 90 字符）')} maxLength={90} value={photoTitle} onChange={event => {setPhotoTitle(event.target.value.slice(0, 90)); setConsent(false)}} /></label><label>{t('图片描述（最多 4000 字符）')}<textarea aria-label={t('图片描述（最多 4000 字符）')} maxLength={4000} value={photoDescription} onChange={event => {setPhotoDescription(event.target.value.slice(0, 4000)); setConsent(false)}} /></label></> : <label>{t('发布文案')}<textarea maxLength={2200} value={caption} onChange={event => {setCaption(event.target.value); setConsent(false)}} /></label>}
      <label>{t('谁可以观看')}<select aria-label={t('谁可以观看')} disabled={mode !== 'direct'} required={mode === 'direct'} value={privacy} onChange={event => {setPrivacy(event.target.value); setConsent(false)}}><option value="">{t('请选择隐私范围（无默认值）')}</option>{creator.privacy_level_options.map(item => <option key={item} value={item}>{item === 'PUBLIC_TO_EVERYONE' ? t('所有人') : item === 'MUTUAL_FOLLOW_FRIENDS' ? t('互相关注的好友') : item === 'FOLLOWER_OF_CREATOR' ? t('关注者') : item === 'SELF_ONLY' ? t('仅自己') : item}</option>)}</select></label>
      <div className="social-options"><label><input type="checkbox" checked={comment} disabled={mode !== 'direct' || creator.comment_disabled} onChange={event => {setComment(event.target.checked); setConsent(false)}} />{t('允许评论')}</label><label><input type="checkbox" checked={duet} disabled={mode !== 'direct' || mediaType === 'PHOTO' || creator.duet_disabled} onChange={event => {setDuet(event.target.checked); setConsent(false)}} />{t('允许合拍')}</label><label><input type="checkbox" checked={stitch} disabled={mode !== 'direct' || mediaType === 'PHOTO' || creator.stitch_disabled} onChange={event => {setStitch(event.target.checked); setConsent(false)}} />{t('允许拼接')}</label></div>
      <label className="social-consent"><input type="checkbox" checked={aigc} onChange={event => {setAigc(event.target.checked); setConsent(false)}} />{t('此内容由 AI 生成或修改')}</label>
      <label className="social-consent"><input type="checkbox" disabled={mode !== 'direct'} checked={brandOrganic} onChange={event => {setBrandOrganic(event.target.checked); setConsent(false)}} />{t('推广自己的品牌')}</label>
      <label className="social-consent"><input type="checkbox" disabled={mode !== 'direct'} checked={brandContent} onChange={event => {setBrandContent(event.target.checked); setConsent(false)}} />{t('与第三方品牌的商业合作')}</label>
      {brandContent && privacy === 'SELF_ONLY' && <p role="alert">{t('商业合作内容不能选择仅自己，请显式修改隐私或商业披露。')}</p>}</fieldset>
      {(brandContent || brandOrganic) && mode === 'direct' && <p>{t('提交商业内容即确认遵守 TikTok 品牌内容政策。')} <a href="https://www.tiktok.com/legal/page/global/bc-policy/en" target="_blank" rel="noreferrer">{t('阅读品牌内容政策')}</a></p>}
      <label className="social-consent"><input type="checkbox" checked={rights} onChange={event => {setRights(event.target.checked); setConsent(false)}} />{t(mediaType === 'PHOTO' ? '我拥有所选图片的必要权利，并同意 TikTok 音乐使用确认。' : '我拥有此视频及音频的必要权利，并同意 TikTok 音乐使用确认。')}</label><a href="https://www.tiktok.com/legal/page/global/music-usage-confirmation/en" target="_blank" rel="noreferrer">{t('阅读 TikTok 音乐使用确认')}</a>
      <label className="social-consent"><input type="checkbox" checked={consent} onChange={event => setConsent(event.target.checked)} />{t(mediaType === 'PHOTO' ? '我已预览图片顺序与封面，并确认目标账号、标题、描述、隐私、互动与披露设置。' : '我已预览视频并确认目标账号、文案、隐私、互动与商业披露设置。')}</label>
      <button type="submit" disabled={!mediaReady || !activeConnection || !mode || (mode === 'direct' && (!privacy || (brandContent && privacy === 'SELF_ONLY'))) || !rights || !consent}>{t('保存待发送发布记录')}</button>
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

export function SocialJobActions({job, projectId, space, version, reload, sources = []}: {job: SocialJob; projectId: string; space: string; version: number; reload: () => void; sources?: SocialSource[]}) {
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
  const [revokeConfirmed, setRevokeConfirmed] = useState(false)
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  useNavigationTask(busy || uncertain)
  const [error, setError] = useState('')
  const inFlight = useRef(false)
  if (job.platform !== 'tiktok' || !['direct', 'draft'].includes(job.mode)) return null
  async function act(operation: 'dispatch' | 'refresh' | 'revoke-photo-access') {
    if (inFlight.current || (operation === 'revoke-photo-access' && !revokeConfirmed) || (operation === 'dispatch' && (!confirmed || uncertain))) return
    inFlight.current = true; setBusy(true); setError('')
    try {await request(`/projects/${encodeURIComponent(projectId)}/social/jobs/${encodeURIComponent(job.id)}/${operation}?${new URLSearchParams({space_id: space})}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': crypto.randomUUID()}, body: JSON.stringify({expected_version: version, ...(operation === 'revoke-photo-access' ? {confirmed: true} : {})})}); setConfirmed(false); setRevokeConfirmed(false); reload()} catch (reason) {setError(errorMessage(reason, t('操作结果未确认，请先刷新发布记录。'))); if (operation === 'dispatch') setUncertain(true)} finally {inFlight.current = false; setBusy(false)}
  }
  const intent = job.intent ?? {}
  return <div className="social-job-actions">{intent.media_type === 'PHOTO' && <><h4>{t('已保存的图片发布意图')}</h4>{job.title && <p>{job.title}</p>}<ol className="tiktok-photo-list">{intent.photo_asset_ids?.map((id, index) => {const source = sources.find(item => item.id === id); const url = socialDownloadPath(source?.file_url); return <li key={id}>{url && <img src={`${baseURL}${url}`} alt={t('已保存图片 {index} 预览', {index: index + 1})} />}<span>{index + 1}. {source?.name ?? id}{index === intent.photo_cover_index ? ` · ${t('图片封面')}` : ''}</span></li>})}</ol><p>{t('自动添加音乐：{value}', {value: intent.auto_add_music ? t('是') : t('否')})}</p><p>{t('已同意创建短期公开图片链接；尚未发送前不会发布到平台。')}</p></>}<p>{t('目标账号记录：{account}', {account: job.connection_id ?? '—'})} · {intent.privacy_level}</p>{job.caption && <p>{job.caption}</p>}
    <p>{t('互动与披露：评论 {comment}；合拍 {duet}；拼接 {stitch}；AI {ai}；商业合作 {brand}；自有品牌 {organic}', {comment: intent.allow_comment ? t('开启') : t('关闭'), duet: intent.allow_duet ? t('开启') : t('关闭'), stitch: intent.allow_stitch ? t('开启') : t('关闭'), ai: intent.is_aigc ? t('是') : t('否'), brand: intent.brand_content ? t('是') : t('否'), organic: intent.brand_organic ? t('是') : t('否')})}</p>
    {error && <p role="alert">{error}</p>}{job.status === 'prepared' && <><label className="social-consent"><input type="checkbox" checked={confirmed} disabled={busy || uncertain} onChange={event => setConfirmed(event.target.checked)} />{t('确认发送此记录到目标账号（会上传素材）')}</label><button disabled={!confirmed || busy || uncertain} onClick={() => void act('dispatch')}>{t('发送到 TikTok')}</button></>}
    {['processing', 'dispatching', 'unknown', 'draft'].includes(job.status) && <button disabled={busy || pollAfter > clock} onClick={() => void act('refresh')}>{t('仅查询平台状态（不重新发送）')}</button>}
    {intent.media_type === 'PHOTO' && job.photo_expires_at && <p>{t('图片公开链接到期：{time}', {time: formatDate(job.photo_expires_at)})}</p>}
    {intent.media_type === 'PHOTO' && job.photo_expires_at && !job.photo_access_revoked && <><label className="social-consent"><input type="checkbox" checked={revokeConfirmed} disabled={busy} onChange={event => setRevokeConfirmed(event.target.checked)} />{t('我理解撤销仅阻止后续图片下载，不能撤回已有副本或已发布内容。')}</label><button disabled={!revokeConfirmed || busy} onClick={() => void act('revoke-photo-access')}>{t('撤销此记录的公开图片链接')}</button></>}
    {job.photo_access_revoked && <p>{t('公开图片链接已撤销；这不代表平台发布已取消。')}</p>}
    {pollAfter > clock && <p role="status">{t('平台查询冷却中，请在 {time} 后查询。', {time: formatDate(new Date(pollAfter))})}</p>}
    {(uncertain || job.status === 'unknown') && <p>{t('请求结果未知。请刷新并检查已有记录，不要重复提交或重新上传。')}</p>}
  </div>
}
