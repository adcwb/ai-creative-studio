# Bounded timeline editing, keyframes and UHD export

This iteration extends the existing persisted timeline, rather than introducing a
separate editor-only document. Existing clips, overlays and exports remain readable.

## Composition contract

- Each outgoing join can select crossfade, wipeleft, wiperight, slideleft,
  slideright or circleopen. The allowlist maps to built-in FFmpeg xfade filters;
  arbitrary filter expressions are never accepted. Different joins can use
  different transitions. Every overlapping transition subtracts its duration from
  the movie clock and crossfades prepared audio. Duration is 0.1–2s, no more than
  half each adjacent clip, and requires a following clip. Existing fade remains
  a non-overlapping fade through black. Subtitle offsets, extra sound tracks and
  overlays use the resulting shortened movie clock.
- Each of at most four visual layers may have up to 16 `keyframes` containing
  `{time,x,y,opacity}`. Time is relative to layer start, begins at zero and strictly
  increases through layer duration. Positions are normalized output-canvas
  coordinates; opacity is 0–1. Width/height remain fixed and every rectangle must
  fit. Values interpolate linearly and hold after the last keyframe. One keyframe
  is a static override. No keyframes preserves the legacy static layer settings.
- Export generates bounded numeric FFmpeg expressions server-side. Position is
  sampled on the movie clock at 24fps and rounded down to even output pixels;
  per-frame alpha multiplies source transparency. Layer visibility/audio intervals
  and original project-asset ownership checks remain enforced.

## UHD resource policy

`output_resolution: "2160p"` encodes actual 3840×2160 landscape, 2160×3840 portrait
or 2160×2160 square H.264/AAC at 24fps. The complete movie is limited to 15 seconds,
24M maximum video rate, 128MiB output/intermediate files, existing 5-minute export
worker deadline and single-threaded encoding. Exceeding limits fails explicitly.
This is a bounded UHD export workflow, not a claim of unlimited 4K editing,
lossless mastering, GPU acceleration or measured production capacity. Preview
uses a smaller browser canvas; export dimensions are independent.

## Safety, compatibility and evidence

The timeline uses existing version/CAS, idempotency, tenant/project ownership and
worker snapshot semantics. New keyframe fields persist with the timeline and
export snapshot. Nonfinite values, excess keys, invalid rectangles and times,
foreign assets, arbitrary transitions and excessive UHD duration are rejected.

`TestKeyframeAnd4KBounds` covers these new bounds. Actual FFmpeg tests decode
motion/alpha pixels, verify endpoint holding and visibility intervals, mixed
spatial transitions and actual 3840×2160, 2160×3840 and 2160×2160 frames. Existing composite tests cover
sound, subtitle timing and static source transparency. Run results belong in the
iteration acceptance report; fixture tests do not prove external model quality.

Rollback requires a matching stopped metadata/media backup predating new
keyframes/transitions/UHD settings. Older programs can ignore unknown JSON fields
and render a different composition; binary-only downgrade is not lossless.
