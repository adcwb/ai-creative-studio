import base64
import hashlib
import importlib.util
import io
import json
import math
from pathlib import Path
import subprocess
import sys
import tempfile
import types
import unittest
from unittest.mock import patch
import wave

spec = importlib.util.spec_from_file_location('studio_local_runner', Path(__file__).with_name('runner.py'))
r = importlib.util.module_from_spec(spec)
spec.loader.exec_module(r)


class LocalModelTests(unittest.TestCase):
    def config(self, folder, engine='wav2vec2-ctc-en'):
        bundle = folder / 'bundle'; bundle.mkdir()
        required = {'wav2vec2-ctc-en': ['model/config.json', 'model/model.safetensors', 'model/vocab.json', 'model/preprocessor_config.json'],
                    'wav2lip': ['repo/inference.py', 'repo/face_detection/detection/sfd/s3fd.pth', 'wav2lip.pth'],
                    'audiogen': ['model/state_dict.bin', 'model/compression_state_dict.bin']}[engine]
        hashes = {}
        for name in required:
            target = bundle / name; target.parent.mkdir(parents=True, exist_ok=True); target.write_bytes(b'synthetic-fixture-not-model')
            hashes[name] = hashlib.sha256(target.read_bytes()).hexdigest()
        value = {'version': 1, 'engine': engine, 'model': r.ENGINES[engine][1], 'bundle': str(bundle), 'sha256': hashes,
                 'usage': 'operator-reviewed' if engine == 'wav2vec2-ctc-en' else 'research-noncommercial', 'source_revision': 'a' * 40, 'memory_limit_mib': 2048}
        path = folder / 'config.json'; path.write_text(json.dumps(value))
        return path, value

    def test_manifest_missing_modified_extra_and_links_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); path, config = self.config(root)
            self.assertEqual(r.load_config(path)['engine'], 'wav2vec2-ctc-en')
            file = Path(config['bundle']) / 'model/config.json'
            file.write_bytes(b'changed')
            with self.assertRaises(ValueError): r.load_config(path)
            file.unlink()
            with self.assertRaises(ValueError): r.load_config(path)
            file.symlink_to('/etc/passwd')
            with self.assertRaises(ValueError): r.load_config(path)

    def test_memory_bound_and_special_file_rejected(self):
        import os
        with tempfile.TemporaryDirectory() as tmp:
            path, config = self.config(Path(tmp))
            for value in (None, 0, True, 16385, '4096'):
                config['memory_limit_mib'] = value; path.write_text(json.dumps(config))
                with self.subTest(value=value), self.assertRaises(ValueError): r.load_config(path)
            config['memory_limit_mib'] = 2048; path.write_text(json.dumps(config))
            os.mkfifo(Path(config['bundle']) / 'model/unmanifested-pipe')
            with self.assertRaises(ValueError): r.load_config(path)

    def test_usage_default_denies_restricted_engines(self):
        for engine in ('wav2lip', 'audiogen'):
            with self.subTest(engine=engine), tempfile.TemporaryDirectory() as tmp:
                path, config = self.config(Path(tmp), engine)
                self.assertEqual(r.load_config(path)['engine'], engine)
                config['usage'] = 'commercial'; path.write_text(json.dumps(config))
                with self.assertRaises(ValueError): r.load_config(path)
                del config['usage']; path.write_text(json.dumps(config))
                with self.assertRaises(ValueError): r.load_config(path)

    def test_ctc_repeated_tokens_need_blank_and_preserve_frames(self):
        # Expected CTC sequence: blank A A blank A blank B blank.
        seq = [0, 1, 1, 0, 1, 0, 2, 0]
        rows = [[0 if i == label else -20 for i in range(3)] for label in seq]
        self.assertEqual(r.ctc_align(rows, [1, 1, 2], 0), [(1, 3), (4, 5), (6, 7)])
        with self.assertRaises(ValueError): r.ctc_align([[0, -1], [0, -1]], [1, 1], 0)
        with self.assertRaises(ValueError): r.ctc_align([[math.nan, 0]], [1], 0)
        with self.assertRaises(ValueError): r.ctc_align([[0, -1]], [0], 0)

    def test_transcript_keeps_original_words_and_rejects_unimplemented_languages(self):
        vocab = {'A': 1, 'B': 2, '|': 3, "'": 4}
        ids, words = r.token_plan('A,  B!', vocab)
        self.assertEqual(ids, [1, 3, 2]); self.assertEqual(words, [(0, 1, 'A,'), (2, 3, 'B!')])
        for text in ('你好', '123', 'A1', '!!!', ''):
            with self.subTest(text=text), self.assertRaises(ValueError): r.token_plan(text, vocab)

    def test_actual_process_guard_blocks_sockets_and_inherited_subprocess(self):
        code = f'''import importlib.util,socket,subprocess,sys
spec=importlib.util.spec_from_file_location("guard",{str(Path(r.__file__).resolve())!r})
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m);m.network_guard()
for family in (socket.AF_INET,socket.AF_INET6,socket.AF_UNIX):
 try: socket.socket(family);raise AssertionError("socket allowed")
 except PermissionError: pass
p=subprocess.run([sys.executable,"-c","import socket; socket.socket()"],capture_output=True)
assert p.returncode != 0 and b"PermissionError" in p.stderr
print("network denied")'''
        result = subprocess.run([sys.executable, '-c', code], env={'PATH': '/usr/bin:/bin'}, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), 'network denied')

    def test_audio_decoding_uses_real_ffmpeg_and_rejects_duration(self):
        out = io.BytesIO()
        with wave.open(out, 'wb') as w:
            w.setnchannels(1); w.setsampwidth(2); w.setframerate(16000); w.writeframes(b'\0\0' * 1600)
        payload = {'audio': base64.b64encode(out.getvalue()).decode(), 'mime_type': 'audio/wav'}
        with tempfile.TemporaryDirectory() as tmp:
            _, samples, duration = r.audio_input(payload, Path(tmp), 1)
            self.assertEqual(len(samples), 1600); self.assertAlmostEqual(duration, .1)
        with tempfile.TemporaryDirectory() as tmp, self.assertRaises(ValueError):
            r.audio_input(payload, Path(tmp), .05)

    def test_audiogen_concrete_call_and_wav_encoding_with_fake_model(self):
        events = []
        class Tensor:
            def __getitem__(self, _): return self
            def detach(self): return self
            def cpu(self): return self
            ndim = 2
            shape = (1, 16000)
            def tolist(self): return [[0.] * 16000]
        class Model:
            sample_rate = 16000
            def set_generation_params(self, **kwargs): events.append(('params', kwargs))
            def generate(self, prompts, progress): events.append(('generate', prompts, progress)); return Tensor()
        def get_pretrained(path, device): events.append(('load', path, device)); return Model()
        package = types.ModuleType('audiocraft.models'); package.AudioGen = types.SimpleNamespace(get_pretrained=get_pretrained)
        with patch.dict(sys.modules, {'audiocraft': types.ModuleType('audiocraft'), 'audiocraft.models': package}), patch.object(r.importlib.metadata, 'version', return_value='1.3.0'):
            result = r.audiogen({'prompt': 'Synthetic rain'}, {'bundle': '/explicit/synthetic/bundle'}, Path('/tmp'))
        self.assertEqual(events[0], ('load', '/explicit/synthetic/bundle/model', 'cpu'))
        self.assertEqual(events[1], ('params', {'duration': 5, 'use_sampling': False}))
        with wave.open(io.BytesIO(base64.b64decode(result['audio']))) as w: self.assertEqual(w.getnframes(), 16000)

    def test_wav2vec2_local_only_inference_and_word_cues_with_fake_model(self):
        events = []
        class Tensor:
            def log_softmax(self, _): return self
            def __getitem__(self, _): return self
            def cpu(self): return self
            def tolist(self): return [[0, -20, -20, -20], [-20, 0, -20, -20], [0, -20, -20, -20], [-20, -20, -20, 0], [-20, -20, 0, -20], [0, -20, -20, -20]]
        class Processor:
            feature_extractor = types.SimpleNamespace(sampling_rate=16000)
            tokenizer = types.SimpleNamespace(get_vocab=lambda: {'A': 1, 'B': 2, '|': 3}, pad_token_id=0)
            def __call__(self, samples, **kwargs): events.append(('features', len(samples), kwargs)); return {'input_values': samples}
            @staticmethod
            def from_pretrained(path, **kwargs): events.append(('processor', path, kwargs)); return Processor()
        class Model:
            @staticmethod
            def from_pretrained(path, **kwargs): events.append(('model', path, kwargs)); return Model()
            def eval(self): return self
            def __call__(self, **_): return types.SimpleNamespace(logits=Tensor())
        import contextlib
        modules = {'torch': types.SimpleNamespace(inference_mode=contextlib.nullcontext),
                   'transformers': types.SimpleNamespace(Wav2Vec2Processor=Processor, Wav2Vec2ForCTC=Model)}
        with patch.dict(sys.modules, modules), patch.object(r.importlib.metadata, 'version', return_value='4.49.0'), patch.object(r, 'audio_input', return_value=(Path('/tmp/synthetic.wav'), [0.] * 16000, 1.)):
            result = r.wav2vec2({'text': 'A, B!', 'duration': 1.}, {'bundle': '/explicit/synthetic/bundle'}, Path('/tmp'))
        self.assertEqual([c['text'] for c in result['cues']], ['A,', 'B!'])
        self.assertEqual(events[0][2], {'local_files_only': True})
        self.assertEqual(events[1][2], {'local_files_only': True, 'use_safetensors': True})
        self.assertLessEqual(result['cues'][0]['end'], result['cues'][1]['start'])

    def test_wav2lip_fixed_arguments_and_real_media_validation_with_fake_inference(self):
        with tempfile.TemporaryDirectory() as tmp:
            folder = Path(tmp); source = folder / 'synthetic.mp4'; work = folder / 'work'; work.mkdir()
            r.command(['/usr/bin/ffmpeg', '-v', 'error', '-f', 'lavfi', '-i', 'color=c=blue:s=320x180:r=24:d=0.5', '-c:v', 'libx264', '-pix_fmt', 'yuv420p', str(source)])
            out = io.BytesIO()
            with wave.open(out, 'wb') as w:
                w.setnchannels(1); w.setsampwidth(2); w.setframerate(16000); w.writeframes(b'\0\0' * 8000)
            payload = {'video': base64.b64encode(source.read_bytes()).decode(), 'audio': base64.b64encode(out.getvalue()).decode(), 'audio_mime_type': 'audio/wav'}
            original = r.command
            calls = []
            def fake_inference(args, **kwargs):
                if args[0] != sys.executable: return original(args, **kwargs)
                calls.append(args)
                self.assertEqual(kwargs['timeout'], 85)
                self.assertIn('/repo/inference.py', args[1])
                self.assertEqual(args[-4:], ['--face_det_batch_size', '1', '--wav2lip_batch_size', '1'])
                face, audio, output = (args[args.index(name) + 1] for name in ('--face', '--audio', '--outfile'))
                return original(['/usr/bin/ffmpeg', '-v', 'error', '-i', face, '-i', audio, '-c:v', 'copy', '-c:a', 'aac', '-shortest', output])
            with patch.object(r, 'command', side_effect=fake_inference):
                result = r.wav2lip(payload, {'bundle': str(folder / 'bundle')}, work)
            self.assertEqual(len(calls), 1)
            self.assertEqual(base64.b64decode(result['video'])[4:8], b'ftyp')

    def test_no_model_switch_or_extra_fields(self):
        with patch.object(r, 'wav2vec2', side_effect=AssertionError('inference')):
            for payload in ({'operation': 'lip_sync', 'model': 'wav2vec2-base-960h-en'}, {'operation': 'forced_alignment', 'model': 'wrong'}):
                with self.assertRaises(ValueError): r.run(payload, {'engine': 'wav2vec2-ctc-en'}, Path('/tmp'))

    def test_cli_missing_config_has_generic_error_no_model_load(self):
        result = subprocess.run([sys.executable, str(Path(r.__file__)), '--config', '/missing-synthetic-model-config'], input=b'{}', capture_output=True)
        self.assertEqual(result.returncode, 1); self.assertEqual(result.stdout, b'')
        self.assertEqual(result.stderr.strip(), b'local model execution unavailable or rejected')


if __name__ == '__main__':
    unittest.main()
