import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError, baseURL, errorMessage, request } from '../api'
import { translate as t, useI18n } from '../i18n'
import type { DramaProject } from '../drama'
import { RuntimeModelPicker } from './RuntimeModelPicker'
import { runtimeFields, runtimeReady, type RuntimeChoice } from '../runtimeSelection'
import './DigitalHumanStudio.css'

interface Avatar {id: string; name: string; reference_asset_id: string; route: 'photo' | 'video'; consent_reference: string; revoked_at?: string}
interface Asset {id: string; name: string; kind: string; duration: number; file_url: string}
interface Capability {provider: string; model?: string; ready: boolean; message: string}
interface Job {id: string; kind: string; status: string; asset_id?: string; error?: string; avatar_id?: string}
interface Attempt {path: string; body: BodyInit; mime: string; key: string; action: 'profile' | 'job' | 'upload' | 'revoke' | 'cancel'}
const jobLabels: Record<string, string> = {queued: '排队中', running: '生成中', succeeded: '已完成', failed: '失败', cancelled: '已取消'}

export function DigitalHumanStudio({project, space, disabled, onDirtyChange}: {project: DramaProject; space: string; disabled: boolean; onDirtyChange: (dirty: boolean) => void}) {
  const {locale} = useI18n()
  const [avatars, setAvatars] = useState<Avatar[]>([])
  const [assets, setAssets] = useState<Asset[]>([])
  const [jobs, setJobs] = useState<Job[]>([])
  const [caps, setCaps] = useState<Record<string, Capability>>({})
  const [name, setName] = useState('')
  const [reference, setReference] = useState('')
  const [consent, setConsent] = useState('')
  const [likeness, setLikeness] = useState(false)
  const [reviewed, setReviewed] = useState(false)
  const [avatarId, setAvatarId] = useState('')
  const [audioId, setAudioId] = useState('')
  const [text, setText] = useState('')
  const [voiceConsent, setVoiceConsent] = useState('')
  const [voiceConfirmed, setVoiceConfirmed] = useState(false)
  const [choice, setChoice] = useState<RuntimeChoice | null>(null)
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [error, setError] = useState('')
  const [readError, setReadError] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [revision, setRevision] = useState(0)
  const pending = useRef<Attempt | null>(null)
  const flight = useRef(false)
  const live = useRef(true)
  const prefix = `/projects/${encodeURIComponent(project.id)}`
  const query = new URLSearchParams({space_id: space}).toString()
  const avatar = avatars.find(item => item.id === avatarId)
  const capability = caps[`digital_human_${avatar?.route ?? 'photo'}`]
  const active = jobs.some(job => job.status === 'queued' || job.status === 'running')
  const locked = disabled || busy || uncertain
  const speechInput = {kind: 'speech', expected_version: project.version, prompt: text.trim()}
  const speechPreview = {operation: 'creative' as const, project_id: project.id, input: speechInput}
  const speechReady = choice ? runtimeReady(choice, speechPreview) : caps.speech?.ready
  const audio = assets.find(item => item.id === audioId)
  const referenceAsset = assets.find(item => item.id === avatar?.reference_asset_id)
  const validAudio = audio?.kind === 'audio' && audio.duration > 0 && audio.duration <= 30 && (avatar?.route !== 'video' || audio.duration <= (referenceAsset?.duration ?? 0) + .1)

  useEffect(() => {live.current = true; return () => {live.current = false}}, [])
  useEffect(() => {onDirtyChange(busy || uncertain || !!name || !!consent || !!text || !!voiceConsent); return () => onDirtyChange(false)}, [busy, uncertain, name, consent, text, voiceConsent, onDirtyChange])
  useEffect(() => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout>
    async function poll() {
      try {
        const [people, media, tasks, capabilities] = await Promise.all([
          request<Avatar[]>(`${prefix}/digital-humans?${query}`, {signal: controller.signal}),
          request<{assets: Asset[]}>(`${prefix}/media?${query}`, {signal: controller.signal}),
          request<Job[]>(`${prefix}/creative-jobs?${query}`, {signal: controller.signal}),
          request<Record<string, Capability>>('/creative-media-capabilities', {signal: controller.signal}),
        ])
        if (controller.signal.aborted) return
        setAvatars(people); setAssets(media.assets); setJobs(tasks); setCaps(capabilities); setLoaded(true); setReadError('')
      } catch (failure) {if (!controller.signal.aborted) {setLoaded(false); setReadError(errorMessage(failure, t('数字人数据读取失败')))}}
      finally {if (!controller.signal.aborted) timer = setTimeout(() => void poll(), 2500)}
    }
    void poll()
    return () => {controller.abort(); clearTimeout(timer)}
  }, [prefix, query, revision, locale])

  async function execute() {
    const attempt = pending.current
    if (!attempt || flight.current) return
    flight.current = true; setBusy(true); setError('')
    try {
      const result = await request<Avatar | Job | {assets: Asset[]}>(attempt.path, {method: 'POST', headers: {'Content-Type': attempt.mime, 'Idempotency-Key': attempt.key}, body: attempt.body})
      if (!live.current) return
      pending.current = null; setUncertain(false)
      if (attempt.action === 'profile') {setAvatarId((result as Avatar).id); setName(''); setConsent(''); setLikeness(false); setReviewed(false)}
      if (attempt.action === 'job') {setText(''); setVoiceConsent(''); setVoiceConfirmed(false); setChoice(null)}
      setRevision(value => value + 1)
    } catch (failure) {
      if (live.current) {
        const unknown = !(failure instanceof ApiError) || failure.status >= 500
        setUncertain(unknown)
        if (!unknown) pending.current = null
        setError(unknown ? t('结果尚未确认，请重试同一请求。') : errorMessage(failure, t('操作失败')))
      }
    } finally {flight.current = false; if (live.current) setBusy(false)}
  }
  function submit(suffix: string, body: unknown, action: Attempt['action']) {
    if (locked || flight.current) return
    pending.current = {path: `${prefix}${suffix}?${query}`, body: JSON.stringify(body), mime: 'application/json', key: crypto.randomUUID(), action}
    void execute()
  }
  function upload(file?: File) {
    if (!file || locked || flight.current) return
    const mime = file.type === 'audio/x-wav' ? 'audio/wav' : file.type
    if (!['image/png', 'image/jpeg', 'video/mp4', 'audio/wav', 'audio/mpeg'].includes(mime) || file.size > 10 * 1024 * 1024) {setError(t('请选择10MiB以内的PNG、JPEG、MP4、WAV或MP3')); return}
    const fields = new URLSearchParams({space_id: space, filename: file.name, role: mime.startsWith('audio/') ? 'voice' : 'reference'})
    pending.current = {path: `${prefix}/media/uploads?${fields}`, body: file, mime, key: crypto.randomUUID(), action: 'upload'}
    void execute()
  }
  return <section className="panel digital-human-studio">
    <div className="section-heading"><div><span className="eyebrow">DIGITAL HUMAN</span><h2>{t('数字人口播')}</h2></div><span>{t('单人 · 固定机位 · 最长30秒')}</span></div>
    <p className="drama-help">{t('上传已授权形象，准备配音，再生成口播视频。照片驱动补充人物运动；视频改口型保留原动作。成片带有AI生成标识。')}</p>
    <p className="drama-source">{t('素材仅做格式、尺寸和时长检查；人脸数量、清晰度、遮挡及生成效果需要人工检查。')}</p>
    {readError && <p role="alert" className="error">{readError}</p>}
    {error && <div role="alert" className="error">{error}{uncertain && <button disabled={busy} onClick={() => void execute()}>{t('重试同一请求')}</button>}</div>}
    <label>{t('上传形象或配音素材')}<input type="file" accept="image/png,image/jpeg,video/mp4,audio/wav,audio/mpeg" disabled={locked} onChange={event => {upload(event.target.files?.[0]); event.target.value = ''}} /></label>
    <details className="drama-history" open={avatars.length === 0}>
      <summary>{t('登记授权形象')}</summary>
      <fieldset disabled={locked}><div className="storyboard-fields">
        <label>{t('形象名称')}<input maxLength={40} value={name} onChange={event => setName(event.target.value)} /></label>
        <label>{t('形象照片或视频')}<select value={reference} onChange={event => {setReference(event.target.value); setReviewed(false); setLikeness(false)}}><option value="">{t('请选择素材')}</option>{assets.filter(item => item.kind === 'image' || item.kind === 'video').map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
      </div><label>{t('形象授权依据')}<input maxLength={300} value={consent} placeholder={t('填写授权文件编号或可追溯的授权记录')} onChange={event => setConsent(event.target.value)} /></label>
        <label><input type="checkbox" checked={likeness} onChange={event => setLikeness(event.target.checked)} />{t('我确认有权使用此人物形象生成口播视频')}</label>
        <label><input type="checkbox" checked={reviewed} onChange={event => setReviewed(event.target.checked)} />{t('我已检查素材为单人、面部清晰且无遮挡')}</label>
      </fieldset>
      <button disabled={locked || !loaded || !name.trim() || !reference || consent.trim().length < 3 || !likeness || !reviewed} onClick={() => submit('/digital-humans', {name: name.trim(), reference_asset_id: reference, consent_reference: consent.trim(), likeness_confirmed: likeness, single_person_confirmed: reviewed}, 'profile')}>{t('保存授权形象')}</button>
    </details>
    <h3>{t('1. 选择形象')}</h3>
    <label>{t('已授权形象')}<select disabled={locked} value={avatarId} onChange={event => setAvatarId(event.target.value)}><option value="">{t('请选择形象')}</option>{avatars.map(item => <option disabled={!!item.revoked_at} key={item.id} value={item.id}>{item.name} · {item.route === 'photo' ? t('照片驱动') : t('视频改口型')}{item.revoked_at ? ` · ${t('已撤销')}` : ''}</option>)}</select></label>
    {avatar && <div className="editing-note"><p>{avatar.consent_reference}</p><p>{t(capability?.message ?? '尚未确认可用能力')}</p>{!avatar.revoked_at && <button disabled={locked} onClick={() => submit(`/digital-humans/${avatar.id}/revoke`, {}, 'revoke')}>{t('撤销此形象授权')}</button>}<p className="drama-help">{t('撤销后禁止新生成；已完成素材保留用于追溯，请另行处理已有成片。')}</p></div>}
    <h3>{t('2. 准备配音')}</h3>
    <p className="drama-help">{t('可以上传录音，或先用文案生成标准TTS配音。此流程不克隆声音；生成配音后需选择音频并确认口播生成。')}</p>
    <label>{t('口播文案')}<textarea maxLength={2000} disabled={locked} value={text} onChange={event => setText(event.target.value)} /></label>
    <RuntimeModelPicker space={space} capability="speech" preview={speechPreview} value={choice} onChange={setChoice} disabled={locked || active} />
    <button disabled={locked || !loaded || active || !speechReady || text.trim().length < 3} onClick={() => submit('/creative-jobs', {...speechInput, ...runtimeFields(choice), expected_provider: choice?.model.provider ?? caps.speech?.provider, expected_model: choice?.model.model ?? caps.speech?.model ?? ''}, 'job')}>{t('先生成配音（可能计费）')}</button>
    <label>{t('选择口播音频')}<select disabled={locked} value={audioId} onChange={event => {setAudioId(event.target.value); setVoiceConfirmed(false); setVoiceConsent('')}}><option value="">{t('选择音频')}</option>{assets.filter(item => item.kind === 'audio').map(item => <option key={item.id} value={item.id}>{item.name} · {item.duration.toFixed(1)}s</option>)}</select></label>
    {audio && <audio controls preload="none" src={`${baseURL}${audio.file_url}`} />}
    {audio && !validAudio && <p role="alert" className="error">{t('配音须在30秒以内，且不能长于参考视频。')}</p>}
    <fieldset disabled={locked}><label>{t('声音授权依据')}<input maxLength={300} value={voiceConsent} placeholder={t('录音授权记录或TTS声音使用依据')} onChange={event => setVoiceConsent(event.target.value)} /></label><label><input type="checkbox" checked={voiceConfirmed} onChange={event => setVoiceConfirmed(event.target.checked)} />{t('我确认有权使用所选配音制作此口播')}</label></fieldset>
    <h3>{t('3. 生成人物口播')}</h3>
    <button className="primary" disabled={locked || !loaded || active || !avatar || !!avatar.revoked_at || !capability?.ready || !validAudio || !voiceConfirmed || voiceConsent.trim().length < 3} onClick={() => submit('/creative-jobs', {kind: 'digital_human', expected_version: project.version, avatar_id: avatarId, audio_asset_id: audioId, prompt: text.trim() || avatar?.name + '口播', voice_confirmed: voiceConfirmed, voice_consent_reference: voiceConsent.trim(), expected_provider: capability?.provider, expected_model: capability?.model}, 'job')}>{active ? t('项目任务处理中…') : t('生成数字人口播')}</button>
    <p className="drama-help">{t('未配置引擎或未确认模型许可时不可生成。任务失败不会自动重试；技术检查通过不代表嘴型、人物一致性和中文发音已验收。')}</p>
    <div className="drama-history"><h3>{t('口播与配音任务')}</h3><ol>{jobs.filter(job => job.kind === 'digital_human' || job.kind === 'speech').map(job => {
      const output = assets.find(item => item.id === job.asset_id)
      return <li key={job.id}><div><span>{job.kind === 'digital_human' ? t('数字人口播') : t('配音')} · {t(jobLabels[job.status] ?? job.status)}</span>{(job.status === 'queued' || job.status === 'running') && <button disabled={locked} onClick={() => submit(`/creative-jobs/${job.id}/cancel`, {}, 'cancel')}>{t('取消创作任务')}</button>}</div>{job.error && <p className="error">{job.error}</p>}{output?.kind === 'video' && <video controls preload="metadata" src={`${baseURL}${output.file_url}`} />}{output?.kind === 'audio' && <button disabled={locked} onClick={() => {setAudioId(output.id); setVoiceConfirmed(false); setVoiceConsent('')}}>{t('使用此配音')}</button>}</li>
    })}</ol></div>
    <p className="drama-help">{t('加入时间轴后勾选“视频原音”以保留口播，或使用所选配音作为片段音轨。')}</p>
    <Link to={`${prefix}/media`} className="button-link">{t('进入编辑器添加字幕、背景与音乐')}</Link>
  </section>
}
