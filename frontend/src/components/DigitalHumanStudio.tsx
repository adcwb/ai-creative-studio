import {AvatarReviewPanel,type AvatarReview} from './AvatarReviewPanel'
import {readAvatarAttempt,saveAvatarAttempt,clearAvatarAttempt,type AvatarAttempt} from './avatarPending'
import {useAppWorkspace} from '../app/workspace'
import {VoiceAuthorizationPanel,type VoiceAuthorization} from './VoiceAuthorizationPanel'
import {usePolling} from '../hooks/usePolling'
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
interface Job {avatar_reviews?:AvatarReview[];reference_asset_id?:string;audio_asset_id?:string;avatar_recovery_phase?:string;voice_authorization_id?:string;error_code?:string;id: string; kind: string; status: string; asset_id?: string; error?: string; avatar_id?: string}
type Attempt=AvatarAttempt
const jobLabels: Record<string, string> = {queued: '排队中', running: '生成中', succeeded: '已完成', failed: '失败', cancelled: '已取消'}

export function DigitalHumanStudio({project, space, disabled, onDirtyChange}: {project: DramaProject; space: string; disabled: boolean; onDirtyChange: (dirty: boolean) => void}) {
  const {locale} = useI18n(),workspace=useAppWorkspace()
  const prefix = `/projects/${encodeURIComponent(project.id)}`
 const query=new URLSearchParams({space_id:space}).toString()
 const recoveryKey=`studio.avatar.pending:${workspace.session.user?.id??'demo'}:${space}:${project.id}`
 const [recovery]=useState(()=>readAvatarAttempt(recoveryKey,prefix,query))
 const [storageNotice,setStorageNotice]=useState('')
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
  const [voiceGrant,setVoiceGrant]=useState<VoiceAuthorization|null>(null)
 const [voicePending,setVoicePending]=useState(false)
 const [cloudTransfer,setCloudTransfer]=useState(false)
  const [choice, setChoice] = useState<RuntimeChoice | null>(null)
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(Boolean(recovery))
  const [error, setError] = useState('')
  const [readError, setReadError] = useState('')
  const [loaded, setLoaded] = useState(false)
  const [revision, setRevision] = useState(0)
  const pending = useRef<Attempt | null>(recovery)
  const flight = useRef(false)
  const live = useRef(true)
  const avatar = avatars.find(item => item.id === avatarId)
  const capability = caps[`digital_human_${avatar?.route ?? 'photo'}`]
  const active = jobs.some(job => job.status === 'queued' || job.status === 'running')
  const locked = disabled || busy || uncertain
  const speechInput = {kind: 'speech', expected_version: project.version, prompt: text.trim()}
  const speechPreview = {operation: 'creative' as const, project_id: project.id, input: speechInput}
  const speechReady = choice ? runtimeReady(choice, speechPreview) : caps.speech?.ready
  const audio = assets.find(item => item.id === audioId)
  const referenceAsset = assets.find(item => item.id === avatar?.reference_asset_id)
  const cloudAudioValid=capability?.provider!=='dashscope-s2v'||(audio?.duration??20)<20
  const validAudio = cloudAudioValid && audio?.kind === 'audio' && audio.duration > 0 && (avatar?.route !== 'video' || audio.duration <= (referenceAsset?.duration ?? 0) + .1)

  useEffect(() => {live.current = true; return () => {live.current = false}}, [])
  useEffect(() => {onDirtyChange(busy || uncertain || !!name || !!consent || !!text); return () => onDirtyChange(false)}, [busy, uncertain, name, consent, text, onDirtyChange])
  usePolling(async signal => {
    try {
      const [people, media, tasks, capabilities] = await Promise.all([
        request<Avatar[]>(`${prefix}/digital-humans?${query}`, {signal}),
        request<{assets: Asset[]}>(`${prefix}/media?${query}`, {signal}),
        request<Job[]>(`${prefix}/creative-jobs?${query}`, {signal}),
        request<Record<string, Capability>>('/creative-media-capabilities', {signal}),
      ])
      if (signal.aborted) return
      setAvatars(people); setAssets(media.assets); setJobs(tasks); setCaps(capabilities); setLoaded(true); setReadError('')
    } catch (failure) {
      if (!signal.aborted) {setLoaded(false); setReadError(errorMessage(failure, t('数字人数据读取失败')))}
      throw failure
    }
  }, 2500, `${prefix}?${query}:${revision}:${locale}`)

  async function execute() {
    const attempt = pending.current
    if (!attempt || flight.current) return
    flight.current = true; setBusy(true); setError('')
    try {
      const result = await request<Avatar | Job | {assets: Asset[]}>(attempt.path, {method: 'POST', headers: {'Content-Type': attempt.mime, 'Idempotency-Key': attempt.key}, body: attempt.body})
      if (!live.current) return
      pending.current = null; clearAvatarAttempt(recoveryKey);setUncertain(false)
      if (attempt.action === 'profile') {setAvatarId((result as Avatar).id); setName(''); setConsent(''); setLikeness(false); setReviewed(false)}
      if (attempt.action === 'job') {setText('');  setChoice(null)}
      setRevision(value => value + 1)
    } catch (failure) {
      if (live.current) {
        const unknown = uncertain || !(failure instanceof ApiError) || failure.status >= 500
        setUncertain(unknown)
        if (!unknown) {pending.current = null;clearAvatarAttempt(recoveryKey)}
        setError(unknown ? t('结果尚未确认，请重试同一请求。') : errorMessage(failure, t('操作失败')))
      }
    } finally {flight.current = false; if (live.current) setBusy(false)}
  }
  function submit(suffix: string, body: unknown, action: Attempt['action']) {
    if (locked || flight.current) return
    pending.current = {path: `${prefix}${suffix}?${query}`, body: JSON.stringify(body), mime: 'application/json', key: crypto.randomUUID(), action}
    if(!saveAvatarAttempt(recoveryKey,pending.current))setStorageNotice(t('浏览器无法保留恢复记录，请在离开前确认保存结果。'))
    void execute()
  }
  function upload(file?: File) {
    if (!file || locked || flight.current) return
    const mime = file.type === 'audio/x-wav' ? 'audio/wav' : file.type
    if (!['image/png', 'image/jpeg', 'video/mp4', 'audio/wav', 'audio/mpeg'].includes(mime)) {setError(t('请选择PNG、JPEG、MP4、WAV或MP3素材')); return}
    const fields = new URLSearchParams({space_id: space, filename: file.name, role: mime.startsWith('audio/') ? 'voice' : 'reference'})
    pending.current = {path: `${prefix}/media/uploads?${fields}`, body: file, mime, key: crypto.randomUUID(), action: 'upload'}
    void execute()
  }
  return <section className="panel digital-human-studio">
    <div className="section-heading"><div><span className="eyebrow">DIGITAL HUMAN</span><h2>{t('数字人口播')}</h2></div><span>{t('单人 · 固定机位 · 使用已授权配音')}</span></div>
    <p className="drama-help">{t('上传已授权形象，准备配音，再生成口播视频。照片驱动补充人物运动；视频改口型保留原动作。成片带有AI生成标识。')}</p>
    <p className="drama-source">{t('素材仅做格式、尺寸和时长检查；人脸数量、清晰度、遮挡及生成效果需要人工检查。')}</p>
    {readError && <p role="alert" className="error">{readError}</p>}
    {storageNotice&&<p role="status">{storageNotice}</p>}{uncertain&&!error&&<p role="status">{t('有待核对的原请求，输入已固定，请先恢复结果。')}<button disabled={busy} onClick={()=>void execute()}>{t('重试同一请求')}</button></p>}
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
    <label>{t('选择口播音频')}<select disabled={locked||voicePending} value={audioId} onChange={event => {const audio=event.target.value;workspace.requestLeave(()=>{setAudioId(audio);setVoiceGrant(null)})}}><option value="">{t('选择音频')}</option>{assets.filter(item => item.kind === 'audio').map(item => <option key={item.id} value={item.id}>{item.name} · {item.duration.toFixed(1)}s</option>)}</select></label>
    {audio && <audio controls preload="none" src={`${baseURL}${audio.file_url}`} />}
    {audio && !validAudio && <p role="alert" className="error">{t(!cloudAudioValid?'百炼单请求配音须不足20秒，请先制作短段；项目不限制素材总时长。':'配音须有有效时长，且不能长于参考视频。')}</p>}
    <VoiceAuthorizationPanel key={`${project.id}:${space}:${audioId}`} project={project.id} space={space} audio={audioId} value={voiceGrant} choose={setVoiceGrant} onBusy={setVoicePending} disabled={locked}/>
    <h3>{t('3. 生成人物口播')}</h3>
 {capability?.provider==='dashscope-s2v'&&<label><input type="checkbox" checked={cloudTransfer} disabled={locked} onChange={e=>setCloudTransfer(e.target.checked)}/>{t('我同意将所选形象与配音临时发送到部署方配置的私有对象存储及百炼数字人服务')}</label>}
 {capability?.provider==='dashscope-s2v'&&<p className="drama-help">{t('云素材读取链接24小时失效；副本删除依赖部署方对象存储生命周期和任务完成清理，撤销不能保证立即撤回云端计算。')} {t('百炼当前接入单段照片口播，原生480P或720P；照片边长400–7000像素，配音不足20秒且小于15MB。较长内容请先分段，不会自动截断或改用其他模型。')}</p>}
    <button className="primary" disabled={locked || !loaded || active || !avatar || !!avatar.revoked_at || !capability?.ready || !validAudio || voicePending || capability?.provider==='dashscope-s2v'&&!cloudTransfer || !voiceGrant || voiceGrant.audio_asset_id!==audioId || !!voiceGrant.revoked_at} onClick={() => submit('/creative-jobs', {kind: 'digital_human_authorized',...(capability?.provider==='dashscope-s2v'?{cloud_transfer_confirmed:cloudTransfer}:{}),voice_authorization_id:voiceGrant?.id, expected_version: project.version, avatar_id: avatarId, audio_asset_id: audioId, prompt: text.trim() || avatar?.name + '口播', voice_confirmed: true, voice_consent_reference: voiceGrant?.consent_reference, expected_provider: capability?.provider, expected_model: capability?.model}, 'job')}>{active ? t('项目任务处理中…') : t('生成数字人口播')}</button>
    <p className="drama-help">{t('未配置引擎或未确认模型许可时不可生成。任务失败不会自动重试；技术检查通过不代表嘴型、人物一致性和中文发音已验收。')}</p>
    <div className="drama-history"><h3>{t('口播与配音任务')}</h3><ol>{jobs.filter(job => (job.kind === 'digital_human'||job.kind==='digital_human_authorized') || job.kind === 'speech').map(job => {
      const output = assets.find(item => item.id === job.asset_id)
      return <li key={job.id}><div><span>{(job.kind === 'digital_human'||job.kind==='digital_human_authorized') ? t('数字人口播') : t('配音')} · {t(jobLabels[job.status] ?? job.status)}</span>{(job.status === 'queued' || job.status === 'running') && <button disabled={locked} onClick={() => submit(`/creative-jobs/${job.id}/cancel`, {}, 'cancel')}>{t('取消创作任务')}</button>}</div>{job.error && <p className="error">{job.error}</p>}{['failed','cancelled'].includes(job.status)&&['engine_done','composed','remote_pending'].includes(job.avatar_recovery_phase??'')&&<div><p className="drama-help">{t(job.avatar_recovery_phase==='remote_pending'?'恢复只查询原百炼任务并取回结果，不再次提交生成；仍检查当前授权。':'恢复只使用已校验的完成结果，继续合成或入库，不重新启动引擎；仍检查当前授权。')}</p><button disabled={locked} onClick={()=>submit(`/creative-jobs/${job.id}/resume`,{},'resume')}>{t(job.avatar_recovery_phase==='remote_pending'?'核对原云端口播任务':'恢复已确认口播结果')}</button></div>}{job.avatar_recovery_phase==='dispatching'&&['failed','cancelled'].includes(job.status)&&<p className="editing-note">{t('引擎执行结果尚未确认，未自动重新生成。请先核对执行器记录。')}</p>}{output?.kind === 'video' && <video controls preload="metadata" src={`${baseURL}${output.file_url}`} />}{output?.kind==='video'&&job.status==='succeeded'&&<AvatarReviewPanel project={project.id} job={job.id} asset={output.id} reviews={job.avatar_reviews??[]} reference={assets.find(a=>a.id===job.reference_asset_id)} audio={assets.find(a=>a.id===job.audio_asset_id)} disabled={locked}/>} {output?.kind === 'audio' && <button disabled={locked} onClick={() => {setAudioId(output.id); setVoiceGrant(null)}}>{t('使用此配音')}</button>}</li>
    })}</ol></div>
    <p className="drama-help">{t('加入时间轴后勾选“视频原音”以保留口播，或使用所选配音作为片段音轨。')}</p>
    <Link to={`${prefix}/media`} className="button-link">{t('进入编辑器添加字幕、背景与音乐')}</Link>
  </section>
}
