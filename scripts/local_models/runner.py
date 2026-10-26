#!/usr/bin/env python3
"""Offline concrete model bridge; no weights/dependencies are installed here."""
import argparse
import array
import base64
import contextlib
import ctypes
import hashlib
import importlib.metadata
import io
import json
import math
import os
from pathlib import Path
import platform
import re
import resource
import shutil
import subprocess
import sys
import tempfile
import wave

ENGINES = {
    'wav2vec2-ctc-en': ('forced_alignment', 'wav2vec2-base-960h-en'),
    'wav2lip': ('lip_sync', 'wav2lip-local-research'),
    'audiogen': ('sound_effect', 'audiogen-medium-local-research'),
}
MAX_INPUT = 48 << 20


def network_guard():
    """Unprivileged, process-only Linux seccomp; inherited by model subprocesses.

    Fail closed outside the reviewed x86-64 ABI. No host firewall/DNS changes.
    Socket creation and io_uring are forbidden, including through x32 syscalls.
    """
    if platform.system() != 'Linux' or platform.machine() != 'x86_64':
        raise ValueError('unsupported isolation platform')
    class Filter(ctypes.Structure):
        _fields_ = [('code', ctypes.c_ushort), ('jt', ctypes.c_ubyte), ('jf', ctypes.c_ubyte), ('k', ctypes.c_uint)]
    class Program(ctypes.Structure):
        _fields_ = [('length', ctypes.c_ushort), ('filters', ctypes.POINTER(Filter))]
    # arch check; deny x32; deny network sockets and asynchronous syscall bypass.
    rules = [(0x20, 0, 0, 4), (0x15, 1, 0, 0xc000003e), (0x06, 0, 0, 0x80000000),
             (0x20, 0, 0, 0), (0x35, 0, 1, 0x40000000), (0x06, 0, 0, 0x50001)]
    for number in (41, 42, 43, 44, 45, 46, 47, 49, 50, 53, 288, 299, 307, 425, 426, 427):
        rules.extend([(0x15, 0, 1, number), (0x06, 0, 0, 0x50001)])
    rules.append((0x06, 0, 0, 0x7fff0000))
    data = (Filter * len(rules))(*(Filter(*r) for r in rules))
    prog = Program(len(rules), data)
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(38, 1, 0, 0, 0) != 0 or libc.prctl(22, 2, ctypes.byref(prog), 0, 0) != 0:
        raise ValueError('network isolation unavailable')


def regular(path):
    path = Path(path)
    if not path.is_absolute() or path.is_symlink() or not path.is_file():
        raise ValueError('explicit regular absolute file required')
    return path


def load_config(path):
    path = regular(path)
    if path.stat().st_size > 65536:
        raise ValueError('config too large')
    value = json.loads(path.read_text())
    allowed = {'version', 'engine', 'model', 'bundle', 'sha256', 'usage', 'source_revision', 'memory_limit_mib'}
    if not isinstance(value, dict) or set(value) - allowed or value.get('version') != 1 or value.get('engine') not in ENGINES:
        raise ValueError('invalid config')
    if value.get('model') != ENGINES[value['engine']][1]:
        raise ValueError('wrong concrete model contract')
    if value.get('usage') != ('research-noncommercial' if value['engine'] != 'wav2vec2-ctc-en' else 'operator-reviewed'):
        raise ValueError('explicit reviewed usage required')
    memory = value.get('memory_limit_mib')
    if type(memory) is not int or not 512 <= memory <= 16384:
        raise ValueError('explicit finite memory limit required')
    root = Path(value.get('bundle', ''))
    if not root.is_absolute() or root.is_symlink() or not root.is_dir():
        raise ValueError('explicit local bundle required')
    if not re.fullmatch('[0-9a-f]{40}', value.get('source_revision', '')):
        raise ValueError('operator-pinned source revision required')
    manifests = value.get('sha256')
    if not isinstance(manifests, dict) or not 1 <= len(manifests) <= 4096:
        raise ValueError('explicit artifact hashes required')
    # Reject symlinks and unmanifested model/code files: trusted bundles only.
    actual = set()
    total = 0
    for file in root.rglob('*'):
        if file.is_symlink():
            raise ValueError('bundle links forbidden')
        if not file.is_dir() and not file.is_file():
            raise ValueError('special bundle file forbidden')
        if file.is_file():
            relative = file.relative_to(root).as_posix()
            actual.add(relative)
            digest = manifests.get(relative)
            if not isinstance(digest, str) or not re.fullmatch('[0-9a-f]{64}', digest):
                raise ValueError('unmanifested artifact')
            total += file.stat().st_size
            if total > 12 << 30:
                raise ValueError('bundle exceeds bound')
            h = hashlib.sha256()
            with file.open('rb') as stream:
                for chunk in iter(lambda: stream.read(1 << 20), b''):
                    h.update(chunk)
            if h.hexdigest() != digest:
                raise ValueError('artifact hash mismatch')
    if actual != set(manifests):
        raise ValueError('missing artifact')
    required = {
        'wav2vec2-ctc-en': {'model/config.json', 'model/model.safetensors', 'model/vocab.json', 'model/preprocessor_config.json'},
        'wav2lip': {'repo/inference.py', 'repo/face_detection/detection/sfd/s3fd.pth', 'wav2lip.pth'},
        'audiogen': {'model/state_dict.bin', 'model/compression_state_dict.bin'},
    }[value['engine']]
    if not required <= actual:
        raise ValueError('incomplete model bundle')
    return value


def decode(value, limit):
    if not isinstance(value, str) or len(value) > (limit + 2) // 3 * 4:
        raise ValueError('media size invalid')
    result = base64.b64decode(value, validate=True)
    if not result or len(result) > limit:
        raise ValueError('media size invalid')
    return result


def command(args, timeout=20, **kwargs):
    return subprocess.run(args, check=True, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                          stderr=subprocess.DEVNULL, timeout=timeout, **kwargs)


def audio_input(payload, directory, max_seconds):
    mime = payload.get('mime_type', payload.get('audio_mime_type'))
    if mime not in ('audio/wav', 'audio/mpeg'):
        raise ValueError('unsupported audio')
    source, target = directory / 'input.audio', directory / 'input.wav'
    source.write_bytes(decode(payload.get('audio'), 24 << 20))
    command(['/usr/bin/ffmpeg', '-nostdin', '-v', 'error', '-protocol_whitelist', 'file,pipe',
             '-i', str(source), '-t', str(max_seconds + .1), '-ac', '1', '-ar', '16000', '-c:a', 'pcm_s16le', str(target)])
    with wave.open(str(target), 'rb') as stream:
        count, rate = stream.getnframes(), stream.getframerate()
        if count <= 0 or count > max_seconds * rate:
            raise ValueError('audio duration exceeds bound')
        samples = array.array('h', stream.readframes(count))
    if sys.byteorder != 'little':
        samples.byteswap()
    return target, [x / 32768 for x in samples], count / rate


def ctc_align(emissions, token_ids, blank):
    """Full CTC Viterbi path with required blanks between repeated labels.

    Returns one frame interval per target token. Scores are log probabilities;
    impossible paths fail rather than invent estimated timestamps.
    """
    frames, n = len(emissions), len(token_ids)
    if not 1 <= frames <= 1600 or not 1 <= n <= 400 or frames < n:
        raise ValueError('CTC dimensions outside bounds')
    vocab = len(emissions[0])
    if not 2 <= vocab <= 256 or blank < 0 or blank >= vocab or any(t < 0 or t >= vocab or t == blank for t in token_ids):
        raise ValueError('invalid CTC vocabulary')
    if any(len(row) != vocab or any(not math.isfinite(v) for v in row) for row in emissions):
        raise ValueError('invalid emissions')
    labels = [blank]
    for token in token_ids:
        labels.extend([token, blank])
    states = len(labels)
    scores = [-math.inf] * states
    scores[0] = emissions[0][blank]
    scores[1] = emissions[0][token_ids[0]]
    back = [bytearray(states)]
    for row in emissions[1:]:
        nxt = [-math.inf] * states
        moves = bytearray(states)
        for s, label in enumerate(labels):
            choices = [(scores[s], 0)]
            if s:
                choices.append((scores[s - 1], 1))
            if s > 1 and label != blank and label != labels[s - 2]:
                choices.append((scores[s - 2], 2))
            score, move = max(choices, key=lambda pair: pair[0])
            nxt[s] = score + row[label]
            moves[s] = move
        scores = nxt
        back.append(moves)
    state = states - 1 if scores[-1] >= scores[-2] else states - 2
    if not math.isfinite(scores[state]):
        raise ValueError('no valid CTC alignment')
    aligned = [[] for _ in token_ids]
    for frame in range(frames - 1, -1, -1):
        if state % 2:
            aligned[state // 2].append(frame)
        if frame:
            state -= back[frame][state]
    if any(not points for points in aligned):
        raise ValueError('incomplete CTC alignment')
    return [(min(points), max(points) + 1) for points in aligned]


def token_plan(text, vocab):
    if not isinstance(text, str) or not 1 <= len(text) <= 2000:
        raise ValueError('transcript required')
    words = text.split()
    tokens, ranges = [], []
    for word in words:
        if any(c.isdigit() or (c.isalpha() and c not in 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ') for c in word):
            raise ValueError('English-only alignment contract')
        normalized = re.sub("[^A-Z']", '', word.upper())
        if not normalized:
            raise ValueError('unpronounceable transcript token')
        if tokens:
            tokens.append(vocab['|'])
        start = len(tokens)
        tokens.extend(vocab[c] for c in normalized)
        ranges.append((start, len(tokens), word))
    if not 1 <= len(tokens) <= 400:
        raise ValueError('transcript too long')
    return tokens, ranges


def wav2vec2(payload, config, directory):
    if importlib.metadata.version('transformers') != '4.49.0':
        raise ValueError('reviewed Transformers 4.49.0 required')
    import torch
    from transformers import Wav2Vec2ForCTC, Wav2Vec2Processor
    target, samples, duration = audio_input(payload, directory, 30)
    del target
    stated = payload.get('duration')
    if not isinstance(stated, (int, float)) or not math.isfinite(stated) or abs(stated - duration) > .1:
        raise ValueError('audio duration mismatch')
    model_dir = str(Path(config['bundle']) / 'model')
    processor = Wav2Vec2Processor.from_pretrained(model_dir, local_files_only=True)
    model = Wav2Vec2ForCTC.from_pretrained(model_dir, local_files_only=True, use_safetensors=True).eval()
    if processor.feature_extractor.sampling_rate != 16000:
        raise ValueError('unsupported sample rate')
    ids, ranges = token_plan(payload.get('text'), processor.tokenizer.get_vocab())
    with torch.inference_mode():
        emissions = model(**processor(samples, sampling_rate=16000, return_tensors='pt')).logits.log_softmax(-1)[0].cpu().tolist()
    aligned = ctc_align(emissions, ids, processor.tokenizer.pad_token_id)
    ratio = duration / len(emissions)
    return {'cues': [{'start': aligned[a][0] * ratio, 'end': aligned[b-1][1] * ratio, 'text': word} for a, b, word in ranges]}


def audiogen(payload, config, directory):
    if importlib.metadata.version('audiocraft') != '1.3.0':
        raise ValueError('reviewed AudioCraft 1.3.0 required')
    prompt = payload.get('prompt')
    if not isinstance(prompt, str) or not 3 <= len(prompt.strip()) <= 2000:
        raise ValueError('prompt required')
    from audiocraft.models import AudioGen
    # Local directory loading is supported by v1.3.0 loaders.py. No HF repo ID.
    model = AudioGen.get_pretrained(str(Path(config['bundle']) / 'model'), device='cpu')
    model.set_generation_params(duration=5, use_sampling=False)
    tensor = model.generate([prompt], progress=False)[0].detach().cpu()
    rate = model.sample_rate
    if not isinstance(rate, int) or not 8000 <= rate <= 48000 or tensor.ndim != 2 or not 1 <= tensor.shape[0] <= 2 or not 1 <= tensor.shape[1] <= rate * 5:
        raise ValueError('invalid AudioGen output')
    channels = tensor.tolist()
    if len(channels) != tensor.shape[0] or any(len(ch) != tensor.shape[1] for ch in channels):
        raise ValueError('invalid waveform shape')
    pcm = array.array('h')
    for frame in zip(*channels):
        for value in frame:
            if not isinstance(value, (float, int)) or not math.isfinite(value):
                raise ValueError('nonfinite waveform')
            pcm.append(round(max(-1., min(1., value)) * 32767))
    if sys.byteorder != 'little':
        pcm.byteswap()
    out = io.BytesIO()
    with wave.open(out, 'wb') as stream:
        stream.setnchannels(tensor.shape[0]); stream.setsampwidth(2); stream.setframerate(rate)
        stream.writeframes(pcm.tobytes())
    return {'audio': base64.b64encode(out.getvalue()).decode()}


def wav2lip(payload, config, directory):
    bundle = Path(config['bundle'])
    video = decode(payload.get('video'), 10 << 20)
    if len(video) < 12 or video[4:8] != b'ftyp':
        raise ValueError('MP4 required')
    audio, _, duration = audio_input(payload, directory, 10)
    raw, normalized, output = directory / 'input.mp4', directory / 'face.mp4', directory / 'output.mp4'
    raw.write_bytes(video)
    # Fixed generated names also prevent the upstream script's shell-based
    # FFmpeg invocation from receiving tenant filenames or prompt text.
    command(['/usr/bin/ffmpeg', '-nostdin', '-v', 'error', '-protocol_whitelist', 'file,pipe', '-i', str(raw),
             '-t', str(duration), '-an', '-vf', 'scale=640:360:force_original_aspect_ratio=decrease,pad=640:360:(ow-iw)/2:(oh-ih)/2',
             '-r', '24', '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-fs', '10485760', str(normalized)])
    (directory / 'temp').mkdir()
    command([sys.executable, str(bundle / 'repo/inference.py'), '--checkpoint_path', str(bundle / 'wav2lip.pth'),
             '--face', str(normalized), '--audio', str(audio), '--outfile', str(output),
             '--face_det_batch_size', '1', '--wav2lip_batch_size', '1'], timeout=85, cwd=directory)
    if not output.is_file() or output.is_symlink() or not 12 <= output.stat().st_size <= 32 << 20:
        raise ValueError('invalid Wav2Lip output')
    probe = subprocess.run(['/usr/bin/ffprobe', '-v', 'error', '-protocol_whitelist', 'file,pipe',
                            '-show_entries', 'format=duration:stream=codec_type,width,height', '-of', 'json', str(output)],
                           check=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=10)
    if len(probe.stdout) > 4096:
        raise ValueError('invalid video metadata')
    metadata = json.loads(probe.stdout)
    seconds = float(metadata['format']['duration'])
    streams = metadata.get('streams', [])
    if not math.isfinite(seconds) or seconds <= 0 or abs(seconds - duration) > .2 or len(streams) > 2 or {v.get('codec_type') for v in streams} != {'video', 'audio'}:
        raise ValueError('output duration/streams mismatch')
    for stream in streams:
        if stream.get('codec_type') == 'video' and (stream.get('width') != 640 or stream.get('height') != 360):
            raise ValueError('output resolution mismatch')
    command(['/usr/bin/ffmpeg', '-nostdin', '-v', 'error', '-protocol_whitelist', 'file,pipe', '-i', str(output), '-f', 'null', '-'])
    return {'video': base64.b64encode(output.read_bytes()).decode()}


def run(payload, config, directory):
    operation, model = ENGINES[config['engine']]
    fields = {'forced_alignment': {'operation', 'model', 'audio', 'mime_type', 'duration', 'text'},
              'lip_sync': {'operation', 'model', 'video', 'audio', 'audio_mime_type'},
              'sound_effect': {'operation', 'model', 'prompt'}}
    if not isinstance(payload, dict) or set(payload) != fields[operation] or payload.get('operation') != operation or payload.get('model') != model:
        raise ValueError('configured operation/model mismatch')
    return {'wav2vec2-ctc-en': wav2vec2, 'audiogen': audiogen, 'wav2lip': wav2lip}[config['engine']](payload, config, directory)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    args = parser.parse_args()
    try:
        config = load_config(args.config)
        raw = sys.stdin.buffer.read(MAX_INPUT + 1)
        if len(raw) > MAX_INPUT:
            raise ValueError('input exceeds bound')
        payload = json.loads(raw)
        with tempfile.TemporaryDirectory(prefix='studio-engine-') as tmp:
            directory = Path(tmp)
            # No host tokens, credentials, proxy settings or default shared caches.
            os.environ.clear()
            os.environ.update({'PATH': '/usr/bin:/bin', 'LANG': 'C.UTF-8', 'HF_HUB_OFFLINE': '1',
                               'TRANSFORMERS_OFFLINE': '1', 'HF_HUB_DISABLE_TELEMETRY': '1',
                               'HF_HOME': str(directory / 'cache'), 'TORCH_HOME': str(directory / 'torch'),
                               'XDG_CACHE_HOME': str(directory / 'cache'), 'TOKENIZERS_PARALLELISM': 'false',
                               'CUDA_VISIBLE_DEVICES': '', 'OMP_NUM_THREADS': '2', 'MKL_NUM_THREADS': '2', 'OPENBLAS_NUM_THREADS': '2',
                               'PYTHONDONTWRITEBYTECODE': '1'})
            sys.dont_write_bytecode = True
            # Never let framework lock/cache metadata mutate a reviewed bundle.
            # Optional conditioning cache is hash-checked with the bundle, then
            # copied into this run's private cache with a separate scratch bound.
            source_cache = Path(config['bundle']) / 'hf-cache'
            if source_cache.is_dir():
                size = sum(f.stat().st_size for f in source_cache.rglob('*') if f.is_file())
                if size > 1 << 30 or shutil.disk_usage(directory).free < size + (64 << 20):
                    raise ValueError('conditioning cache exceeds scratch allowance')
                shutil.copytree(source_cache, directory / 'cache')
            memory = config['memory_limit_mib'] << 20
            resource.setrlimit(resource.RLIMIT_AS, (memory, memory))
            resource.setrlimit(resource.RLIMIT_CPU, (110, 110))
            resource.setrlimit(resource.RLIMIT_FSIZE, (40 << 20, 40 << 20))
            network_guard()
            with open(os.devnull, 'w') as quiet, contextlib.redirect_stdout(quiet):
                result = run(payload, config, directory)
            encoded = json.dumps(result, allow_nan=False).encode()
            if len(encoded) > 45 << 20:
                raise ValueError('output exceeds bound')
            sys.stdout.buffer.write(encoded)
    except Exception:
        print('local model execution unavailable or rejected', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
