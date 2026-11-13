#!/usr/bin/env python3
"""Native process/SCM acceptance using synthetic fixtures; no live AI calls."""
import argparse
import base64
import copy
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def disposable_dsn(value):
    parsed = urllib.parse.urlparse(value)
    if parsed.scheme not in ('postgres', 'postgresql'):
        raise ValueError('Fixture requires a loopback PostgreSQL URI')
    if parsed.hostname not in ('127.0.0.1', 'localhost', '::1') or not parsed.path.startswith(('/studio_windows_', '/studio_native_')):
        raise ValueError('Fixture requires dedicated studio_windows_/studio_native_ database')
    if any(k in urllib.parse.parse_qs(parsed.query) for k in ('host', 'hostaddr', 'service', 'dbname')):
        raise ValueError('Fixture refuses connection target overrides')
    return value


def fixture_environment(source):
    # Windows os.environ normalizes keys to uppercase. Match without case,
    # retaining SystemRoot for Winsock/side-by-side DLL loading, never secrets.
    allowed = {'PATH', 'SYSTEMROOT', 'WINDIR', 'TEMP', 'TMP', 'LANG', 'CI'}
    return {key.upper(): value for key, value in source.items() if key.upper() in allowed}


def run(args):
    if args.storage == 'postgres':
        disposable_dsn(args.dsn)
    if args.scm and (os.name != 'nt' or os.environ.get('CI') != 'true'):
        raise ValueError('SCM fixture runs only in disposable Windows CI')
    suffix = '.exe' if os.name == 'nt' else ''
    with tempfile.TemporaryDirectory(prefix="studio 原生's ") as directory:
        root = Path(directory)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        base = f'http://127.0.0.1:{port}'
        env = fixture_environment(os.environ)
        if os.name == 'nt' and not env.get('SYSTEMROOT'):
            raise ValueError('Native Windows fixture requires SystemRoot')
        env.update(AUTH_MODE='demo', IMAGE_PROVIDER='mock-svg', TEXT_PROVIDER='mock-text',
                   HTTP_ADDR=f'127.0.0.1:{port}', DATA_DIR=str(root / 'api'),
                   FRONTEND_ORIGIN=base, CONTENT_MODERATION_MODE='disabled')
        if args.storage == 'postgres':
            env.update(STORAGE_BACKEND='postgres', QUEUE_BACKEND='postgres', DATABASE_URL=args.dsn,
                       STORAGE_AUTO_MIGRATE='true', BLOB_STORE='shared-file', BLOB_FILE_ROOT=str(root / 'blobs'),
                       MODEL_CONFIG_KEK_BASE64=base64.b64encode(bytes(range(32))).decode())
        logs = []
        processes = []
        active = []
        services = []

        def request(path, body=None, key=None, status=200, method=None):
            headers = {'Content-Type': 'application/json'}
            if key:
                headers['Idempotency-Key'] = key
            data = json.dumps(body).encode() if body is not None else None
            req = urllib.request.Request(base + path, data=data, headers=headers, method=method)
            try:
                response = urllib.request.urlopen(req, timeout=15)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read()
                assert response.status == status, (path, response.status, status)
                assert response.headers.get('X-Request-ID'), 'missing request correlation'
                return json.loads(raw) if 'application/json' in response.headers.get('Content-Type', '') else raw

        def wait(fn, predicate, seconds=60):
            deadline = time.monotonic() + seconds
            while time.monotonic() < deadline:
                if any(process.poll() is not None for process in active):
                    raise AssertionError('Native fixture process exited before readiness')
                try:
                    result = fn()
                    if predicate(result):
                        return result
                except OSError:
                    pass
                time.sleep(.1)
            raise AssertionError('Native fixture did not reach expected state')

        def service(action, name, executable='', values=None):
            config = root / f'{name}.json'
            if values is not None:
                config.write_text(json.dumps(values), encoding='utf-8')
            command = ['powershell.exe', '-NoProfile', '-NonInteractive', '-File', str(ROOT / 'scripts/windows_service_fixture.ps1'),
                       '-Action', action, '-Name', name]
            if executable:
                command += ['-Executable', executable, '-EnvironmentFile', str(config)]
            subprocess.run(command, check=True, timeout=60, stdout=subprocess.DEVNULL)

        def start(component, values):
            executable = str(ROOT / 'backend/bin' / (component + suffix))
            if args.scm:
                name = 'CreativeStudioAPI' if component == 'server' else 'CreativeStudioWorker'
                services.append(name)
                service('start', name, executable, values)
                return name
            log = (root / (component + '.jsonl')).open('ab')
            logs.append(log)
            process = subprocess.Popen([executable], env=values, stdout=log, stderr=subprocess.STDOUT)
            processes.append(process)
            active.append(process)
            return process

        def stop(handle):
            if isinstance(handle, str):
                service('remove', handle)
                services.remove(handle)
            elif handle.poll() is None:
                handle.kill()
                handle.wait(timeout=15)
            if not isinstance(handle, str) and handle in active:
                active.remove(handle)

        query = '?space_id=personal-demo'
        try:
            api = start('server', env)
            wait(lambda: request('/healthz'), lambda v: v['status'] == 'ok')
            if args.storage == 'postgres':
                worker_env = dict(env, DATA_DIR=str(root / 'worker'), STORAGE_AUTO_MIGRATE='false')
                start('worker', worker_env)
            body = {'prompt': '合成原生验收海报，无付费请求', 'kind': 'poster', 'ratio': '9:16'}
            task = request('/api/v1/tasks' + query, body, 'native-task', 202)
            task_path = '/api/v1/tasks/' + task['id']
            wait(lambda: request(task_path + query), lambda v: v['status'] == 'succeeded')
            stop(api)
            api = start('server', env)
            wait(lambda: request('/healthz'), lambda v: v['status'] == 'ok')
            assert request('/api/v1/tasks' + query, body, 'native-task')['id'] == task['id']
            request(task_path + '?space_id=team-demo', status=404)
            project = request('/api/v1/projects' + query, {'title': '原生合成验收', 'brief': '纯合成素材，验证本地编码，不代表模型效果。'}, 'native-project', 201)
            path = '/api/v1/projects/' + project['id'] + '/media'
            media = request(path + '/demo' + query, {'expected_version': 1}, 'native-media')
            timeline = copy.deepcopy(media['timeline'])
            timeline['clips'] = timeline['clips'][:1]
            timeline['clips'][0]['duration'] = 1.5
            timeline['clips'][0]['subtitle'] = '原生字幕 Native'
            timeline['subtitle_mode'] = 'soft'
            saved = request(path + '/timeline' + query, {'expected_version': media['version'], 'timeline': timeline}, 'native-timeline', method='PUT')
            exported = request(path + '/exports' + query, {'expected_version': saved['version']}, 'native-export', 202)
            def current_export():
                return next(v for v in request(path + query)['exports'] if v['id'] == exported['id'])
            result = wait(current_export, lambda v: v['status'] in ('succeeded', 'failed', 'cancelled'), 120)
            assert result['status'] == 'succeeded', result.get('error_code')
            output = root / '成片.mp4'
            output.write_bytes(request(result['file_url']))
            probe = json.loads(subprocess.check_output(['ffprobe', '-v', 'error', '-show_streams', '-show_format', '-of', 'json', str(output)], timeout=30))
            assert {'video', 'audio', 'subtitle'} <= {v['codec_type'] for v in probe['streams']}
            assert abs(float(probe['format']['duration']) - 1.5) < .15
            subprocess.run(['ffmpeg', '-v', 'error', '-i', str(output), '-f', 'null', '-'], check=True, timeout=30)
            print(json.dumps({'storage': args.storage, 'scm': args.scm, 'restart': True, 'idempotency': True,
                              'tenant_isolation': True, 'encoded_and_decoded': True, 'live_provider_calls': 0}))
        except Exception:
            # Emit only known diagnostic fields; never raw upstream data, env,
            # credentials, prompts, stack values or arbitrary stderr.
            for log_path in root.rglob('*.jsonl'):
                try:
                    for line in log_path.read_text(encoding='utf-8', errors='replace').splitlines()[-30:]:
                        try:
                            record = json.loads(line)
                        except (ValueError, TypeError):
                            continue
                        if isinstance(record, dict):
                            safe = {k: record[k] for k in ('event', 'component', 'service', 'error_code', 'status', 'request_id', 'task_id') if k in record}
                            if safe:
                                print(json.dumps(safe, ensure_ascii=True))
                except OSError:
                    pass
            raise
        finally:
            for name in list(reversed(services)):
                service('remove', name)
            for process in reversed(processes):
                stop(process)
            for log in logs:
                log.close()


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--storage', choices=('file', 'postgres'), default='file')
    parser.add_argument('--dsn', default='')
    parser.add_argument('--scm', action='store_true')
    run(parser.parse_args())
