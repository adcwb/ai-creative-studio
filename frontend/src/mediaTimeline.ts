export type Transition = '' | 'fade' | 'crossfade' | 'wipeleft' | 'wiperight' | 'slideleft' | 'slideright' | 'circleopen'
export const overlapTransitions: readonly Transition[] = ['crossfade', 'wipeleft', 'wiperight', 'slideleft', 'slideright', 'circleopen']
export function overlaps(transition?: Transition) {return overlapTransitions.includes(transition ?? '')}
export interface OverlayKeyframe {time: number; x: number; y: number; opacity: number}
export interface ClipTiming {
  duration: number
  transition?: Transition
  transition_duration?: number
}
export interface VideoOverlay {
  id: string
  asset_id: string
  start: number
  trim_start: number
  duration: number
  x: number
  y: number
  width: number
  height: number
  opacity: number
  preserve_source_audio: boolean
  source_audio_volume: number
  keyframes?: OverlayKeyframe[]
}
export interface AudioTiming {id: string; asset_id: string; start: number; trim_start: number; duration: number; volume: number}
export interface VisualAsset {id: string; kind: 'image' | 'video' | 'audio'; duration: number}
export function timelineDuration(clips: readonly ClipTiming[]): number {
  return clips.reduce((sum, clip) => sum + clip.duration - (overlaps(clip.transition) ? clip.transition_duration ?? 0 : 0), 0)
}
export function maxTransitionDuration(clips: readonly ClipTiming[], index: number, transition = clips[index]?.transition): number {
  return Math.min(2, (clips[index]?.duration ?? 0) / 2, overlaps(transition) ? (clips[index + 1]?.duration ?? 0) / 2 : 2)
}
export function compositionError(clips: readonly ClipTiming[], overlays: readonly VideoOverlay[], assets: readonly VisualAsset[], tracks: readonly AudioTiming[] = []): 'transition' | 'overlay' | 'audio' | null {
  for (const [index, clip] of clips.entries()) {
    if (!Number.isFinite(clip.duration) || clip.duration < .5 || clip.duration > 30) return 'transition'
    if (!clip.transition) {if ((clip.transition_duration ?? 0) !== 0) return 'transition'; continue}
    if (clip.transition !== 'fade' && !overlaps(clip.transition)) return 'transition'
    const seconds = clip.transition_duration ?? 0
    if (!Number.isFinite(seconds) || seconds < .1 || seconds > maxTransitionDuration(clips, index)) return 'transition'
  }
  const duration = timelineDuration(clips)
  if (overlays.length > 4) return 'overlay'
  const ids = new Set<string>()
  for (const layer of overlays) {
    const asset = assets.find(item => item.id === layer.asset_id)
    if (!asset || asset.kind === 'audio' || ids.has(layer.id) || !/^[A-Za-z0-9_-]{1,40}$/.test(layer.id) ||
      ![layer.start, layer.trim_start, layer.duration, layer.x, layer.y, layer.width, layer.height, layer.opacity, layer.source_audio_volume].every(Number.isFinite) ||
      layer.start < 0 || layer.trim_start < 0 || layer.duration < .1 || layer.start + layer.duration > duration + .001 ||
      layer.x < 0 || layer.y < 0 || layer.width < .1 || layer.height < .1 || layer.x + layer.width > 1 || layer.y + layer.height > 1 ||
      layer.opacity < 0 || layer.opacity > 1 || layer.source_audio_volume < 0 || layer.source_audio_volume > 1 ||
      (!layer.preserve_source_audio && layer.source_audio_volume !== 0) ||
      (asset.kind === 'image' && (layer.trim_start !== 0 || layer.preserve_source_audio || layer.source_audio_volume !== 0)) ||
      (asset.kind === 'video' && layer.trim_start + layer.duration > asset.duration + .05)) return 'overlay'
    const keys = layer.keyframes ?? []
    if (keys.length > 16 || keys.some((key, index) =>
      ![key.time, key.x, key.y, key.opacity].every(Number.isFinite) || key.time < 0 || key.time > layer.duration ||
      (index === 0 ? key.time !== 0 : key.time <= keys[index - 1].time) || key.x < 0 || key.y < 0 ||
      key.x + layer.width > 1 || key.y + layer.height > 1 || key.opacity < 0 || key.opacity > 1)) return 'overlay'
    ids.add(layer.id)
  }
  if (tracks.length > 8) return 'audio'
  const trackIds = new Set<string>()
  for (const track of tracks) {
    const asset = assets.find(item => item.id === track.asset_id)
    if (!asset || asset.kind !== 'audio' || trackIds.has(track.id) ||
      ![track.start, track.trim_start, track.duration, track.volume].every(Number.isFinite) ||
      track.start < 0 || track.trim_start < 0 || track.duration < .1 || track.volume < 0 || track.volume > 1 ||
      track.start + track.duration > duration + .001 || track.trim_start + track.duration > asset.duration + .05) return 'audio'
    trackIds.add(track.id)
  }
  return null
}


export function clipStarts(clips: readonly ClipTiming[]) {
  let start = 0
  return clips.map(clip => {const value = start; start += clip.duration - (overlaps(clip.transition) ? clip.transition_duration ?? 0 : 0); return value})
}
export function overlayAt(layer: VideoOverlay, localTime: number): {x: number; y: number; opacity: number} {
  const keys = layer.keyframes ?? []
  if (!keys.length) return {x: layer.x, y: layer.y, opacity: layer.opacity}
  if (localTime <= keys[0].time) return keys[0]
  for (let i = 1; i < keys.length; i++) {
    if (localTime <= keys[i].time) {
      const a = keys[i - 1], b = keys[i], ratio = (localTime - a.time) / (b.time - a.time)
      return {x: a.x + (b.x - a.x) * ratio, y: a.y + (b.y - a.y) * ratio, opacity: a.opacity + (b.opacity - a.opacity) * ratio}
    }
  }
  return keys[keys.length - 1]
}
export interface EditableClip extends ClipTiming {
  id: string; asset_id: string; trim_start: number; audio_asset_id: string; source_audio_asset_id?: string; source_audio_trim_start?: number
  subtitle_cues?: {start: number; end: number; text: string}[]
  subtitle_source?: 'manual' | 'estimated' | 'asr' | 'forced'; subtitle_alignment_id?: string
}

// An equal-duration visual replacement leaves dialogue, captions and other clips
// intact. Source sound retains an explicit reference to its previous video.
export function replaceClipVisual<T extends EditableClip & {preserve_source_audio?: boolean; source_audio_asset_id?: string; source_audio_trim_start?: number}>(clip: T, asset: VisualAsset): T | null {
  if (asset.kind === 'audio' || (asset.kind === 'video' && asset.duration + .05 < clip.duration)) return null
  return {...clip, asset_id: asset.id, trim_start: 0, ...(clip.preserve_source_audio ? {source_audio_asset_id: clip.source_audio_asset_id || clip.asset_id, source_audio_trim_start: clip.source_audio_asset_id ? clip.source_audio_trim_start ?? 0 : clip.trim_start} : {})}
}
// Splitting per-shot dialogue requires an audio source offset not present in the
// existing contract; require moving it to an independently trimmed audio track.
export function splitClip<T extends EditableClip>(clip: T, offset: number, video: boolean, secondID: string): [T, T] | null {
  if (!Number.isFinite(offset) || offset < .5 || clip.duration - offset < .5 || clip.audio_asset_id) return null
  const cues = clip.subtitle_cues ?? []
  function sliceCues(start: number, end: number) {
    return cues.filter(cue => cue.end > start && cue.start < end).map(cue => ({...cue, start: Math.max(cue.start, start) - start, end: Math.min(cue.end, end) - start}))
  }
  const manual = {subtitle_source: 'manual' as const, subtitle_alignment_id: ''}
  return [{...clip, ...manual, duration: offset, transition: '', transition_duration: 0, subtitle_cues: sliceCues(0, offset)},
    {...clip, ...manual, ...(clip.source_audio_asset_id ? {source_audio_trim_start: (clip.source_audio_trim_start ?? 0) + offset} : {}), id: secondID, duration: clip.duration - offset, trim_start: video ? clip.trim_start + offset : 0, subtitle_cues: sliceCues(offset, clip.duration)}]
}
