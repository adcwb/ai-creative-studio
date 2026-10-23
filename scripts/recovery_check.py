#!/usr/bin/env python3
"""Whole-directory backup/restore through real server and synthetic media APIs."""
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request

import backup
ROOT = Path(__file__).resolve().parents[1]

def main():
    with tempfile.TemporaryDirectory(prefix='studio-recovery-') as directory:
        root = Path(directory)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        origin = f'http://127.0.0.1:{port}'
        process = None
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        def request(path, body=None, key=None):
            headers = {'Content-Type': 'application/json'}
            if key:
                headers['Idempotency-Key'] = key
            data = None if body is None else json.dumps(body).encode()
            with opener.open(urllib.request.Request(origin + path, data=data, headers=headers), timeout=45) as response:
                raw = response.read()
                return json.loads(raw) if 'application/json' in response.headers.get('Content-Type', '') else raw
        def start(data):
            nonlocal process
            env = {'PATH': os.environ['PATH'], 'DATA_DIR': str(data), 'HTTP_ADDR': f'127.0.0.1:{port}', 'AUTH_MODE': 'demo', 'IMAGE_PROVIDER': 'mock-svg', 'TEXT_PROVIDER': 'mock-text'}
            process = subprocess.Popen([str(ROOT / 'backend/bin/server')], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            for _ in range(100):
                if process.poll() is not None:
                    raise RuntimeError('server exited during recovery acceptance')
                try:
                    request('/healthz')
                    return
                except OSError:
                    time.sleep(.05)
            raise RuntimeError('server readiness timeout')
        def stop():
            if process and process.poll() is None:
                process.terminate()
                process.wait(15)
        try:
            data = root / 'data'
            start(data)
            project = request('/api/v1/projects?space_id=personal-demo', {'title': 'Recovery synthetic fixture', 'brief': 'Synthetic recovery test, no AI provider.'}, 'recovery-project-key')
            media_path = f"/api/v1/projects/{project['id']}/media?space_id=personal-demo"
            demo_path = f"/api/v1/projects/{project['id']}/media/demo?space_id=personal-demo"
            media = request(demo_path, {'expected_version': 1}, 'recovery-demo-key')
            hashes = {asset['id']: hashlib.sha256(request(asset['file_url'])).hexdigest() for asset in media['assets']}
            try:
                backup.backup(data, root / 'running.tar.gz')
                raise AssertionError('backup incorrectly accepted running data')
            except ValueError:
                pass
            subprocess.run(['python3', str(ROOT / 'scripts/local_load.py'), '--base-url', origin, '--requests', '100', '--concurrency', '4'], check=True)
            stop()
            archive = root / 'snapshot.tar.gz'
            backup.backup(data, archive)
            backup.verify(archive)
            restored = root / 'restored'
            backup.restore(archive, restored)
            start(restored)
            after = request(media_path)
            assert after == media, 'restored media metadata differs'
            assert request(demo_path, {'expected_version': 1}, 'recovery-demo-key') == media, 'idempotent receipt not restored'
            for asset in after['assets']:
                assert hashlib.sha256(request(asset['file_url'])).hexdigest() == hashes[asset['id']], 'restored media content differs'
            print(json.dumps({'result': 'passed', 'restored_assets': len(hashes), 'checks': ['running backup rejected', 'complete manifest validation', 'fresh server started from restored directory', 'media metadata preserved', 'media content hashes identical', 'idempotency receipt preserved'], 'live_provider_calls': 0}, indent=2))
        finally:
            stop()

if __name__ == '__main__':
    main()
