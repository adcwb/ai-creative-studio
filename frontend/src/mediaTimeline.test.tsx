import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { TimelinePreview } from './components/TimelinePreview'
import { VideoOverlayEditor } from './components/VideoOverlayEditor'
import { compositionError, maxTransitionDuration, timelineDuration, type ClipTiming, type VideoOverlay, type VisualAsset, overlayAt, clipStarts, splitClip, overlapTransitions } from './mediaTimeline'
import { getActiveLocale, setActiveLocale } from './i18n'

const clips: ClipTiming[] = [{duration: 3, transition: 'crossfade', transition_duration: 1}, {duration: 2}]
const assets: VisualAsset[] = [{id: 'video', kind: 'video', duration: 10}, {id: 'image', kind: 'image', duration: 0}, {id: 'audio', kind: 'audio', duration: 10}]
const layer: VideoOverlay = {id: 'layer-1', asset_id: 'video', start: .5, trim_start: 1, duration: 2, x: .5, y: .1, width: .4, height: .3, opacity: .6, preserve_source_audio: true, source_audio_volume: .25}

describe('crossfade and overlay timing', () => {
  it('subtracts outgoing overlaps and leaves black fades unchanged', () => {
    expect(timelineDuration(clips)).toBe(4)
    expect(timelineDuration([{duration: 3, transition: 'fade', transition_duration: 1}, {duration: 2}])).toBe(5)
    expect(maxTransitionDuration(clips, 0)).toBe(1)
    expect(compositionError(clips, [layer], assets)).toBeNull()
  })
  it('requires the next shot and refuses edits that invalidate an overlap', () => {
    expect(compositionError([clips[0]], [], assets)).toBe('transition')
    expect(compositionError([clips[0], {duration: .5}], [], assets)).toBe('transition')
    expect(compositionError([{duration: 2}, clips[0]], [], assets)).toBe('transition')
    expect(compositionError([{duration: Number.NaN}], [], assets)).toBe('transition')
    expect(compositionError([{duration: 2, transition_duration: .2}], [], assets)).toBe('transition')
  })
  it('uses the shortened timeline for overlay and extra audio ends', () => {
    expect(compositionError(clips, [{...layer, start: 3, duration: 2}], assets)).toBe('overlay')
    const track = {id: 'track', asset_id: 'audio', start: 3, trim_start: 0, duration: 2, volume: .5}
    expect(compositionError(clips, [], assets, [track])).toBe('audio')
    expect(compositionError(clips, [], assets, [{...track, duration: 1}])).toBeNull()
  })
  it('bounds overlays and rejects foreign, duplicate and nonfinite inputs', () => {
    for (const update of [{asset_id: 'foreign'}, {asset_id: 'audio'}, {x: .8}, {width: .05}, {opacity: 1.1}, {duration: Infinity}, {trim_start: 9}, {id: '../outside'}]) {
      expect(compositionError(clips, [{...layer, ...update}], assets)).toBe('overlay')
    }
    expect(compositionError(clips, [layer, layer], assets)).toBe('overlay')
    expect(compositionError(clips, Array.from({length: 5}, (_, i) => ({...layer, id: `layer-${i}`})), assets)).toBe('overlay')
  })
  it('requires an explicit source audio choice and valid image settings', () => {
    expect(compositionError(clips, [{...layer, preserve_source_audio: false}], assets)).toBe('overlay')
    expect(compositionError(clips, [{...layer, asset_id: 'image'}], assets)).toBe('overlay')
    const image = {...layer, asset_id: 'image', trim_start: 0, preserve_source_audio: false, source_audio_volume: 0}
    expect(compositionError(clips, [image], assets)).toBeNull()
  })
  it('renders English accessible keyframe controls', () => {
    const previous = getActiveLocale()
    setActiveLocale('en')
    try {
      const html = renderToStaticMarkup(<VideoOverlayEditor layers={[layer]} assets={assets.map(a => ({...a, name: a.id}))} duration={4} onChange={() => {}} />)
      expect(html).toContain('Overlay 1 asset')
      expect(html).toContain('Keep source audio for overlay 1')
      expect(html).toContain('Scrub the live preview')
      expect(html).toContain('Add keyframe to overlay 1')
      expect(html).not.toMatch(/[\u3400-\u9fff]/)
    } finally {setActiveLocale(previous)}
  })
  it('preserves Chinese controls and disables adding layers without visual assets', () => {
    const previous = getActiveLocale()
    setActiveLocale('zh-CN')
    try {
      const html = renderToStaticMarkup(<VideoOverlayEditor layers={[]} assets={[]} duration={0} onChange={() => {}} />)
      expect(html).toContain('尚未添加画面叠层')
      expect(html).toContain('<button disabled="">添加画面叠层</button>')
    } finally {setActiveLocale(previous)}
  })
})

describe('keyframes and non-linear editing', () => {
  it('shares overlap timing for all allowed transition styles', () => {
    for (const transition of overlapTransitions) {
      const pair = [{duration: 3, transition, transition_duration: 1}, {duration: 2}]
      expect(timelineDuration(pair)).toBe(4)
      expect(clipStarts(pair)).toEqual([0, 2])
      expect(compositionError(pair, [], assets)).toBeNull()
    }
  })
  it('interpolates bounded keyframes and holds endpoints', () => {
    const animated = {...layer, keyframes: [{time: 0, x: 0, y: 0, opacity: 0}, {time: 2, x: .6, y: .6, opacity: 1}]}
    expect(compositionError(clips, [animated], assets)).toBeNull()
    expect(overlayAt(animated, 1)).toEqual({x: .3, y: .3, opacity: .5})
    expect(overlayAt(animated, 3).x).toBe(.6)
    expect(overlayAt(layer, 1)).toEqual({x: .5, y: .1, opacity: .6})
    for (const keyframes of [[{time: 1, x: 0, y: 0, opacity: 1}], [{time: 0, x: .8, y: 0, opacity: 1}], [{time: 0, x: 0, y: 0, opacity: NaN}], [{time: 0, x: 0, y: 0, opacity: 1}, {time: 0, x: 0, y: 0, opacity: 1}], Array.from({length: 17}, (_, i) => ({time: i / 10, x: 0, y: 0, opacity: 1}))]) expect(compositionError(clips, [{...layer, keyframes}], assets)).toBe('overlay')
  })
  it('splits source trims and cues without retaining alignment provenance or moving the outgoing join', () => {
    const clip = {id: 'one', asset_id: 'video', duration: 4, trim_start: 1, audio_asset_id: '', subtitle_source: 'asr' as const, subtitle_alignment_id: 'task', subtitle_cues: [{start: 1, end: 3, text: 'Across split'}], transition: 'wipeleft' as const, transition_duration: .5}
    const result = splitClip(clip, 2, true, 'two')!
    expect(result[0].transition).toBe('')
    expect(result[1].transition).toBe('wipeleft')
    expect(result[1].trim_start).toBe(3)
    expect(result[0].subtitle_cues).toEqual([{start: 1, end: 2, text: 'Across split'}])
    expect(result[1].subtitle_cues).toEqual([{start: 0, end: 1, text: 'Across split'}])
    expect(result[1].subtitle_source).toBe('manual')
    expect(result[1].subtitle_alignment_id).toBe('')
    expect(splitClip({...clip, audio_asset_id: 'voice'}, 2, true, 'two')).toBeNull()
    expect(splitClip(clip, .2, true, 'two')).toBeNull()
    expect(splitClip(clip, NaN, true, 'two')).toBeNull()
    expect(splitClip({...clip, trim_start: 0}, 2, false, 'two')![1].trim_start).toBe(0)
  })
})

it('renders bilingual preview controls with the synchronized audio preview boundary', () => {
  const previous = getActiveLocale()
  try {
    setActiveLocale('en')
    const english = renderToStaticMarkup(<TimelinePreview clips={[]} overlays={[]} assets={[]} aspect="16:9" subtitleMode="soft" time={0} onTime={() => {}} />)
    expect(english).toContain('Timeline composite canvas')
    expect(english).toContain('Preview synchronizes pictures')
    expect(english).toContain('Exact playhead (seconds)')
    expect(english).not.toMatch(/[\u3400-\u9fff]/)
    setActiveLocale('zh-CN')
    const chinese = renderToStaticMarkup(<TimelinePreview clips={[]} overlays={[]} assets={[]} aspect="9:16" subtitleMode="none" time={0} onTime={() => {}} />)
    expect(chinese).toContain('实时画面预览')
    expect(chinese).toContain('预览同步画面')
  } finally {setActiveLocale(previous)}
})
