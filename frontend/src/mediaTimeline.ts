export interface ClipTiming {
  duration: number
  transition?: '' | 'fade' | 'crossfade'
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
}
export interface AudioTiming {id: string; asset_id: string; start: number; trim_start: number; duration: number; volume: number}
export interface VisualAsset {id: string; kind: 'image' | 'video' | 'audio'; duration: number}
export function timelineDuration(clips: readonly ClipTiming[]): number {
  return clips.reduce((sum, clip) => sum + clip.duration - (clip.transition === 'crossfade' ? clip.transition_duration ?? 0 : 0), 0)
}
export function maxTransitionDuration(clips: readonly ClipTiming[], index: number, transition = clips[index]?.transition): number {
  return Math.min(2, (clips[index]?.duration ?? 0) / 2, transition === 'crossfade' ? (clips[index + 1]?.duration ?? 0) / 2 : 2)
}
export function compositionError(clips: readonly ClipTiming[], overlays: readonly VideoOverlay[], assets: readonly VisualAsset[], tracks: readonly AudioTiming[] = []): 'transition' | 'overlay' | 'audio' | null {
  for (const [index, clip] of clips.entries()) {
    if (!Number.isFinite(clip.duration) || clip.duration < .5 || clip.duration > 30) return 'transition'
    if (!clip.transition) {if ((clip.transition_duration ?? 0) !== 0) return 'transition'; continue}
    if (clip.transition !== 'fade' && clip.transition !== 'crossfade') return 'transition'
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

