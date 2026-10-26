# Text-only transcription contract

`openai-asr` with the exact `gpt-transcribe` model is available only through the
managed runtime and explicit `mode: "transcription"`. It uses multipart
`POST /v1/audio/transcriptions`, `model=gpt-transcribe`, `response_format=json`.
It does not send `timestamp_granularities`, and does not synthesize cue times.
The existing `whisper-1`/`mode: "asr"` timestamp contract remains separate.
No automatic model substitution occurs.

The alignment quote/acceptance endpoints use `operation: "alignment"` and the
existing `asr` configuration capability. Input must reference the currently saved
clip's own audio, WAV/MP3, at most 24 MiB and **30 seconds**. The quote binds mode,
model, exact audio bytes, duration, clip timing and configuration version. Existing
original-currency reservations, dispatch-unknown accounting, authorization,
revocation, input/output review and independent worker execution apply.

A successful text task has `mode: "transcription"`, `timing_basis:
"transcript-only"`, nonempty `transcript` of at most 2000 Unicode characters, and
`cues: []`. The UI displays escaped text in Chinese/English context and offers no
subtitle application button. The server also rejects this task as subtitle
provenance, including attempts to label it manual. Persistence validates this
text-only result and never turns it into timed cues. Longer/empty transcription
responses fail explicitly; an empty result for a non-speech fixture is not proof
of a vendor failure or model quality.

The evaluation harness case14 uses application-generated synthetic tone media
under 30 seconds. This verifies protocol plumbing only; it is not intelligible
speech or a word-accuracy benchmark. No hosted transcription has been performed.
Actual quality, permission, latency and billing remain unverified; account/session
and fresh quote/moderation configuration are still required before authorized live
execution. No secrets were read or configured by implementation/tests.

Official contract reviewed 2026-10-26:
[OpenAI speech-to-text guide](https://developers.openai.com/api/docs/guides/speech-to-text).
The guide's `gpt-transcribe` JSON text/languages contract is distinct from the
legacy Whisper verbose timestamp format. Its published Whisper retirement date
must be tracked separately from availability of text-only transcription.
