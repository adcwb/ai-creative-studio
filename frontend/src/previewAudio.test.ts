import { describe, expect, it } from 'vitest'
import { audioPosition, audioRegions } from './previewAudio'
import { replaceClipVisual, splitClip } from './mediaTimeline'

describe('production sound and equal-duration replacement', () => {
  const clip = {id: 'shot-1', asset_id: 'original', duration: 3, trim_start: 2, audio_asset_id: 'voice', audio_volume: .7, preserve_source_audio: true, source_audio_volume: .6, subtitle: 'keep this', subtitle_cues: [{start: 0, end: 1, text: 'keep'}]}
  it('keeps timing, captions, dialogue and original video sound across repeated replacements', () => {
    const next = replaceClipVisual(clip, {id: 'replacement', kind: 'video', duration: 4})!
    expect(next).toMatchObject({asset_id: 'replacement', trim_start: 0, duration: 3, audio_asset_id: 'voice', subtitle: 'keep this', source_audio_asset_id: 'original', source_audio_trim_start: 2})
    expect(next.subtitle_cues).toEqual(clip.subtitle_cues)
    expect(replaceClipVisual(next, {id: 'image', kind: 'image', duration: 0})).toMatchObject({source_audio_asset_id: 'original', source_audio_trim_start: 2})
    expect(clip.asset_id).toBe('original')
    expect(replaceClipVisual(clip, {id: 'short', kind: 'video', duration: 1})).toBeNull()
  })
  it('schedules retained source sound, voice, positioned audio and looping music', () => {
    const next = replaceClipVisual(clip, {id: 'image', kind: 'image', duration: 0})!
    const regions = audioRegions([next], [{id: 'fx', asset_id: 'effect', start: 1, trim_start: .5, duration: 1, volume: .4}], [], 'music', .2)
    expect(regions.find(r => r.id === 'source:shot-1')).toMatchObject({asset: 'original', trim: 2, volume: .6})
    expect(regions.find(r => r.id === 'voice:shot-1')).toMatchObject({asset: 'voice', trim: 0, volume: .7})
    const effect = regions.find(r => r.id === 'track:fx')!
    expect(audioPosition(effect, .8, 4).active).toBe(false)
    expect(audioPosition(effect, 1.5, 4)).toMatchObject({active: true, offset: 1})
    expect(audioPosition(effect, 2, 4).active).toBe(false)
    expect(audioPosition(regions.find(r => r.id === 'music')!, 2.5, 2)).toMatchObject({offset: .5, active: true})
  })
  it('accounts for overlapping transitions when placing the next voice', () => {
    const regions = audioRegions([{...clip, transition: 'crossfade', transition_duration: 1}, {...clip, id: 'shot-2'}], [], [], '', 0)
    const voice = regions.find(r => r.id === 'voice:shot-2')!
    expect(voice.start).toBe(2)
    expect(audioPosition(voice, 2.5, 8).volume).toBeCloseTo(.35)
  })
  it('splits a retained audio source at the same offset as the picture', () => {
    const input = {...clip, audio_asset_id: '', source_audio_asset_id: 'original', source_audio_trim_start: 2}
    const parts = splitClip(input, 1, false, 'second')!
    expect(parts[1].source_audio_trim_start).toBe(3)
  })
})
