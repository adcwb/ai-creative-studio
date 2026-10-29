import { useEffect, useRef, useState } from 'react'
import { baseURL } from '../api'
import { translate as t, useI18n } from '../i18n'
import { clipStarts, overlayAt, overlaps, timelineDuration, type ClipTiming, type VideoOverlay } from '../mediaTimeline'
import './timelinePreview.css'
import { TimelineAudio } from './TimelineAudio'
import { audioRegions, type AudibleClip } from '../previewAudio'
import type { AudioTiming } from '../mediaTimeline'

interface PreviewClip extends ClipTiming, AudibleClip {id: string; asset_id: string; trim_start: number; crop_x?: number; crop_y?: number; crop_width?: number; crop_height?: number; subtitle?: string; subtitle_cues?: {start: number; end: number; text: string}[]}
interface Asset {id: string; kind: 'image' | 'video' | 'audio'; file_url: string}
type Source = HTMLImageElement | HTMLVideoElement
export function TimelinePreview({clips, overlays, assets, aspect, subtitleMode, time, onTime, audioTracks = [], musicAsset = '', musicVolume = .2}: {
  clips: PreviewClip[]; overlays: VideoOverlay[]; assets: Asset[]; aspect: string; subtitleMode: string; time: number; onTime: (time: number) => void
  audioTracks?: AudioTiming[]; musicAsset?: string; musicVolume?: number
}) {
  const {formatNumber} = useI18n()
  const canvas = useRef<HTMLCanvasElement>(null)
  const [playing, setPlaying] = useState(false)
  const [loadFailed, setLoadFailed] = useState(false)
  const sources = useRef(new Map<string, Source>())
  const duration = timelineDuration(clips)
  const timeRef = useRef(time)
  const onTimeRef = useRef(onTime)
  const width = aspect === '9:16' ? 360 : 640, height = aspect === '16:9' ? 360 : 640
  useEffect(() => {timeRef.current = time; onTimeRef.current = onTime}, [time, onTime])
  // Each occurrence has its own decoder so repeated assets can be independently
  // trimmed or appear on both sides of a transition. Never load arbitrary URLs.
  const sourceSpecs = JSON.stringify([...clips.map(c => ({key: `c:${c.id}`, asset: assets.find(a => a.id === c.asset_id)})), ...overlays.map(l => ({key: `o:${l.id}`, asset: assets.find(a => a.id === l.asset_id)}))])
  useEffect(() => {
    const entries = JSON.parse(sourceSpecs) as {key: string; asset?: Asset}[]
    const map = new Map<string, Source>()
    for (const {key, asset} of entries) {
      if (!asset || asset.kind === 'audio' || !asset.file_url.startsWith('/') || asset.file_url.startsWith('//')) continue
      const source = asset.kind === 'image' ? new Image() : document.createElement('video')
      source.crossOrigin = 'use-credentials'
      if (source instanceof HTMLVideoElement) {source.muted = true; source.playsInline = true; source.preload = 'auto'}
      source.onerror = () => setLoadFailed(true)
      source.src = `${baseURL}${asset.file_url}`
      map.set(key, source)
    }
    sources.current = map
    return () => {for (const source of map.values()) {source.onerror = null; if (source instanceof HTMLVideoElement) {source.pause(); source.removeAttribute('src'); source.load()} else source.src = ''} sources.current = new Map()}
  }, [sourceSpecs])
  useEffect(() => {
    if (!playing) return
    let last = performance.now(), frame = 0
    const tick = (now: number) => {
      const next = Math.min(duration, timeRef.current + Math.min(.1, (now - last) / 1000))
      last = now; timeRef.current = next; onTimeRef.current(next)
      if (next >= duration) {setPlaying(false); return}
      frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
  }, [playing, duration])
  useEffect(() => {
    const element = canvas.current, context = element?.getContext('2d')
    if (!context || !element) return
    const shotCanvas = document.createElement('canvas'); shotCanvas.width = width; shotCanvas.height = height
    const shotContext = shotCanvas.getContext('2d')!
    let frame = 0
    const starts = clipStarts(clips)
    const draw = () => {
      const clock = Math.min(Math.max(0, time), Math.max(0, duration - 1 / 24))
      context.globalAlpha = 1; context.fillStyle = '#000'; context.fillRect(0, 0, width, height)
      function sourceImage(key: string, seek: number, x: number, y: number, w: number, h: number, target = context, crop?: PreviewClip) {
        const source = sources.current.get(key)
        if (!source) return
        let sw = 0, sh = 0
        if (source instanceof HTMLVideoElement) {
          if (source.readyState < 1) return
          if (!source.seeking && Math.abs(source.currentTime - seek) > 1 / 24) source.currentTime = Math.min(seek, Math.max(0, source.duration - .001))
          if (source.readyState < 2) return
          sw = source.videoWidth; sh = source.videoHeight
        } else {if (!source.complete) return; sw = source.naturalWidth; sh = source.naturalHeight}
        if (!sw || !sh) return
        const cropped = crop && (crop.crop_width ?? 0) > 0 && (crop.crop_height ?? 0) > 0
        const sx = cropped ? (crop.crop_x ?? 0) * sw : 0, sy = cropped ? (crop.crop_y ?? 0) * sh : 0
        const sourceWidth = cropped ? crop.crop_width! * sw : sw, sourceHeight = cropped ? crop.crop_height! * sh : sh
        const scale = Math.min(w / sourceWidth, h / sourceHeight)
        target!.drawImage(source, sx, sy, sourceWidth, sourceHeight, x + (w - sourceWidth * scale) / 2, y + (h - sourceHeight * scale) / 2, sourceWidth * scale, sourceHeight * scale)
      }
      const active = clips.map((clip, index) => ({clip, index, local: clock - starts[index]})).filter(({clip, local}) => local >= 0 && local < clip.duration)
      for (const {clip, index, local} of active) {
        context.save()
        let x = 0
        if ((clip.transition === 'slideleft' || clip.transition === 'slideright') && local > clip.duration - (clip.transition_duration ?? 0)) {const progress = (local - clip.duration + (clip.transition_duration ?? 0)) / (clip.transition_duration ?? 1); x = width * progress * (clip.transition === 'slideleft' ? -1 : 1)}
        const previous = clips[index - 1]
        const incoming = previous && overlaps(previous.transition) && local < (previous.transition_duration ?? 0)
        if (incoming) {
          const progress = Math.min(1, local / (previous.transition_duration ?? 1))
          if (previous.transition === 'crossfade') context.globalAlpha = progress
          else if (previous.transition === 'circleopen') {context.beginPath(); context.ellipse(width / 2, height / 2, width * progress / Math.SQRT2, height * progress / Math.SQRT2, 0, 0, Math.PI * 2); context.clip()}
          else if (previous.transition === 'slideleft') x = width * (1 - progress)
          else if (previous.transition === 'slideright') x = -width * (1 - progress)
          else {context.beginPath(); context.rect(previous.transition === 'wipeleft' ? width * (1 - progress) : 0, 0, width * progress, height); context.clip()}
        }
        if (clip.transition === 'fade') {const fade = clip.transition_duration ?? .5; context.globalAlpha *= Math.max(0, Math.min(1, local / fade, (clip.duration - local) / fade))}
        shotContext.clearRect(0, 0, width, height); shotContext.fillStyle = '#000'; shotContext.fillRect(0, 0, width, height)
        sourceImage(`c:${clip.id}`, clip.trim_start + local, 0, 0, width, height, shotContext, clip)
        context.drawImage(shotCanvas, x, 0)
        context.restore()
      }
      for (const layer of overlays) {
        const local = clock - layer.start
        if (local < 0 || local >= layer.duration) continue
        const position = overlayAt(layer, local)
        if (![position.x, position.y, position.opacity, layer.width, layer.height].every(Number.isFinite)) continue
        context.save(); context.globalAlpha = Math.max(0, Math.min(1, position.opacity))
        sourceImage(`o:${layer.id}`, layer.trim_start + local, position.x * width, position.y * height, layer.width * width, layer.height * height)
        context.restore()
      }
      if (subtitleMode !== 'none') {
        const lines = active.flatMap(({clip, local}) => clip.subtitle_cues?.length ? clip.subtitle_cues.filter(c => local >= c.start && local < c.end).map(c => c.text) : clip.subtitle ? [clip.subtitle] : [])
        context.font = '20px sans-serif'; context.textAlign = 'center'; context.textBaseline = 'bottom'; context.lineWidth = 3; context.strokeStyle = '#000'; context.fillStyle = '#fff'
        lines.slice(-3).forEach((line, i) => {const y = height - 16 - (lines.length - i - 1) * 26; context.strokeText(line, width / 2, y, width - 24); context.fillText(line, width / 2, y, width - 24)})
      }
      frame = requestAnimationFrame(draw)
    }
    frame = requestAnimationFrame(draw)
    return () => cancelAnimationFrame(frame)
  }, [clips, overlays, aspect, subtitleMode, time, width, height, duration])
  return <section className="timeline-preview" aria-label={t('实时画面预览')}>
    <h3>{t('实时画面预览')}</h3>
    <p className="drama-help">{t('预览同步画面、原音、配音和音乐；浏览器混音、字体和转场边缘可能与导出不同。最终成片请检查导出文件。')}</p>
    <TimelineAudio regions={audioRegions(clips, audioTracks, overlays, musicAsset, musicVolume)} assets={assets} playing={playing} time={time} />
    {loadFailed && <p role="alert">{t('部分预览素材无法读取，请检查素材权限并重新打开项目。')}</p>}
    <canvas ref={canvas} width={width} height={height} aria-label={t('时间轴合成画面')} />
    <div className="media-actions"><button type="button" disabled={!duration} onClick={() => {if (!playing && time >= duration) onTime(0); setPlaying(!playing)}}>{playing ? t('暂停预览') : t('播放预览')}</button><output>{formatNumber(time, {maximumFractionDigits: 2})} / {formatNumber(duration, {maximumFractionDigits: 2})} {t('秒')}</output></div>
    <label>{t('预览播放头（秒）')}<input aria-label={t('预览播放头（秒）')} type="range" min="0" max={Math.max(0, duration)} step={1 / 24} value={Math.min(time, Math.max(0, duration))} onChange={event => {setPlaying(false); onTime(Number(event.target.value))}} /></label>
    <label>{t('精确播放头（秒）')}<input type="number" min="0" max={Math.max(0, duration)} step="0.01" value={time} onChange={event => {setPlaying(false); onTime(Math.min(duration, Math.max(0, Number(event.target.value) || 0)))}} /></label>
    <div className="timeline-ruler" aria-label={t('镜头时间刻度')}>{clips.map((clip, index) => <button type="button" key={clip.id} style={{flexGrow: clip.duration}} onClick={() => {setPlaying(false); onTime(clipStarts(clips)[index])}}>{t('镜头 {index}', {index: index + 1})}</button>)}</div>
  </section>
}
