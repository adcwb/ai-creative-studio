#!/usr/bin/env python3
"""Run the complete secure desktop/mobile suite with real PostgreSQL and a worker.
Creates and drops only a fresh synthetic database. No existing secret values,
model providers, external storage or paid services are used.
"""
import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]

def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--pg-bin', type=Path, required=True)
    p.add_argument('--pg-socket', type=Path, required=True)
    p.add_argument('--pg-port', type=int, required=True)
    p.add_argument('--pg-user', default='agent')
    p.add_argument('--pg-lib', type=Path)
    p.add_argument('--chromium', default='/usr/bin/chromium')
    args = p.parse_args()
    if not args.pg_socket.is_absolute() or not args.pg_user.isalnum():
        p.error('use an absolute local test socket and an alphanumeric synthetic user')
    for port in (8082, 5175):
        with socket.socket() as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            sock.bind(('127.0.0.1', port))
    evidence = Path(tempfile.mkdtemp(prefix='studio-postgres-browser-'))
    dbname = 'studio_browser_' + uuid.uuid4().hex[:16]
    env = {'PATH': os.environ['PATH']}
    pg_env = {**env, **({'LD_LIBRARY_PATH': str(args.pg_lib)} if args.pg_lib else {})}
    pg_args = ['-h', str(args.pg_socket), '-p', str(args.pg_port), '-U', args.pg_user]
    subprocess.run([str(args.pg_bin / 'createdb'), *pg_args, '-T', 'template0', '-E', 'UTF8', dbname], env=pg_env, check=True)
    processes, logs = [], []
    try:
        for name in ('server', 'worker'):
            subprocess.run(['go', 'build', '-o', str(evidence / name), './cmd/' + name], cwd=ROOT / 'backend', check=True)
        common = {**env, 'STORAGE_BACKEND': 'postgres', 'QUEUE_BACKEND': 'postgres',
                  'DATABASE_URL': f'host={args.pg_socket} port={args.pg_port} user={args.pg_user} dbname={dbname} sslmode=disable',
                  'BLOB_STORE': 'shared-file', 'BLOB_FILE_ROOT': str(evidence / 'objects'),
                  'AUTH_MODE': 'secure', 'AUTH_ALLOW_REGISTRATION': 'true', 'AUTH_COOKIE_SECURE': 'false',
                  'FRONTEND_ORIGIN': 'http://localhost:5175', 'IMAGE_PROVIDER': 'mock-svg', 'TEXT_PROVIDER': 'mock-text'}
        def start(name, extra):
            log = (evidence / (name + '.log')).open('w')
            logs.append(log)
            process = subprocess.Popen([str(evidence / name)], env={**common, 'DATA_DIR': str(evidence / (name + '-cache')), **extra}, stdout=log, stderr=log)
            processes.append(process)
            return process
        server = start('server', {'HTTP_ADDR': '127.0.0.1:8082', 'STORAGE_AUTO_MIGRATE': 'true'})
        client = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        for _ in range(200):
            if server.poll() is not None:
                raise RuntimeError('synthetic API exited; inspect evidence log')
            try:
                with client.open('http://127.0.0.1:8082/healthz', timeout=1) as response:
                    if response.status == 200:
                        break
            except OSError:
                time.sleep(.05)
        else:
            raise RuntimeError('synthetic API did not become healthy')
        worker = start('worker', {})
        browser_env = {**env, 'PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH': args.chromium, 'npm_config_cache': str(evidence / 'npm-cache')}
        subprocess.run(['npm', 'exec', 'playwright', 'test', '--', '--config', 'playwright.postgres.config.ts'], cwd=ROOT / 'frontend', env=browser_env, check=True)
        if worker.poll() is not None:
            raise RuntimeError('independent worker exited during browser test')
        result = {'database': 'fresh synthetic PostgreSQL', 'worker': 'independent process', 'object_store': 'shared-file', 'secure_desktop_mobile': 'passed', 'live_calls': 0}
        (evidence / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
        print(json.dumps({'evidence': str(evidence), **result}))
    finally:
        for proc in reversed(processes):
            if proc.poll() is None:
                proc.terminate()
        for proc in reversed(processes):
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
        for log in logs:
            log.close()
        subprocess.run([str(args.pg_bin / 'dropdb'), *pg_args, dbname], env=pg_env, check=True)
        print('Evidence: ' + str(evidence))

if __name__ == '__main__':
    main()
