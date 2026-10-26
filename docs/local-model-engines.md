# Concrete offline model bridges (not model-quality acceptance)

`scripts/local_models/runner.py` implements three concrete adapters behind the existing
Go local-transform JSON protocol. It does not install packages, download weights,
call a hosted model, configure a credential or enable a provider automatically.
The old generic CLI contract alone was not a model implementation; these adapters
now provide actual inference calls and an English CTC alignment algorithm.

| Adapter / application model ID | Implemented operation | Remaining qualification |
| --- | --- | --- |
| `wav2vec2-ctc-en` / `wav2vec2-base-960h-en` | Transformers 4.49.0 `Wav2Vec2Processor` and `Wav2Vec2ForCTC`, local-only safetensors; full CTC Viterbi alignment, required blank separation for repeated labels, original word/punctuation text retained in cues | English letters/apostrophes only (numeric tokens, including mixed forms such as A1, are rejected), at most400 target tokens/30s audio. No multilingual normalization or alignment accuracy measurement; mismatched speech can still align poorly and requires review. |
| `wav2lip` / `wav2lip-local-research` | Explicit reviewed upstream checkout, fixed `inference.py --checkpoint_path --face --audio --outfile` arguments, batches1, 640×360/24fps local input normalization; output streams/duration/resolution and actual decode validated | Upstream open-source model is restricted to noncommercial research/academic/personal use. No Sync hosted API fallback. Actual weights, environment compatibility and lip quality have not been tested here. |
| `audiogen` / `audiogen-medium-local-research` | AudioCraft1.3.0 `AudioGen.get_pretrained(local_directory, device='cpu')`, 5-second deterministic generation configuration, bounded finite waveform→PCM WAV | Model weights use CC-BY-NC4.0. Complete offline compression, LM and text-conditioning artifacts required; no actual weights or generated-sound quality tested. |

These are optional, research-oriented local adapters. Wav2Lip and AudioGen default
to refusal unless the operator config explicitly says `research-noncommercial`.
That declaration does not grant a licence or make commercial deployment permissible.
The adapters are not enabled by the Docker image or application defaults.

## Trust, offline isolation and limits

A trusted deployment operator supplies a private JSON config and a fully prepared
local bundle; tenants cannot submit paths, commands, package versions or weights.
All regular bundle files must appear in the SHA256 manifest; missing, changed,
extra, linked or special files are rejected before loading a model. Only trusted,
reviewed weights/code are acceptable: AudioCraft and upstream Wav2Lip use Python
checkpoint formats and upstream code execution. Hashing detects change, not
malicious contents. `source_revision` records the operator-reviewed40-hex upstream
revision; this tool does **not** independently authenticate that revision or fetch Git.
It must match the code the operator audited, together with the manifest.

The runner removes inherited environment values and uses explicit local HF/Torch
cache paths, disables Hub/Transformers downloads and telemetry, and hides GPUs.
Before importing a model, Linux x86-64 `no_new_privs`/seccomp denies socket syscalls
and io_uring, inherited by FFmpeg and model subprocesses. This is process-local;
it changes no host DNS/firewall settings. Unsupported ABIs or filter-install
failure reject execution. It is a network restriction, **not** a general sandbox
against malicious operator-installed code or filesystem access.

Limits:48MiB JSON input,45MiB JSON output; audio30s for alignment and10s for lip
sync; lip input10MiB MP4, output32MiB; sound5s;110s process CPU,40MiB individual
output file; required `memory_limit_mib`512..16384 sets a finite RLIMIT_AS. Two
CPU threads are requested from common libraries. The existing Go runner also kills
the process group at120s. Models may fail within these resource limits. AudioGen CPU generation may exceed
the120s application timeout; actual speed and memory requirements remain unmeasured. Deployment
must additionally cap aggregate worker memory/CPU with its normal container/cgroup
limits; per-process bounds are not host-wide capacity or OOM isolation guarantees.

## Operator setup (not performed by this task)

Use separate offline virtual environments where framework dependencies conflict.
Install and review their dependencies through normal approved package sources;
no bootstrap script executes a model download. Use an already-downloaded,
reviewed local snapshot with safetensors for Wav2Vec2. AudioGen needs
`model/state_dict.bin`, `model/compression_state_dict.bin` and complete local
conditioning resources in optional `hf-cache/`; remote identifiers in a checkpoint
cannot download anything under the process guard and will fail if not cached.
Prepare cache snapshots as regular files, not symlinks. A supplied HF cache is capped
at1GiB and copied into the private run directory before use; framework cache writes
and bytecode never modify the original bundle. Keep prepared bundles
immutable to avoid concurrent changes between hashing and engine loading.

A config has exactly these fields (illustrative values intentionally do not validate):

```json
{
  "version": 1,
  "engine": "wav2vec2-ctc-en",
  "model": "wav2vec2-base-960h-en",
  "bundle": "/operator/prepared/wav2vec2",
  "source_revision": "REPLACE_WITH_REVIEWED_40_HEX_REVISION",
  "usage": "operator-reviewed",
  "memory_limit_mib": 4096,
  "sha256": {
    "model/config.json": "REPLACE_WITH_SHA256_OF_EACH_FILE"
  }
}
```

Wav2Vec2 additionally requires `model/model.safetensors`, `model/vocab.json`,
`model/preprocessor_config.json` and tokenizer files needed by that local processor.
Wav2Lip requires `repo/inference.py`, its reviewed supporting source,
`repo/face_detection/detection/sfd/s3fd.pth` and `wav2lip.pth`. AudioGen uses the
files described above. Include **all** files in the manifest. A bundle path is
never an HTTP URL or Hugging Face repository ID.

The existing Go configuration accepts one executable without arguments. A trusted
operator can create a wrapper using fixed, reviewed absolute paths, for example:

```sh
#!/bin/sh
exec /operator/venv/bin/python /operator/studio/scripts/local_models/runner.py --config /operator/config/wav2vec2.json
```

Set `FORCED_ALIGNMENT_COMMAND` to that executable and `FORCED_ALIGNMENT_MODEL` to
`wav2vec2-base-960h-en`; analogous `LIP_SYNC_*`/`SOUND_EFFECT_*` settings use their
model IDs from the table. No wrapper or model is installed by this change. A
configured executable being present is not proof of inference readiness; perform
an explicit offline sample and inspect results before exposing it to users.

## Evidence and official contracts

`python3 scripts/local_models/test_runner.py` validates CTC repeated-label paths,
English/transcript restrictions, manifest/symlink/FIFO/memory bounds, actual Linux
socket denial inherited by subprocesses, actual FFmpeg audio decoding, concrete
Wav2Vec2/AudioGen call arguments with fake engines, and Wav2Lip argument+media
validation using a fake inference stage. Fake engines and synthetic emissions
**do not prove pretrained-model inference or model quality**. PyTorch,
Transformers, AudioCraft and actual checkpoints were not installed or executed.

Contracts reviewed on2026-10-26:

- [Transformers4.49 Wav2Vec2 API](https://huggingface.co/docs/transformers/v4.49.0/model_doc/wav2vec2).
- [AudioCraft1.3 local checkpoint loader](https://github.com/facebookresearch/audiocraft/blob/v1.3.0/audiocraft/models/loaders.py), [AudioGen API](https://github.com/facebookresearch/audiocraft/blob/v1.3.0/audiocraft/models/audiogen.py), [weight licence](https://github.com/facebookresearch/audiocraft/blob/v1.3.0/LICENSE_weights).
- [Wav2Lip inference arguments](https://github.com/Rudrabha/Wav2Lip/blob/master/inference.py) and [upstream usage restrictions](https://github.com/Rudrabha/Wav2Lip#non-commercial-open-source-version).

A request to the GitHub commit API returned HTTP403; it was not retried or bypassed.
No newly fetched full Wav2Lip revision is claimed. The known inference argument
contract and operator-manifested source are separate from verifying a particular
installed upstream revision. TorchAudio's old forced-align API is not used: its
[2.8 documentation](https://docs.pytorch.org/audio/2.8.0/tutorials/ctc_forced_alignment_api_tutorial.html)
states removal in2.9, so this bridge uses its own CTC path computation.
