# Crossfade and bounded visual layers

Timeline JSON remains additive: old clips with an empty transition or `fade`
and no `video_overlays` keep their existing behavior. Existing version/CAS,
idempotency and authenticated project/tenant ownership remain required.

## Timing and sound

`transition: "crossfade"` belongs to the outgoing clip and blends into the next
clip. It requires a next clip, 0.1–2 seconds, and at most half of **each** adjacent
clip. The final clip cannot crossfade. Video uses a linear dissolve; the prepared
clip audio uses a triangular crossfade. Existing explicit source-audio and
voice-volume controls are applied before that crossfade. Silent clips stay silent.
`fade` still fades through black at clip edges without overlapping the timeline.

Movie duration is the sum of clip durations minus outgoing crossfade durations.
Each clip's local subtitle cues are shifted to its resulting movie start and
sorted before writing SRT/VTT or burning subtitles. During a dissolve both clips'
subtitle cues may overlap; this is preserved, not silently removed. Background
music, extra audio tracks and visual layers use the **shortened movie clock**.
Saving rejects tracks or layers extending past that clock. Export and UI duration
both use the same subtraction. Output remains bounded at 24fps; times are rounded
to encoded frames/samples rather than promising sub-frame editing.

## Visual layers

`video_overlays` accepts up to four ordered image or video layers. The last entry
is on top. Each contains `id`, `asset_id`, `start`, `trim_start`, `duration`,
`x`, `y`, `width`, `height`, `opacity`, `preserve_source_audio`, and
`source_audio_volume`. Position/size fractions must fit the output canvas; width
and height are at least 0.1, opacity is 0–1. A layer lasts at least 0.1 seconds and
fits within the final movie. Video trim must fit its source duration. Images have
zero trim and no source audio. A muted layer must have volume zero.

Layers preserve source aspect ratio inside their rectangle with transparent
padding, rather than stretching content. Original transparency and layer opacity
compose together. Source sound is opt-in, trimmed, delayed to the layer start,
then mixed and limited; image layers cannot claim source audio. A silent video has
no audio stream to mix. Layers stop at their configured end and do not freeze or
repeat after it. Global subtitles are applied after visual layers.

All assets must belong to the current project. Both in-process and PostgreSQL
export workers include layer assets when materializing verified private input
files. Unknown/cross-project assets, NaN/infinity, invalid IDs, duplicate layer
IDs, out-of-bounds rectangles and excess layers are rejected. Arbitrary filter
expressions and external URLs are never accepted as layer input.

The frontend provides persisted editing, z-order, time/rectangle/opacity and
explicit audio controls in Chinese and English, including error/ARIA text. It
shows the resulting total duration and refuses invalid saves. The viewer remains
an exported-file viewer: there is **no live composition preview**, keyframes,
chroma key or professional NLE claim.

## Resource and rollback boundaries

Clip merging is pairwise; every merge or overlay pass uses two inputs. Intermediate
composite movies are capped at 128MiB and checked for duration/audio-video streams;
superseded intermediate movies are deleted. Existing resolution/duration,
concurrency, object-size and export limits still apply. Repeated encoding trades
performance for bounded input count and is not lossless mastering.

New binaries read old timelines. Do not run old binaries against timelines using
new layer fields: an old decoder could ignore unknown fields. Rollback requires a
stopped, matching pre-upgrade complete metadata/media backup, or an explicitly
reviewed conversion that removes new composition features. Merely switching the
binary is not a lossless rollback.

## Verification

`TestCompositeTimelineBounds` covers crossfade/overlay bounds and ownership lookup.
`TestCompositeActualPixelsAudioAndSubtitles` creates synthetic PNG and actual MP4
media and checks decoded blend pixels, rectangle boundaries, opacity and stacking,
clip/overlay PCM audio, shortened duration, subtitle offsets and mixed
crossfade/straight-cut joins. Existing enhanced-media tests continue to cover
720p/1080p/portrait/square and burned subtitles. All are local FFmpeg evidence,
not real model quality or production throughput evidence.

The frontend adds seven composition unit tests and a desktop/mobile browser test
that edits, saves, exports, checks duration and reloads the saved composition.
Final run results are recorded in the parent milestone acceptance report.
