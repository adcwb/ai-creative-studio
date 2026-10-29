import { clipStarts, overlaps, timelineDuration, type AudioTiming, type ClipTiming, type VideoOverlay } from './mediaTimeline'

export interface AudibleClip extends ClipTiming {id: string; asset_id: string; trim_start: number; audio_asset_id?: string; audio_volume?: number; preserve_source_audio?: boolean; source_audio_volume?: number; source_audio_asset_id?: string; source_audio_trim_start?: number}
export interface AudioRegion {id: string; asset: string; start: number; duration: number; trim: number; volume: number; loop?: boolean; fadeIn?: number; fadeOut?: number}

export function audioRegions(clips: AudibleClip[], tracks: AudioTiming[], overlays: VideoOverlay[], music: string, musicVolume: number): AudioRegion[] {
  const starts = clipStarts(clips)
  const regions: AudioRegion[] = []
  clips.forEach((c, i) => {
    const envelope = {start: starts[i], duration: c.duration, fadeIn: i > 0 && overlaps(clips[i - 1].transition) ? clips[i - 1].transition_duration : 0, fadeOut: overlaps(c.transition) ? c.transition_duration : 0}
    if (c.preserve_source_audio) regions.push({id: `source:${c.id}`, asset: c.source_audio_asset_id || c.asset_id, trim: c.source_audio_asset_id ? c.source_audio_trim_start ?? 0 : c.trim_start, volume: c.source_audio_volume ?? 1, ...envelope})
    if (c.audio_asset_id) regions.push({id: `voice:${c.id}`, asset: c.audio_asset_id, trim: 0, volume: c.audio_volume ?? 1, ...envelope})
  })
  for (const track of tracks) regions.push({id: `track:${track.id}`, asset: track.asset_id, start: track.start, duration: track.duration, trim: track.trim_start, volume: track.volume})
  for (const layer of overlays) if (layer.preserve_source_audio) regions.push({id: `overlay:${layer.id}`, asset: layer.asset_id, start: layer.start, duration: layer.duration, trim: layer.trim_start, volume: layer.source_audio_volume})
  if (music) regions.push({id: 'music', asset: music, start: 0, duration: timelineDuration(clips), trim: 0, volume: musicVolume, loop: true})
  return regions
}

export function audioPosition(region: AudioRegion, time: number, sourceDuration: number) {
  const local = time - region.start
  let offset = local + region.trim
  if (region.loop && sourceDuration > 0) offset %= sourceDuration
  const active = local >= 0 && local < region.duration && offset >= 0 && (sourceDuration <= 0 || offset < sourceDuration)
  const fadeIn = region.fadeIn ? Math.min(1, local / region.fadeIn) : 1
  const fadeOut = region.fadeOut ? Math.min(1, (region.duration - local) / region.fadeOut) : 1
  return {active, offset: Math.max(0, offset), volume: Math.max(0, Math.min(1, region.volume * fadeIn * fadeOut))}
}
