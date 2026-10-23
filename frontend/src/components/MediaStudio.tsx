import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, baseURL, errorMessage, request } from '../api'
import { ConfirmDialog } from './ConfirmDialog'
import type { DramaProject } from '../drama'

interface MediaAsset { id: string; kind: 'image' | 'audio' | 'video'; mime_type: string; name: string; role: string; character: string; shot_id: string; duration: number; source: string; file_url: string }
interface Clip { id: string; asset_id: string; duration: number; trim_start: number; subtitle: string; audio_asset_id: string; audio_volume: number }
interface Timeline { clips: Clip[]; music_asset_id: string; music_volume: number }
interface Export { id: string; status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'; file_url?: string; subtitle_url?: string; subtitle_vtt_url?: string; error?: string; duration?: number }
interface MediaState { version: number; assets: MediaAsset[]; timeline: Timeline; exports: Export[] }
interface Attempt { path: string; method: string; body: BodyInit; type: string; key: string }
const emptyTimeline: Timeline = {clips: [], music_asset_id: '', music_volume: 0.2}
function mediaURL(url: string) { return url.startsWith('/') ? `${baseURL}${url}` : '' }

export function MediaStudio({project, space, onDirtyChange}: {project: DramaProject; space: string; onDirtyChange: (dirty: boolean) => void}) {
  const [state, setState] = useState<MediaState | null>(null)
  const [baselineVersion, setBaselineVersion] = useState(0)
  const [timeline, setTimeline] = useState<Timeline>(emptyTimeline)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [confirmDemo, setConfirmDemo] = useState(false)
  const [role, setRole] = useState('shot')
  const [character, setCharacter] = useState('')
  const [shotId, setShotId] = useState('')
  const [photo, setPhoto] = useState<File | null>(null)
  const [rotation, setRotation] = useState(0)
  const [square, setSquare] = useState(false)
  const pending = useRef<Attempt | null>(null)
  const inFlight = useRef(false)
  const alive = useRef(true)
  const dirty = state !== null && JSON.stringify(timeline) !== JSON.stringify(state.timeline)
  const dirtyRef = useRef(false)
  const path = `/projects/${encodeURIComponent(project.id)}/media`
  const query = new URLSearchParams({space_id: space}).toString()
  useEffect(() => {dirtyRef.current = dirty}, [dirty])
  useEffect(() => {onDirtyChange(dirty || busy || uncertain); return () => onDirtyChange(false)}, [dirty, busy, uncertain, onDirtyChange])
  const load = useCallback(async (replace = false) => {
    const result = await request<MediaState>(`${path}?${query}`)
    if (!alive.current) return
    setState(result)
    if (replace || !dirtyRef.current) {setTimeline(result.timeline); setBaselineVersion(result.version)}
  }, [path, query])
  useEffect(() => {
    alive.current = true
    void load().catch(failure => {if (alive.current) setError(errorMessage(failure, '媒体读取失败'))})
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
      setNotice('操作已保存。生成与导出状态以服务端记录为准。')
      const replace = attempt.path.includes('/timeline?') || attempt.path.includes('/demo?')
      try {await load(replace)} catch (failure) {setError(`操作已被接受，媒体刷新失败：${errorMessage(failure, '请重新读取')}`)}
    } catch (failure) {
      if (!alive.current) return
      const unknown = !(failure instanceof ApiError) || failure.status >= 500
      setUncertain(unknown)
      setError(unknown ? '结果未确认，请用原请求重试。不会自动创建第二次生成。' : errorMessage(failure, '媒体操作失败'))
    } finally {inFlight.current = false; if (alive.current) setBusy(false)}
  }
  function mutate(suffix: string, data: unknown, method = 'POST') {
    if (busy || uncertain) return
    const attempt = {path: `${path}${suffix}?${query}`, method, body: JSON.stringify(data), type: 'application/json', key: crypto.randomUUID()}
    pending.current = attempt; void execute(attempt)
  }
  function upload(file: Blob, name: string) {
    if (busy || uncertain) return
    if (file.size > 32 * 1024 * 1024) {setError('文件不能超过 32 MiB。'); return}
    const params = new URLSearchParams({space_id: space, filename: name, role, character, shot_id: shotId})
    const attempt = {path: `${path}/uploads?${params}`, method: 'POST', body: file, type: file.type, key: crypto.randomUUID()}
    pending.current = attempt; void execute(attempt)
  }
  async function editPhoto() {
    if (!photo) return
    if (photo.size > 32 * 1024 * 1024) {setError('照片不能超过 32 MiB。'); return}
    try {
      const bitmap = await createImageBitmap(photo)
      if (bitmap.width * bitmap.height > 24000000) {bitmap.close(); throw new Error('图片像素过大，请选择不超过 2400 万像素的照片。')}
      const size = square ? Math.min(bitmap.width, bitmap.height) : 0
      const width = size || bitmap.width, height = size || bitmap.height
      const canvas = document.createElement('canvas'); canvas.width = rotation % 180 ? height : width; canvas.height = rotation % 180 ? width : height
      const context = canvas.getContext('2d')!
      context.translate(canvas.width / 2, canvas.height / 2); context.rotate(rotation * Math.PI / 180)
      context.drawImage(bitmap, (bitmap.width - width) / 2, (bitmap.height - height) / 2, width, height, -width / 2, -height / 2, width, height); bitmap.close()
      const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob(value => value ? resolve(value) : reject(new Error('无法编辑照片')), 'image/png'))
      upload(blob, `edited-${photo.name.replace(/\.[^.]+$/, '')}.png`)
    } catch (failure) {setError(errorMessage(failure, '照片编辑失败'))}
  }
  function updateClip(index: number, patch: Partial<Clip>) {setTimeline({...timeline, clips: timeline.clips.map((clip, i) => i === index ? {...clip, ...patch} : clip)})}
  function move(index: number, delta: number) {const clips = [...timeline.clips]; [clips[index], clips[index + delta]] = [clips[index + delta], clips[index]]; setTimeline({...timeline, clips})}
  const locked = busy || uncertain
  const visual = state?.assets.filter(asset => asset.kind !== 'audio') ?? []
  const audio = state?.assets.filter(asset => asset.kind === 'audio') ?? []
  return <section className="panel media-studio" aria-labelledby="media-heading">
    <div className="section-heading"><div><span className="eyebrow">04 / BRING IT TO LIFE</span><h2 id="media-heading">素材与成片</h2></div><span>本地合成 · 媒体版本 {state?.version ?? '—'}</span></div>
    <p className="drama-help">上传照片、镜头视频、配音或音乐，编排时间轴并导出 MP4。角色参考只保存关联与创作约束，无法保证跨镜头外观一致。真实模型效果与费用尚未验证。</p>
    {error && <div className="error" role="alert">{error}{uncertain ? <button disabled={busy} onClick={() => {if (pending.current) void execute(pending.current)}}>原请求重试</button> : <button disabled={busy} onClick={() => {void load().then(() => setError('')).catch(failure => setError(errorMessage(failure, '读取失败')))}}>重新读取媒体</button>}</div>}
    {notice && <p className="notice" role="status">{notice}</p>}
    {!state ? <p role="status">正在读取媒体…</p> : <>
      <details className="media-upload" open><summary>上传与照片编辑</summary><fieldset disabled={locked}>
        <div className="storyboard-fields"><div><label htmlFor="media-role">素材用途</label><select id="media-role" value={role} onChange={event => setRole(event.target.value)}><option value="shot">镜头素材</option><option value="reference">角色参考</option><option value="voice">配音</option><option value="music">音乐</option><option value="sfx">音效</option></select></div>
        <div><label htmlFor="media-character">关联角色</label><select id="media-character" value={character} onChange={event => setCharacter(event.target.value)}><option value="">不关联角色</option>{project.script?.characters.map(item => <option key={item.name}>{item.name}</option>)}</select></div>
        <div><label htmlFor="media-shot">关联镜头</label><select id="media-shot" value={shotId} onChange={event => setShotId(event.target.value)}><option value="">不关联镜头</option>{project.storyboard?.shots.map((shot, index) => <option key={shot.id} value={shot.id}>第 {index + 1} 镜 · {shot.description.slice(0, 25)}</option>)}</select></div>
        <div><label htmlFor="media-file">上传素材（PNG/JPEG/WAV/MP3/MP4）</label><input id="media-file" type="file" accept="image/png,image/jpeg,audio/wav,audio/mpeg,video/mp4" onChange={event => {const file = event.target.files?.[0]; if (file) upload(file, file.name); event.target.value = ''}} /></div></div>
        <label htmlFor="photo-edit">选择待编辑照片</label><input id="photo-edit" type="file" accept="image/png,image/jpeg" onChange={event => {setPhoto(event.target.files?.[0] ?? null); setRotation(0)}} />
        {photo && <div className="media-actions"><span>{photo.name}</span><label>旋转<select aria-label="照片旋转" value={rotation} onChange={event => setRotation(Number(event.target.value))}>{[0, 90, 180, 270].map(value => <option key={value} value={value}>{value}°</option>)}</select></label><label><input type="checkbox" checked={square} onChange={event => setSquare(event.target.checked)} /> 居中裁剪为正方形</label><button onClick={() => void editPhoto()}>保存编辑照片为新素材</button></div>}
      </fieldset></details>
      <div className="media-assets">{state.assets.map(asset => <article key={asset.id}>{asset.kind === 'image' ? <img src={mediaURL(asset.file_url)} alt={asset.name} loading="lazy" /> : asset.kind === 'video' ? <video src={mediaURL(asset.file_url)} controls preload="metadata" /> : <audio src={mediaURL(asset.file_url)} controls preload="metadata" />}<strong>{asset.name}</strong><p>{asset.role} {asset.character} · {asset.source}</p>{asset.shot_id && <p>已关联镜头 {(project.storyboard?.shots.findIndex(shot => shot.id === asset.shot_id) ?? -1) + 1}</p>}{asset.kind !== 'audio' && <button disabled={locked || timeline.clips.length >= 30} onClick={() => setTimeline({...timeline, clips: [...timeline.clips, {id: crypto.randomUUID(), asset_id: asset.id, duration: Math.min(asset.duration || 3, 30), trim_start: 0, subtitle: '', audio_asset_id: '', audio_volume: 1}]})}>加入时间轴</button>}</article>)}</div>
      {state.assets.length === 0 && <p className="drama-help">还没有媒体。上传素材，或使用明确标注的合成演示验证本地成片。</p>}
      <button disabled={locked || dirty} onClick={() => {if (timeline.clips.length) setConfirmDemo(true); else mutate('/demo', {expected_version: state.version})}}>创建合成演示素材与时间轴</button>
      <h3>可编辑时间轴 <small>{timeline.clips.reduce((total, clip) => total + clip.duration, 0)} 秒</small></h3>
      <p className="drama-help">按顺序拼接镜头；配音从镜头起点播放，字幕持续整个镜头，背景音乐覆盖成片。视频原音不自动保留，请单独上传配音或音效。保存后再导出。</p>
      <fieldset disabled={locked}><div className="media-timeline">{timeline.clips.map((clip, index) => <article key={clip.id}>
        <div className="media-actions"><strong>片段 {index + 1}</strong><button disabled={index === 0 || locked} onClick={() => move(index, -1)}>上移片段 {index + 1}</button><button disabled={index === timeline.clips.length - 1 || locked} onClick={() => move(index, 1)}>下移片段 {index + 1}</button><button onClick={() => setTimeline({...timeline, clips: timeline.clips.filter((_, i) => i !== index)})}>移除片段 {index + 1}</button></div>
        <div className="storyboard-fields"><label>画面素材<select aria-label={`片段 ${index + 1} 画面素材`} value={clip.asset_id} onChange={event => updateClip(index, {asset_id: event.target.value})}>{visual.map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label><label>时长（秒）<input aria-label={`片段 ${index + 1} 时长`} type="number" min="0.5" max="30" step="0.1" value={clip.duration} onChange={event => updateClip(index, {duration: Number(event.target.value)})} /></label><label>视频起点（秒）<input aria-label={`片段 ${index + 1} 起点`} type="number" min="0" step="0.1" value={clip.trim_start} onChange={event => updateClip(index, {trim_start: Number(event.target.value)})} /></label><label>配音 / 音效<select aria-label={`片段 ${index + 1} 音频`} value={clip.audio_asset_id} onChange={event => updateClip(index, {audio_asset_id: event.target.value})}><option value="">无</option>{audio.map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label><label>配音音量<input aria-label={`片段 ${index + 1} 音量`} type="number" min="0" max="1" step="0.1" value={clip.audio_volume} onChange={event => updateClip(index, {audio_volume: Number(event.target.value)})} /></label></div>
        <label>字幕<textarea aria-label={`片段 ${index + 1} 字幕`} maxLength={300} value={clip.subtitle} onChange={event => updateClip(index, {subtitle: event.target.value})} /></label>
      </article>)}</div>
      <div className="storyboard-fields"><label>背景音乐<select value={timeline.music_asset_id} onChange={event => setTimeline({...timeline, music_asset_id: event.target.value})}><option value="">无背景音乐</option>{audio.map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label><label>音乐音量<input type="number" min="0" max="1" step="0.1" value={timeline.music_volume} onChange={event => setTimeline({...timeline, music_volume: Number(event.target.value)})} /></label></div></fieldset>
      <div className="media-actions"><button disabled={locked || !dirty} onClick={() => {setTimeline(state.timeline); setBaselineVersion(state.version)}}>取消时间轴编辑</button><button disabled={locked || !dirty} onClick={() => mutate('/timeline', {expected_version: baselineVersion, timeline}, 'PUT')}>保存时间轴</button><button className="primary" disabled={locked || dirty || timeline.clips.length === 0} onClick={() => mutate('/exports', {expected_version: state.version})}>合成并导出 MP4</button></div>
      {dirty && <p className="drama-help">时间轴有未保存修改。切换前请先保存或取消。</p>}
      <div className="media-exports">{state.exports.map(item => <article key={item.id}><h3>成片 · {{queued: '等待合成', running: '正在合成', succeeded: '导出完成', failed: '导出失败', cancelled: '已取消'}[item.status]}</h3>{item.error && <p className="error">{item.error}</p>}{item.status === 'succeeded' && item.file_url && <><video controls crossOrigin="anonymous" src={mediaURL(item.file_url)} preload="metadata" aria-label="成片预览">{item.subtitle_vtt_url && <track kind="subtitles" src={mediaURL(item.subtitle_vtt_url)} srcLang="zh" label="中文字幕" default />}</video><a href={mediaURL(item.file_url)} download>下载 MP4 成片</a>{item.subtitle_url && <a href={mediaURL(item.subtitle_url)} download>下载 SRT 字幕</a>}<p>{item.duration} 秒 · 本地合成；素材来源见素材列表</p></>}{(item.status === 'queued' || item.status === 'running') && <button disabled={locked} onClick={() => mutate(`/exports/${item.id}/cancel`, {})}>取消成片合成</button>}</article>)}</div>
    </>}
    {confirmDemo && state && <ConfirmDialog title="替换当前时间轴为合成演示？" message="此操作创建合成图片和音轨，并用演示镜头替换已保存时间轴。原媒体素材仍保留。" confirmLabel="确认创建合成演示" onCancel={() => setConfirmDemo(false)} onConfirm={() => {setConfirmDemo(false); mutate('/demo', {expected_version: state.version})}} />}
  </section>
}
