#!/usr/bin/env python3
"""Isolated loopback Compose build and acceptance; never pushes.
Use --no-build and image arguments to test existing local images. No provider env or
repository .env is loaded. Only this run's randomly named volume is removed.
"""
import argparse
import copy
import http.client
import json
import os
from pathlib import Path
import re
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--no-build', action='store_true', help='Use already built local images')
    parser.add_argument('--backend-image')
    parser.add_argument('--frontend-image')
    args = parser.parse_args()
    evidence = Path(tempfile.mkdtemp(prefix='studio-docker-evidence-'))
    project = 'studio-qa-' + uuid.uuid4().hex[:12]
    args.backend_image = args.backend_image or project + '-backend:latest'
    args.frontend_image = args.frontend_image or project + '-frontend:latest'
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    empty_env = evidence / 'empty.env'
    empty_env.write_text('')
    override = evidence / 'override.json'
    override.write_text(json.dumps({'services': {
        'backend': {'image': args.backend_image, 'pull_policy': 'never', 'restart': 'no', 'environment': {
            'IMAGE_PROVIDER': 'mock-svg', 'TEXT_PROVIDER': 'mock-text', 'ASR_PROVIDER': '',
            'FRONTEND_ORIGIN': base}},
        'frontend': {'image': args.frontend_image, 'pull_policy': 'never', 'restart': 'no'},
    }}))
    config = str(evidence / 'docker-config')
    Path(config).mkdir()
    env = {'PATH': os.environ['PATH'], 'DOCKER_CONFIG': config, 'STUDIO_PORT': str(port)}
    command = ['docker', 'compose', '--env-file', str(empty_env), '-p', project,
               '-f', str(ROOT / 'compose.yaml'), '-f', str(override)]
    def compose(*parts):
        result = subprocess.run(command + list(parts), env=env, cwd=ROOT, capture_output=True, text=True, timeout=1200 if parts[0] == 'build' else 180)
        with (evidence / 'compose.log').open('a') as log:
            log.write(' '.join(parts) + '\n' + result.stdout + result.stderr)
        result.check_returncode()
        return result.stdout
    def call(path, body=None, key=None, method=None, status=200, mime='application/json', headers=None):
        hdr = {'Content-Type': mime, **(headers or {})}
        if key:
            hdr['Idempotency-Key'] = key
        raw = body if isinstance(body, bytes) else json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(base + path, data=raw, headers=hdr, method=method)
        try:
            response = urllib.request.urlopen(req, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            value = response.read()
            assert response.status == status, (path, response.status, status, value[:200])
            return json.loads(value) if 'application/json' in response.headers.get('Content-Type', '') else value
    def ready():
        for _ in range(150):
            try:
                call('/healthz')
                return
            except (OSError, AssertionError):
                time.sleep(.2)
        raise AssertionError('container proxy health timeout')
    checks = []
    print(json.dumps({'evidence': str(evidence), 'compose_project': project, 'model_validation': False}), flush=True)
    try:
        if not args.no_build:
            compose('build')
        compose('up', '-d', '--no-build', '--wait', '--wait-timeout', '120')
        ready()
        assert b'ok' in call('/readyz')
        html = call('/').decode()
        asset = re.search(r'src="(/assets/[^\"]+\.js)"', html)
        assert asset and len(call(asset.group(1))) > 1000
        assert call('/nested/client/route') == html.encode()
        checks.append('non-root containers healthy; frontend HTML/JS/SPA fallback and same-origin API proxy reachable')
        for service in ('backend', 'frontend'):
            assert compose('exec', '-T', service, 'id', '-u').strip() != '0'
        query = '?space_id=personal-demo'
        item = call('/api/v1/projects' + query, {'title': 'Docker synthetic acceptance', 'brief': '合成素材容器验收，不调用模型。'}, 'project', status=201)
        path = f"/api/v1/projects/{item['id']}/media"
        media = call(path + '/demo' + query, {'expected_version': 1}, 'demo')
        assert all(asset['source'] == 'synthetic' for asset in media['assets'])
        photo = next(asset for asset in media['assets'] if asset['kind'] == 'image')
        upload = path + '/uploads' + query + '&filename=synthetic.png'
        pixels = call(photo['file_url'])
        media = call(upload, pixels, 'upload', mime='image/png')
        assert call(upload, pixels, 'upload', mime='image/png') == media
        call(upload, b'invalid', 'invalid-upload', status=400, mime='image/png')
        # Nginx rejects the oversized Content-Length before reading a body.
        # Avoid writing 34 MiB into its already closed socket (client BrokenPipe).
        connection = http.client.HTTPConnection('127.0.0.1', port, timeout=10)
        try:
            connection.putrequest('POST', upload)
            connection.putheader('Content-Length', str(34 * 1024 * 1024))
            connection.putheader('Content-Type', 'image/png')
            connection.putheader('Idempotency-Key', 'nginx-upload-limit')
            connection.endheaders()
            response = connection.getresponse()
            assert response.status == 413
            response.read()
        finally:
            connection.close()
        body = {'expected_version': media['version'], 'timeline': copy.deepcopy(media['timeline'])}
        body['timeline']['clips'][0].update(subtitle_cues=[{'start': .25, 'end': .75, 'text': '容器逐句字幕'}], subtitle_source='manual')
        saved = call(path + '/timeline' + query, body, 'save', 'PUT')
        call(path + '/timeline' + query, body, 'stale', 'PUT', status=409)
        checks.append('proxy upload/content/size guards, replay and timeline version conflicts')
        export_body = {'expected_version': saved['version']}
        export = call(path + '/exports' + query, export_body, 'export', status=202)
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            done = next(entry for entry in call(path + query)['exports'] if entry['id'] == export['id'])
            if done['status'] != 'running':
                break
            time.sleep(.2)
        assert done['status'] == 'succeeded', done
        video = call(done['file_url'])
        (evidence / 'container-synthetic.mp4').write_bytes(video)
        assert call(done['file_url'], status=206, headers={'Range': 'bytes=0-31'}) == video[:32]
        srt = call(done['subtitle_url'])
        assert '00:00:00,250 --> 00:00:00,750' in srt.decode()
        (evidence / 'container-synthetic.srt').write_bytes(srt)
        (evidence / 'container-synthetic.vtt').write_bytes(call(done['subtitle_vtt_url']))
        # Probe and decode with the runtime container's actual bundled media tools.
        compose('cp', str(evidence / 'container-synthetic.mp4'), 'backend:/tmp/acceptance.mp4')
        probe = json.loads(compose('exec', '-T', 'backend', 'ffprobe', '-v', 'error', '-show_format', '-show_streams', '-of', 'json', '/tmp/acceptance.mp4'))
        (evidence / 'ffprobe.json').write_text(json.dumps(probe, indent=2))
        assert {'video', 'audio', 'subtitle'} <= {s['codec_type'] for s in probe['streams']}
        duration = sum(c['duration'] for c in saved['timeline']['clips'])
        assert abs(float(probe['format']['duration']) - duration) < .25
        compose('exec', '-T', 'backend', 'ffmpeg', '-v', 'error', '-i', '/tmp/acceptance.mp4', '-f', 'null', '-')
        checks.append('container FFmpeg actually encodes and decodes MP4; probe verifies duration/video/audio/subtitles; proxy range and SRT/VTT downloads')
        compose('restart', 'backend')
        ready()
        assert call(path + query)['timeline'] == saved['timeline']
        assert call(path + '/exports' + query, export_body, 'export', status=202)['id'] == export['id']
        assert call(done['file_url']) == video
        checks.append('container restart retains named-volume media, timeline, export bytes and idempotency receipts')
        result = {'checks': checks, 'project': project, 'base': base, 'images': vars(args), 'model_validation': False, 'evidence': str(evidence)}
        (evidence / 'acceptance.json').write_text(json.dumps(result, ensure_ascii=False, indent=2))
        print(json.dumps(result, ensure_ascii=False))
    finally:
        try:
            (evidence / 'container.log').write_text(compose('logs', '--no-color'))
        finally:
            compose('down', '--volumes', '--remove-orphans')


if __name__ == '__main__':
    main()
