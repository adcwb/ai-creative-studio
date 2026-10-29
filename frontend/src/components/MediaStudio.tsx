import { translate as t, useI18n } from "../i18n"
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, baseURL, errorMessage, request } from '../api'
import { SubtitleAlignment, type AlignmentCapability, type AlignmentTask, type SubtitleFields } from './SubtitleAlignment'
import { RuntimeModelPicker } from './RuntimeModelPicker'
import { runtimeFields, runtimeReady, type RuntimeChoice } from '../runtimeSelection'
import { MaskEditor } from './MaskEditor'
import { useAppWorkspace } from '../app/workspace'
import { ConfirmDialog } from './ConfirmDialog'
import { TimelinePreview } from './TimelinePreview'
import { PosterEditor } from './PosterEditor'
import { VideoOverlayEditor } from './VideoOverlayEditor'
import { compositionError, maxTransitionDuration, timelineDuration, clipStarts, splitClip, replaceClipVisual, type Transition, type VideoOverlay } from '../mediaTimeline'
import type { DramaProject } from '../drama'

interface MediaAsset { id: string; kind: 'image' | 'audio' | 'video'; mime_type: string; name: string; role: string; character: string; shot_id: string; duration: number; source: string; file_url: string }
interface Clip extends SubtitleFields { shot_id?: string; source_audio_asset_id?: string; source_audio_trim_start?: number; id: string; asset_id: string; duration: number; trim_start: number; subtitle: string; audio_asset_id: string; audio_volume: number; preserve_source_audio?: boolean; source_audio_volume?: number; transition?: Transition; transition_duration?: number }
interface AudioTrack { id: string; asset_id: string; start: number; trim_start: number; duration: number; volume: number }
interface Timeline { episode_number?: number; clips: Clip[]; music_asset_id: string; music_volume: number; output_resolution?: '360p' | '720p' | '1080p' | '2160p'; aspect_ratio?: '16:9' | '9:16' | '1:1'; subtitle_mode?: 'soft' | 'burn' | 'none'; audio_tracks?: AudioTrack[]; video_overlays?: VideoOverlay[] }
interface Export { episode_number?: number; timeline?: Timeline; id: string; status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'; file_url?: string; subtitle_url?: string; subtitle_vtt_url?: string; error?: string; duration?: number }
interface MediaState { episode_timelines?: Record<string, Timeline>; selections?: {id: string; shot_id: string; asset_id: string}[]; version: number; assets: MediaAsset[]; timeline: Timeline; exports: Export[] }
interface Attempt { path: string; method: string; body: BodyInit; type: string; key: string }
const emptyTimeline: Timeline = {clips: [], music_asset_id: '', music_volume: 0.2}
function mediaURL(url: string) { return url.startsWith('/') ? `${baseURL}${url}` : '' }

type StudioProps = {project: DramaProject; space: string; onDirtyChange: (dirty: boolean) => void}
function episodeTimeline(state: MediaState, episode: number): Timeline {return episode === 1 ? state.timeline : state.episode_timelines?.[String(episode)] ?? {...emptyTimeline, episode_number: episode}}
export function MediaStudio(props: StudioProps) {
  const {t} = useI18n()
  const {requestLeave} = useAppWorkspace()
  const [episode, setEpisode] = useState(1)
  const [number, setNumber] = useState(2)
  const [saved, setSaved] = useState<number[]>([])
  useEffect(() => {const controller = new AbortController(); void request<MediaState>(`/projects/${props.project.id}/media?${new URLSearchParams({space_id: props.space})}`, {signal: controller.signal}).then(data => setSaved(Object.keys(data.episode_timelines ?? {}).map(Number))).catch(() => {}); return () => controller.abort()}, [props.project.id, props.space])
  const episodes = [...new Set([1, episode, ...saved, ...(props.project.script?.scenes.map(s => s.episode_number ?? 1) ?? [])])].sort((a,b) => a-b)
  function openEpisode(value: number) {if (Number.isInteger(value) && value>=1 && value<=999) requestLeave(() => {setSaved(old => [...new Set([...old, value])]); setEpisode(value)})}
  return <><div className="episode-toolbar"><label>{t('当前剪辑集数')}<select aria-label={t('当前剪辑集数')} value={episode} onChange={e => openEpisode(Number(e.target.value))}>{episodes.map(n => <option key={n} value={n}>{t('第 {p0} 集', {p0:n})}</option>)}</select></label><label>{t('打开其他集')}<input type="number" min="1" max="999" value={number} onChange={e => setNumber(Number(e.target.value))} /></label><button disabled={!Number.isInteger(number) || number<1 || number>999} onClick={() => openEpisode(number)}>{t('进入该集')}</button><p>{t('素材在项目内共享，每集时间轴和导出独立保存。')}</p></div><EpisodeMediaStudio key={episode} {...props} episode={episode} /></>
}
function EpisodeMediaStudio({project, space, onDirtyChange, episode}: StudioProps & {episode: number}) {
  const {formatNumber} = useI18n()

  const [alignmentError, setAlignmentError] = useState('')
  const [asrChoices, setAsrChoices] = useState<Record<string, RuntimeChoice | null>>({})
  const [alignments, setAlignments] = useState<AlignmentTask[]>([])
  const [baseAlignmentCaps, setAlignmentCaps] = useState<Partial<Record<'estimate' | 'asr' | 'forced' | 'transcription', AlignmentCapability>>>({})

  const [state, setState] = useState<MediaState | null>(null)
  const [baselineVersion, setBaselineVersion] = useState(0)
  function alignmentPreview(clip: Clip) {return {operation: 'alignment' as const, project_id: project.id, input: {...(episode>1 ? {episode_number: episode} : {}), clip_id: clip.id, audio_asset_id: clip.audio_asset_id, mode: asrChoices[clip.id]?.model.model === 'gpt-transcribe' ? 'transcription' : 'asr', expected_version: baselineVersion}}}
  function alignmentCapabilities(clip: Clip) {
    const choice = asrChoices[clip.id]
    return choice ? {...baseAlignmentCaps, asr: undefined, [choice.model.model === 'gpt-transcribe' ? 'transcription' : 'asr']: {provider: choice.model.provider, model: choice.model.model, mode: 'real', ready: runtimeReady(choice, alignmentPreview(clip)), message: t('已选择组织模型，提交前须确认费用上限。')}} : baseAlignmentCaps
  }

  const [posterDirty, setPosterDirty] = useState(false)
  const [playhead, setPlayhead] = useState(0)
  const [timeline, setTimeline] = useState<Timeline>(emptyTimeline)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [confirmDemo, setConfirmDemo] = useState(false)
  const [role, setRole] = useState('shot')
  const [character, setCharacter] = useState('')
  const [shotId, setShotId] = useState('')
  const [maskSource, setMaskSource] = useState<MediaAsset | null>(null)
  const [photo, setPhoto] = useState<File | null>(null)
  const [rotation, setRotation] = useState(0)
  const [square, setSquare] = useState(false)
  const pending = useRef<Attempt | null>(null)
  const inFlight = useRef(false)
  const alive = useRef(true)
  const dirty = state !== null && JSON.stringify(timeline) !== JSON.stringify(episodeTimeline(state, episode))
  const dirtyRef = useRef(false)
  const path = `/projects/${encodeURIComponent(project.id)}/media`
  const query = new URLSearchParams({space_id: space}).toString()
  useEffect(() => {dirtyRef.current = dirty}, [dirty])
  useEffect(() => {onDirtyChange(dirty || posterDirty || busy || uncertain || maskSource !== null); return () => onDirtyChange(false)}, [dirty, posterDirty, busy, uncertain, maskSource, onDirtyChange])
  const load = useCallback(async (replace = false) => {
    const result = await request<MediaState>(`${path}?${query}`)
    if (!alive.current) return
    setState(result)
    if (replace || !dirtyRef.current) {setTimeline(episodeTimeline(result, episode)); setBaselineVersion(result.version)}
    try {const tasks = await request<AlignmentTask[]>(`${path}/alignments?${query}`); if (alive.current) {setAlignments(tasks); setAlignmentError('')}} catch (failure) {if (alive.current) setAlignmentError(errorMessage(failure, t("字幕对齐记录读取失败")))}
  }, [path, query, episode])
  useEffect(() => {
    alive.current = true
    void request<Partial<Record<'estimate' | 'asr' | 'forced' | 'transcription', AlignmentCapability>>>('/alignment-capabilities').then(result => {if (alive.current) setAlignmentCaps(result)}).catch(() => {if (alive.current) setAlignmentCaps({asr: {provider: 'unavailable', mode: 'unavailable', ready: false, message: t("无法读取识别配置，请刷新后重试")}})})
    void load().catch(failure => {if (alive.current) setError(errorMessage(failure, t("媒体读取失败")))})
    const timer = setInterval(() => {if (!inFlight.current) void load().catch(() => {})}, 2000)
    return () => {alive.current = false; clearInterval(timer)}
  }, [load])
  async function execute(attempt: Attempt) {
    if (inFlight.current) return
    inFlight.current = true; setBusy(true); setError(''); setNotice('')
    try {
      await request(`${attempt.path}`, {method: attempt.method, headers: {'Content-Type': attempt.type, 'Idempotency-Key': attempt.key}, body: attempt.body})
      if (!alive.current) return
      pending.current = null; setUncertain(false)
      setNotice(t("操作已保存。生成与导出状态以服务端记录为准。"))
      const replace = attempt.path.includes('/timeline?') || attempt.path.includes('/demo?')
      try {await load(replace)} catch (failure) {setError(t("操作已被接受，媒体刷新失败：{p0}", {p0: errorMessage(failure, t('重新读取媒体'))}))}
    } catch (failure) {
      if (!alive.current) return
      const unknown = !(failure instanceof ApiError) || failure.status >= 500
      setUncertain(unknown)
      setError(unknown ? t("结果未确认，请用原请求重试。不会自动创建第二次生成。") : errorMessage(failure, t("媒体操作失败")))
    } finally {inFlight.current = false; if (alive.current) setBusy(false)}
  }
  function mutate(suffix: string, data: unknown, method = 'POST') {
    if (busy || uncertain) return
    const attempt = {path: `${path}${suffix}?${query}`, method, body: JSON.stringify(data), type: 'application/json', key: crypto.randomUUID()}
    pending.current = attempt; void execute(attempt)
  }
  function upload(file: Blob, name: string, assetRole = role) {
    if (busy || uncertain) return
    if (file.size > 32 * 1024 * 1024) {setError(t("文件不能超过 32 MiB。")); return}
    const params = new URLSearchParams({space_id: space, filename: name, role: assetRole, character, shot_id: shotId})
    const attempt = {path: `${path}/uploads?${params}`, method: 'POST', body: file, type: file.type, key: crypto.randomUUID()}
    pending.current = attempt; void execute(attempt)
  }
  async function editPhoto() {
    if (!photo) return
    if (photo.size > 32 * 1024 * 1024) {setError(t("照片不能超过 32 MiB。")); return}
    try {
      const bitmap = await createImageBitmap(photo)
      if (bitmap.width * bitmap.height > 20000000) {bitmap.close(); throw new Error(t("图片像素过大，请选择不超过 2000 万像素的照片。"))}
      const size = square ? Math.min(bitmap.width, bitmap.height) : 0
      const width = size || bitmap.width, height = size || bitmap.height
      const canvas = document.createElement('canvas'); canvas.width = rotation % 180 ? height : width; canvas.height = rotation % 180 ? width : height
      const context = canvas.getContext('2d')!
      context.translate(canvas.width / 2, canvas.height / 2); context.rotate(rotation * Math.PI / 180)
      context.drawImage(bitmap, (bitmap.width - width) / 2, (bitmap.height - height) / 2, width, height, -width / 2, -height / 2, width, height); bitmap.close()
      const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob(value => value ? resolve(value) : reject(new Error(t("无法编辑照片"))), 'image/png'))
      upload(blob, `edited-${photo.name.replace(/\.[^.]+$/, '')}.png`)
    } catch (failure) {setError(errorMessage(failure, t("照片编辑失败")))}
  }
  function updateClip(index: number, patch: Partial<Clip>) {
    const current = timeline.clips[index]
    const affectsTiming = (patch.duration !== undefined && patch.duration !== current.duration) || (patch.trim_start !== undefined && patch.trim_start !== current.trim_start) || (patch.audio_asset_id !== undefined && patch.audio_asset_id !== current.audio_asset_id)
    setTimeline({...timeline, clips: timeline.clips.map((clip, i) => i === index ? {...clip, ...patch, ...(affectsTiming && clip.subtitle_cues?.length ? {subtitle_source: 'manual' as const} : {})} : clip)})
  }
  function updateTrack(index: number, patch: Partial<AudioTrack>) {setTimeline({...timeline, audio_tracks: timeline.audio_tracks?.map((track, i) => i === index ? {...track, ...patch} : track)})}
  function move(index: number, delta: number) {const clips = [...timeline.clips]; [clips[index], clips[index + delta]] = [clips[index + delta], clips[index]]; setTimeline({...timeline, clips})}
  function split(index: number) {
    const clip = timeline.clips[index]
    const parts = splitClip(clip, playhead - clipStarts(timeline.clips)[index], visual.find(asset => asset.id === clip.asset_id)?.kind === 'video', crypto.randomUUID())
    if (parts && timeline.clips.length < 30) setTimeline({...timeline, clips: [...timeline.clips.slice(0, index), ...parts, ...timeline.clips.slice(index + 1)]})
  }
  const locked = busy || uncertain
  const visual = state?.assets.filter(asset => asset.kind !== 'audio') ?? []
  const audio = state?.assets.filter(asset => asset.kind === 'audio') ?? []
  const duration = timelineDuration(timeline.clips)
  const outputLimit = ({'360p': 180, '720p': 120, '1080p': 60, '2160p': 15})[timeline.output_resolution ?? '360p']
  const outputInvalid = !Number.isFinite(duration) || duration > outputLimit
  const compositionProblem = compositionError(timeline.clips, timeline.video_overlays ?? [], state?.assets ?? [], timeline.audio_tracks ?? [])
  return <section className="panel media-studio" aria-labelledby="media-heading">
    <div className="section-heading"><div><span className="eyebrow">04 / BRING IT TO LIFE</span><h2 id="media-heading">{t("素材与成片")}</h2></div><span>{t('本地合成 · 媒体版本 {version}', {version: state?.version ?? '—'})}</span></div>
    <p className="drama-help">{t("上传照片、镜头视频、配音或音乐，编排时间轴并导出 MP4。角色参考只保存关联与创作约束，无法保证跨镜头外观一致。真实模型效果与费用尚未验证。")}</p>
    {error && <div className="error" role="alert">{error}{uncertain ? <button disabled={busy} onClick={() => {if (pending.current) void execute(pending.current)}}>{t("原请求重试")}</button> : <button disabled={busy} onClick={() => {void load().then(() => setError('')).catch(failure => setError(errorMessage(failure, t("读取失败"))))}}>{t("重新读取媒体")}</button>}</div>}
    {notice && <p className="notice" role="status">{notice}</p>}
    {!state ? <p role="status">{t("正在读取媒体…")}</p> : <>
      <details className="media-upload" open><summary>{t("上传与照片编辑")}</summary><fieldset disabled={locked}>
        <div className="storyboard-fields"><div><label htmlFor="media-role">{t("素材用途")}</label><select id="media-role" value={role} onChange={event => setRole(event.target.value)}><option value="shot">{t("镜头素材")}</option><option value="reference">{t("角色参考")}</option><option value="voice">{t("配音")}</option><option value="music">{t("音乐")}</option><option value="sfx">{t("音效")}</option></select></div>
        <div><label htmlFor="media-character">{t("关联角色")}</label><select id="media-character" value={character} onChange={event => setCharacter(event.target.value)}><option value="">{t("不关联角色")}</option>{project.script?.characters.map(item => <option key={item.name}>{item.name}</option>)}</select></div>
        <div><label htmlFor="media-shot">{t("关联镜头")}</label><select id="media-shot" value={shotId} onChange={event => setShotId(event.target.value)}><option value="">{t("不关联镜头")}</option>{project.storyboard?.shots.map((shot, index) => <option key={shot.id} value={shot.id}>{t("第")}{index + 1} {t("镜 ·")}{shot.description.slice(0, 25)}</option>)}</select></div>
        <div><label htmlFor="media-file">{t("上传素材（PNG/JPEG/WAV/MP3/MP4）")}</label><input id="media-file" type="file" accept="image/png,image/jpeg,audio/wav,audio/mpeg,video/mp4" onChange={event => {const file = event.target.files?.[0]; if (file) upload(file, file.name); event.target.value = ''}} /></div></div>
        <label htmlFor="photo-edit">{t("选择待编辑照片")}</label><input id="photo-edit" type="file" accept="image/png,image/jpeg" onChange={event => {setPhoto(event.target.files?.[0] ?? null); setRotation(0)}} />
        {photo && <div className="media-actions"><span>{photo.name}</span><label>{t("旋转")}<select aria-label={t("照片旋转")} value={rotation} onChange={event => setRotation(Number(event.target.value))}>{[0, 90, 180, 270].map(value => <option key={value} value={value}>{value}°</option>)}</select></label><label><input type="checkbox" checked={square} onChange={event => setSquare(event.target.checked)} /> {t("居中裁剪为正方形")}</label><button onClick={() => void editPhoto()}>{t("保存编辑照片为新素材")}</button></div>}
      </fieldset></details>
      {maskSource && <MaskEditor key={maskSource.id} url={mediaURL(maskSource.file_url)} name={maskSource.name} disabled={locked} onSave={(blob, name) => upload(blob, name, 'reference')} onClose={() => setMaskSource(null)} />}
      {!!state.selections?.length && <section className="adopted-assets"><h3>{t("已采用的镜头")}</h3><p>{t("选择加入剪辑；采用记录不会自动覆盖时间轴。")}</p>{Array.from(new Map(state.selections.map(s => [s.shot_id, s])).values()).map(selection => {const asset = state.assets.find(a => a.id === selection.asset_id); if (!asset || asset.kind === 'audio') return null; return <button key={selection.id} disabled={locked || timeline.clips.length >= 30} onClick={() => setTimeline({...timeline, clips: [...timeline.clips, {id: crypto.randomUUID(), asset_id: asset.id, shot_id: selection.shot_id, duration: Math.min(asset.duration || 3, 30), trim_start: 0, subtitle: '', audio_asset_id: '', audio_volume: 1}]})}>{t("加入时间轴")} · {asset.name}</button>})}</section>}
      <div className="media-assets">{state.assets.map(asset => <article key={asset.id}>{asset.kind === 'image' ? <img src={mediaURL(asset.file_url)} alt={asset.name} loading="lazy" /> : asset.kind === 'video' ? <video src={mediaURL(asset.file_url)} controls preload="metadata" /> : <audio src={mediaURL(asset.file_url)} controls preload="metadata" />}<strong>{asset.name}</strong><p>{asset.role} {asset.character} · {asset.source}</p>{asset.shot_id && <p>{t("已关联镜头")}{(project.storyboard?.shots.findIndex(shot => shot.id === asset.shot_id) ?? -1) + 1}</p>}{asset.kind === 'image' && <button disabled={locked} onClick={() => setMaskSource(asset)}>{t("绘制局部编辑遮罩")}</button>}{asset.kind !== 'audio' && <button disabled={locked || timeline.clips.length >= 30} onClick={() => setTimeline({...timeline, clips: [...timeline.clips, {id: crypto.randomUUID(), asset_id: asset.id, shot_id: asset.shot_id, duration: Math.min(asset.duration || 3, 30), trim_start: 0, subtitle: '', audio_asset_id: '', audio_volume: 1}]})}>{t("加入时间轴")}</button>}</article>)}</div>
      {state.assets.length === 0 && <p className="drama-help">{t("还没有媒体。上传素材，或使用明确标注的合成演示验证本地成片。")}</p>}
      <button disabled={locked || dirty || episode !== 1} onClick={() => {if (timeline.clips.length) setConfirmDemo(true); else mutate('/demo', {expected_version: state.version})}}>{t("创建合成演示素材与时间轴")}</button>
      {alignmentError && <p className="error" role="alert">{t("字幕对齐记录读取失败：")}{alignmentError}{t("。媒体编辑仍可使用，正在自动重试读取。")}</p>}
      <PosterEditor projectId={project.id} space={space} onDirtyChange={setPosterDirty} />
      <TimelinePreview clips={timeline.clips} overlays={timeline.video_overlays ?? []} assets={state.assets} aspect={timeline.aspect_ratio ?? '16:9'} subtitleMode={timeline.subtitle_mode ?? 'soft'} time={playhead} onTime={setPlayhead} audioTracks={timeline.audio_tracks} musicAsset={timeline.music_asset_id} musicVolume={timeline.music_volume} />
      <p className="drama-help">{t("替换画面保留片段时长、字幕、配音和原音来源；原素材不会被删除。")}</p>
      <h3>{t("可编辑时间轴")}<small>{formatNumber(duration, {maximumFractionDigits: 2})} {t("秒")}</small></h3>
      <p className="drama-help">{t("溶解、擦除、推移和圆形转场与下一镜重叠并缩短成片，镜头字幕与配音随镜头一起移动；重叠区可同时出现字幕。附加音轨与叠层位置按缩短后的成片计时。黑场转场不缩短时长。原音须明确开启；请降低混音音量，保存并导出后检查实际效果。")}</p>
      <fieldset disabled={locked}>
      <div className="storyboard-fields"><label>{t("输出清晰度")}<select value={timeline.output_resolution || '360p'} onChange={event => setTimeline({...timeline, output_resolution: event.target.value as Timeline['output_resolution']})}><option value="360p">{t("360p · 最长180秒")}</option><option value="720p">{t("720p · 最长120秒")}</option><option value="1080p">{t("1080p · 最长60秒")}</option><option value="2160p">{t("4K · 最长15秒")}</option></select></label><label>{t("成片画幅")}<select value={timeline.aspect_ratio || '16:9'} onChange={event => setTimeline({...timeline, aspect_ratio: event.target.value as Timeline['aspect_ratio']})}><option value="16:9">{t("16:9 横屏")}</option><option value="9:16">{t("9:16 竖屏")}</option><option value="1:1">{t("1:1 方形")}</option></select></label><label>{t("字幕输出")}<select value={timeline.subtitle_mode || 'soft'} onChange={event => setTimeline({...timeline, subtitle_mode: event.target.value as Timeline['subtitle_mode']})}><option value="soft">{t("可选字幕轨")}</option><option value="burn">{t("烧录到画面")}</option><option value="none">{t("不输出字幕")}</option></select></label></div>
      <p className="drama-help">{t("播放头位于片段内部且两侧至少0.5秒时可拆分。单镜配音请先移至独立音轨再拆分；字幕按区间裁剪并标为人工编辑。复制与重排后请检查转场、叠层和音轨范围。")}</p>
      <div className="media-timeline">{timeline.clips.map((clip, index) => <article key={clip.id}>
        <div className="media-actions"><strong>{t("片段")}{index + 1}</strong><button disabled={timeline.clips.length >= 30} onClick={() => setTimeline({...timeline, clips: [...timeline.clips.slice(0, index + 1), {...structuredClone(clip), id: crypto.randomUUID(), subtitle_source: 'manual', subtitle_alignment_id: ''}, ...timeline.clips.slice(index + 1)]})}>{t('复制片段 {index}', {index: index + 1})}</button><button disabled={timeline.clips.length >= 30 || !!clip.audio_asset_id || playhead - clipStarts(timeline.clips)[index] < .5 || clip.duration - (playhead - clipStarts(timeline.clips)[index]) < .5} onClick={() => split(index)}>{t('在播放头拆分片段 {index}', {index: index + 1})}</button><button disabled={index === 0 || locked} onClick={() => move(index, -1)}>{t("上移片段")}{index + 1}</button><button disabled={index === timeline.clips.length - 1 || locked} onClick={() => move(index, 1)}>{t("下移片段")}{index + 1}</button><button onClick={() => setTimeline({...timeline, clips: timeline.clips.filter((_, i) => i !== index)})}>{t("移除片段")}{index + 1}</button></div>
        <div className="storyboard-fields"><label>{t("画面素材")}<select aria-label={t("片段 {p0} 画面素材", {p0: index + 1})} value={clip.asset_id} onChange={event => {const asset = visual.find(a => a.id === event.target.value); const next = asset && replaceClipVisual(clip, asset); if (next) setTimeline({...timeline, clips: timeline.clips.map((item, i) => i === index ? next : item)}); else setError(t('替换素材时长不足，原片段保持不变。'))}}>{visual.map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label><label>{t("时长（秒）")}<input aria-label={t("片段 {p0} 时长", {p0: index + 1})} type="number" min="0.5" max="30" step="0.1" value={clip.duration} onChange={event => updateClip(index, {duration: Number(event.target.value)})} /></label><label>{t("视频起点（秒）")}<input aria-label={t("片段 {p0} 起点", {p0: index + 1})} type="number" min="0" step="0.1" value={clip.trim_start} onChange={event => updateClip(index, {trim_start: Number(event.target.value)})} /></label><label>{t("配音 / 音效")}<select aria-label={t("片段 {p0} 音频", {p0: index + 1})} value={clip.audio_asset_id} onChange={event => updateClip(index, {audio_asset_id: event.target.value})}><option value="">{t("无")}</option>{audio.map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label><label>{t("配音音量")}<input aria-label={t("片段 {p0} 音量", {p0: index + 1})} type="number" min="0" max="1" step="0.1" value={clip.audio_volume} onChange={event => updateClip(index, {audio_volume: Number(event.target.value)})} /></label></div>
        <div className="storyboard-fields"><label><input type="checkbox" checked={clip.preserve_source_audio ?? false} disabled={!clip.source_audio_asset_id && visual.find(asset => asset.id === clip.asset_id)?.kind !== 'video'} onChange={event => updateClip(index, {preserve_source_audio: event.target.checked, source_audio_volume: clip.source_audio_volume ?? 1, ...(!event.target.checked ? {source_audio_asset_id: '', source_audio_trim_start: 0} : {})})} /> {t("保留片段")}{index + 1} {t("视频原音")}</label><label>{t("原音音量")}<input aria-label={t("片段 {p0} 原音音量", {p0: index + 1})} type="number" min="0" max="1" step="0.1" value={clip.source_audio_volume ?? 1} onChange={event => updateClip(index, {source_audio_volume: Number(event.target.value)})} /></label><label>{t("转场")}<select aria-label={t("片段 {p0} 转场", {p0: index + 1})} value={clip.transition || ''} onChange={event => updateClip(index, {transition: event.target.value as Clip['transition'], transition_duration: event.target.value ? Math.min(0.5, maxTransitionDuration(timeline.clips, index, event.target.value as Clip['transition'])) : 0})}><option value="">{t("直接切换")}</option><option value="fade">{t("淡入淡出黑场")}</option><option value="crossfade" disabled={index === timeline.clips.length - 1}>{t("与下一镜交叉溶解")}</option><option value="wipeleft" disabled={index === timeline.clips.length - 1}>{t('向左擦除')}</option><option value="wiperight" disabled={index === timeline.clips.length - 1}>{t('向右擦除')}</option><option value="slideleft" disabled={index === timeline.clips.length - 1}>{t('向左推移')}</option><option value="slideright" disabled={index === timeline.clips.length - 1}>{t('向右推移')}</option><option value="circleopen" disabled={index === timeline.clips.length - 1}>{t('圆形展开')}</option></select></label>{!!clip.transition && <label>{t("转场时长（秒）")}<input aria-label={t("片段 {p0} 转场时长", {p0: index + 1})} type="number" min="0.1" max={maxTransitionDuration(timeline.clips, index)} step="0.1" value={clip.transition_duration ?? 0.5} onChange={event => updateClip(index, {transition_duration: Number(event.target.value)})} /></label>}</div>
        <label>{t("字幕")}<textarea aria-label={t("片段 {p0} 字幕", {p0: index + 1})} maxLength={300} value={clip.subtitle} onChange={event => updateClip(index, {subtitle: event.target.value})} /></label>
        <RuntimeModelPicker space={space} capability="asr" preview={alignmentPreview(clip)} value={asrChoices[clip.id] ?? null} onChange={value => setAsrChoices(previous => ({...previous, [clip.id]: value}))} disabled={locked || dirty} />
        <SubtitleAlignment index={index} clip={clip} tasks={alignments.filter(task => task.clip_id === clip.id && (task.episode_number ?? 1) === episode)} capabilities={alignmentCapabilities(clip)} locked={locked} dirty={dirty} version={state.version} onChange={patch => updateClip(index, patch)} onCreate={mode => {const caps = alignmentCapabilities(clip)[mode]; if (caps?.ready) mutate('/alignments', {...((mode === 'asr' || mode === 'transcription') ? runtimeFields(asrChoices[clip.id] ?? null) : {}), ...(episode>1 ? {episode_number: episode} : {}), clip_id: clip.id, audio_asset_id: clip.audio_asset_id, mode, ...(mode !== 'asr' && mode !== 'transcription' ? {text: clip.subtitle} : {}), expected_version: baselineVersion, expected_provider: caps.provider, expected_model: caps.model ?? ''})}} onCancel={task => mutate(`/alignments/${task.id}/cancel`, {})} onApply={task => {if (task.mode !== 'transcription' && task.source_version === state.version && task.clip_id === clip.id) updateClip(index, {subtitle_cues: structuredClone(task.cues), subtitle_source: task.mode === 'asr' ? 'asr' : task.mode === 'forced' ? 'forced' : 'estimated', subtitle_alignment_id: task.id})}} />
      </article>)}</div>
      <div className="storyboard-fields"><label>{t("背景音乐")}<select value={timeline.music_asset_id} onChange={event => setTimeline({...timeline, music_asset_id: event.target.value})}><option value="">{t("无背景音乐")}</option>{audio.map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label><label>{t("音乐音量")}<input type="number" min="0" max="1" step="0.1" value={timeline.music_volume} onChange={event => setTimeline({...timeline, music_volume: Number(event.target.value)})} /></label></div>
      <VideoOverlayEditor layers={timeline.video_overlays ?? []} assets={state.assets} duration={duration} onChange={video_overlays => setTimeline({...timeline, video_overlays})} />
      <h3>{t("附加音轨")}</h3><p className="drama-help">{t("最多8条配音、音乐或音效。起点相对于整部成片；每条可独立裁剪和调整音量，不循环。")}</p>
      {(timeline.audio_tracks ?? []).map((track, index) => <div className="storyboard-fields" key={track.id}><label>{t("音轨")}{index + 1} {t("素材")}<select value={track.asset_id} onChange={event => updateTrack(index, {asset_id: event.target.value})}>{audio.map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label>{(['start', 'trim_start', 'duration', 'volume'] as const).map(field => <label key={field}>{{start: t("成片起点（秒）"), trim_start: t("音源起点（秒）"), duration: t("使用时长（秒）"), volume: t("音量")}[field]}<input aria-label={t("音轨 {p0} {p1}", {p0: index + 1, p1: field})} type="number" min={field === 'duration' ? 0.1 : 0} max={field === 'volume' ? 1 : 180} step="0.1" value={track[field]} onChange={event => updateTrack(index, {[field]: Number(event.target.value)})} /></label>)}<button onClick={() => setTimeline({...timeline, audio_tracks: timeline.audio_tracks?.filter((_, i) => i !== index)})}>{t("移除音轨")}{index + 1}</button></div>)}
      <button disabled={!audio.length || duration < .1 || (timeline.audio_tracks?.length ?? 0) >= 8} onClick={() => setTimeline({...timeline, audio_tracks: [...(timeline.audio_tracks ?? []), {id: crypto.randomUUID(), asset_id: audio[0].id, start: 0, trim_start: 0, duration: Math.min(audio[0].duration || 3, duration || 3), volume: 0.5}]})}>{t("添加音轨")}</button></fieldset>
      {outputInvalid && <p className="error" role="alert">{t('当前输出规格最多 {seconds} 秒，请缩短时间轴或选择较低清晰度。', {seconds: outputLimit})}</p>}
      {compositionProblem && <p className="error" role="alert">{compositionProblem === 'transition' ? t('转场或镜头时长无效。重叠转场需要下一镜，时长0.1–2秒且不超过相邻镜头各自一半；移除或调整镜头后请检查转场。') : compositionProblem === 'audio' ? t('附加音轨超出成片或素材范围，请根据交叉溶解后的时长调整。') : t('画面叠层无效。请检查素材、0–1位置和大小、原音设置，以及缩短后成片中的起点和时长。')}</p>}
      <div className="media-actions"><button disabled={locked || !dirty} onClick={() => {setTimeline(episodeTimeline(state, episode)); setBaselineVersion(state.version)}}>{t("取消时间轴编辑")}</button><button disabled={locked || !dirty || !!compositionProblem || outputInvalid || timeline.clips.length === 0} onClick={() => mutate('/timeline', {expected_version: baselineVersion, timeline}, 'PUT')}>{t("保存时间轴")}</button><button className="primary" disabled={locked || dirty || !!compositionProblem || outputInvalid || timeline.clips.length === 0} onClick={() => mutate('/exports', {expected_version: state.version, ...(episode>1 ? {episode_number: episode} : {})})}>{t("合成并导出 MP4")}</button></div>
      {dirty && <p className="drama-help">{t("时间轴有未保存修改。切换前请先保存或取消。")}</p>}
      <div className="media-exports">{state.exports.filter(item => (item.episode_number ?? 1) === episode).map(item => <article key={item.id}><h3>{t("成片 ·")}{{queued: t("等待合成"), running: t("正在合成"), succeeded: t("导出完成"), failed: t("导出失败"), cancelled: t("已取消")}[item.status]}</h3>{item.error && <p className="error">{item.error}</p>}{item.status === 'succeeded' && item.file_url && <><video controls crossOrigin="use-credentials" src={mediaURL(item.file_url)} preload="metadata" aria-label={t("成片预览")}>{item.subtitle_vtt_url && (!item.timeline?.subtitle_mode || item.timeline.subtitle_mode === 'soft') && <track kind="subtitles" src={mediaURL(item.subtitle_vtt_url)} srcLang="zh" label={t("中文字幕")} default />}</video><a href={mediaURL(item.file_url)} download>{t("下载 MP4 成片")}</a>{item.subtitle_url && <a href={mediaURL(item.subtitle_url)} download>{t("下载 SRT 字幕")}</a>}<p>{formatNumber(item.duration ?? 0)} {t("秒 · 本地合成；素材来源见素材列表")}</p></>}{(item.status === 'queued' || item.status === 'running') && <button disabled={locked} onClick={() => mutate(`/exports/${item.id}/cancel`, {})}>{t("取消成片合成")}</button>}</article>)}</div>
    </>}
    {confirmDemo && state && <ConfirmDialog title={t("替换当前时间轴为合成演示？")} message={t("此操作创建合成图片和音轨，并用演示镜头替换已保存时间轴。原媒体素材仍保留。")} confirmLabel={t("确认创建合成演示")} onCancel={() => setConfirmDemo(false)} onConfirm={() => {setConfirmDemo(false); mutate('/demo', {expected_version: state.version})}} />}
  </section>
}
