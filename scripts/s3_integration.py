#!/usr/bin/env python3
"""Run the S3 bridge against an isolated disposable local MinIO container."""
import argparse
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', required=True, help='locally built/reviewed MinIO image; no registry login is performed')
    args = parser.parse_args()
    name = 'studio-s3-test-' + secrets.token_hex(6)
    access, secret = 'test' + secrets.token_hex(10), secrets.token_hex(32)
    with tempfile.TemporaryDirectory(prefix='studio-s3-') as directory:
        env_file = Path(directory) / 'fixture.env'
        env_file.write_text(f'MINIO_ROOT_USER={access}\nMINIO_ROOT_PASSWORD={secret}\n')
        env_file.chmod(0o600)
        try:
            run('docker', 'run', '-d', '--name', name, '--env-file', str(env_file), '-p', '127.0.0.1::9000', args.image, 'server', '/data', stdout=subprocess.DEVNULL)
            binding = run('docker', 'port', name, '9000/tcp', capture_output=True).stdout.strip()
            endpoint = 'http://' + binding
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            for attempt in range(60):
                try:
                    with opener.open(endpoint + '/minio/health/live', timeout=1) as response:
                        if response.status == 200:
                            break
                except OSError:
                    time.sleep(.5)
            else:
                raise RuntimeError('local MinIO did not become ready')
            # Only allow toolchain environment; do not inspect/inherit provider credentials.
            env = {key: os.environ[key] for key in ['PATH', 'HOME', 'GOPATH', 'GOCACHE', 'GOTOOLCHAIN'] if key in os.environ}
            env.update(STUDIO_TEST_S3_ENDPOINT=endpoint, STUDIO_TEST_S3_ACCESS=access, STUDIO_TEST_S3_SECRET=secret)
            run('go', 'test', '-race', './internal/studio', '-run', '^TestS3LocalContainerIntegration$', '-count=1', '-v', cwd=ROOT / 'backend', env=env)
            print(json.dumps({'fixture': 'local disposable MinIO', 'result': 'passed', 'checks': ['signed upload', 'signed read', 'PNG roundtrip', 'expired URL rejected', 'object substitution rejected', 'anonymous read denied'], 'live_provider_calls': 0}))
        finally:
            subprocess.run(['docker', 'rm', '-f', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)

if __name__ == '__main__':
    main()
