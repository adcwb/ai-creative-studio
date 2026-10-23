#!/usr/bin/env python3
"""Two real API processes and two workers against disposable PostgreSQL.

Uses a local Unix-socket PostgreSQL test installation and shared-file BlobStore.
No existing credentials are loaded; no real model or S3 service is called.
Creates and drops only a randomly named database owned by this invocation.
"""
import argparse
import concurrent.futures
import http.cookiejar
import json
import os
from pathlib import Path
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
    parser.add_argument('--pg-bin', type=Path, required=True)
    parser.add_argument('--pg-socket', type=Path, required=True)
    parser.add_argument('--pg-port', type=int, required=True)
    parser.add_argument('--pg-user', default='agent')
    parser.add_argument('--pg-lib', type=Path)
    args = parser.parse_args()
    assert args.pg_socket.is_absolute() and args.pg_socket.is_dir()
    assert args.pg_user.isalnum() and 0 < args.pg_port < 65536
    dbname = 'studio_integration_' + uuid.uuid4().hex[:16]
    evidence = Path(tempfile.mkdtemp(prefix='studio-multiservice-'))
    env = {'PATH': os.environ['PATH'], 'PGPASSFILE': '/dev/null', 'PGSERVICEFILE': '/dev/null'}
    pg_env = dict(env)
    if args.pg_lib:
        pg_env['LD_LIBRARY_PATH'] = str(args.pg_lib)
    pg_args = ['-h', str(args.pg_socket), '-p', str(args.pg_port), '-U', args.pg_user]
    subprocess.run([str(args.pg_bin / 'createdb'), *pg_args, '-T', 'template0', '-E', 'UTF8', dbname], env=pg_env, check=True)
    print(json.dumps({'evidence': str(evidence), 'postgres': 'disposable local database'}), flush=True)
    processes, logs = [], []
    workers = []
    checks = []
    try:
        for name in ('server', 'worker'):
            subprocess.run(['go', 'build', '-o', str(evidence / name), './cmd/' + name], cwd=ROOT / 'backend', check=True)
        ports = []
        for _ in range(2):
            with socket.socket() as sock:
                sock.bind(('127.0.0.1', 0))
                ports.append(sock.getsockname()[1])
        origin = f'http://127.0.0.1:{ports[0]}'
        common = {**env, 'STORAGE_BACKEND': 'postgres', 'QUEUE_BACKEND': 'postgres',
                  'STORAGE_AUTO_MIGRATE': 'true', 'DATABASE_URL': f'host={args.pg_socket} port={args.pg_port} user={args.pg_user} dbname={dbname} sslmode=disable passfile=/dev/null',
                  'BLOB_STORE': 'shared-file', 'BLOB_FILE_ROOT': str(evidence / 'objects'),
                  'AUTH_MODE': 'secure', 'AUTH_ALLOW_REGISTRATION': 'true', 'AUTH_COOKIE_SECURE': 'false',
                  'FRONTEND_ORIGIN': origin, 'IMAGE_PROVIDER': 'mock-svg', 'TEXT_PROVIDER': 'mock-text'}
        def start(name, index):
            log = (evidence / f'{name}-{index}.log').open('w')
            logs.append(log)
            config = {**common, 'DATA_DIR': str(evidence / f'{name}-{index}-cache')}
            if name == 'server':
                config['HTTP_ADDR'] = f'127.0.0.1:{ports[index]}'
            proc = subprocess.Popen([str(evidence / name)], env=config, stdout=log, stderr=log)
            processes.append(proc)
            return proc
        def client():
            return urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        alice, bob, anon = client(), client(), client()
        def call(opener, index, method, path, body=None, csrf='', key='', status=200):
            request = urllib.request.Request(f'http://127.0.0.1:{ports[index]}' + path, method=method,
                headers={'Content-Type': 'application/json', 'Origin': origin, 'X-CSRF-Token': csrf, 'Idempotency-Key': key},
                data=None if body is None else json.dumps(body).encode())
            try:
                response = opener.open(request, timeout=20)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read()
                assert response.status == status, (method, path, response.status, status, raw[:200])
                return json.loads(raw) if 'application/json' in response.headers.get('Content-Type', '') else raw
        for index in range(2):
            proc = start('server', index)
            for _ in range(200):
                try:
                    call(anon, index, 'GET', '/healthz')
                    break
                except OSError:
                    assert proc.poll() is None, 'API exited; see isolated evidence log'
                    time.sleep(.05)
            else:
                raise AssertionError('API startup timed out')
        a = call(alice, 0, 'POST', '/api/v1/auth/register', {'email': 'alice@example.invalid', 'password': 'Synthetic-password-2026!'})
        b = call(bob, 1, 'POST', '/api/v1/auth/register', {'email': 'bob@example.invalid', 'password': 'Synthetic-password-2026!'})
        assert call(alice, 1, 'GET', '/api/v1/auth/session')['user']['id'] == a['user']['id']
        tenant = a['spaces'][0]['id']
        query = '?space_id=' + tenant
        csrf = a['csrf_token']
        call(bob, 1, 'GET', '/api/v1/tasks' + query, status=403)
        body = {'prompt': 'Synthetic multi-process image', 'kind': 'poster', 'ratio': '1:1', 'style': 'natural'}
        # No worker exists yet: API must persist but never execute queued work.
        task = call(alice, 0, 'POST', '/api/v1/tasks' + query, body, csrf, 'multi-image-task', 202)
        time.sleep(.25)
        assert call(alice, 1, 'GET', '/api/v1/tasks/' + task['id'] + query)['status'] == 'queued'
        checks.append('API does not execute queued work')
        for index in range(2):
            workers.append(start('worker', index))
        for _ in range(300):
            current = call(alice, 1, 'GET', '/api/v1/tasks/' + task['id'] + query)
            if current['status'] in ('succeeded', 'failed'):
                break
            time.sleep(.05)
        assert current['status'] == 'succeeded', current
        assert call(alice, 0, 'GET', current['result_url']) == call(alice, 1, 'GET', current['result_url'])
        # Repeated acceptance through either API must retain the same task.
        def replay(index):
            return call(alice, index % 2, 'POST', '/api/v1/tasks' + query, body, csrf, 'multi-image-task', 200)
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
            repeats = list(pool.map(replay, range(12)))
        assert all(item['id'] == task['id'] for item in repeats)
        checks.append('two independent workers, shared result and cross-API idempotency')
        # Bounded creative workload, not a health-only benchmark. Two tenants,
        # eight concurrent acceptances per batch, four independent worker slots.
        # Synthetic SVG inference does not measure real model/provider capacity.
        accepted_latencies, completion_latencies = [], []
        load_started = time.monotonic()
        for batch in range(2):
            def submit_load(index):
                opener, session = (alice, a) if index % 2 == 0 else (bob, b)
                scope = '?space_id=' + session['spaces'][0]['id']
                started = time.monotonic()
                item = call(opener, index % 2, 'POST', '/api/v1/tasks' + scope,
                            {**body, 'prompt': f'Synthetic load {batch}-{index}'},
                            session['csrf_token'], f'bounded-load-{batch}-{index}', 202)
                return opener, scope, item, started, time.monotonic() - started
            with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
                pending = list(pool.map(submit_load, range(8)))
            deadline = time.monotonic() + 60
            while pending:
                assert time.monotonic() < deadline, 'bounded creative workload timed out'
                remaining = []
                for opener, scope, item, started, accepted in pending:
                    value = call(opener, 1, 'GET', '/api/v1/tasks/' + item['id'] + scope)
                    assert value['status'] != 'failed', value
                    if value['status'] == 'succeeded':
                        assert call(opener, 0, 'GET', value['result_url']) == call(opener, 1, 'GET', value['result_url'])
                        accepted_latencies.append(accepted)
                        completion_latencies.append(time.monotonic() - started)
                    else:
                        remaining.append((opener, scope, item, started, accepted))
                pending = remaining
                if pending:
                    time.sleep(.05)
        def percentile_ms(values, percentile):
            return round(sorted(values)[min(len(values) - 1, int((len(values) - 1) * percentile))] * 1000, 2)
        load_report = {'kind': 'synthetic SVG creation through real PostgreSQL and independent workers',
                       'tasks': len(completion_latencies), 'tenants': 2, 'concurrent_clients': 8,
                       'failures': 0, 'wall_seconds': round(time.monotonic() - load_started, 3),
                       'accept_p50_ms': percentile_ms(accepted_latencies, .5),
                       'accept_p95_ms': percentile_ms(accepted_latencies, .95),
                       'completion_p50_ms': percentile_ms(completion_latencies, .5),
                       'completion_p95_ms': percentile_ms(completion_latencies, .95),
                       'production_capacity_claim': False}
        (evidence / 'creative-load.json').write_text(json.dumps(load_report, indent=2) + '\n')
        checks.append('16 synthetic creative tasks under eight concurrent clients across two tenants')
        project = call(alice, 0, 'POST', '/api/v1/projects' + query,
                       {'title': 'Synthetic distributed film', 'brief': 'A courier delivers a synthetic letter to a clockmaker.'}, csrf, 'multi-project-create', 201)
        project_path = '/api/v1/projects/' + project['id']
        def wait_project(field):
            for _ in range(400):
                value = call(alice, 1, 'GET', project_path + query)
                if value.get(field, {}).get('status') in ('succeeded', 'failed'):
                    assert value[field]['status'] == 'succeeded', value[field]
                    return value
                time.sleep(.05)
            raise AssertionError('project worker timed out')
        call(alice, 0, 'POST', project_path + '/generations' + query,
             {'expected_version': project['version']}, csrf, 'multi-project-script', 202)
        project = wait_project('latest_generation')
        call(alice, 1, 'POST', project_path + '/storyboard-generations' + query,
             {'expected_version': project['version']}, csrf, 'multi-project-board', 202)
        project = wait_project('latest_storyboard_generation')
        checks.append('independent script and storyboard workers commit project revisions')
        creative = call(alice, 0, 'POST', project_path + '/analysis' + query,
                        {'expected_version': project['version']}, csrf, 'multi-analysis', 202)
        for _ in range(300):
            jobs = call(alice, 1, 'GET', project_path + '/creative-jobs' + query)
            analyzed = next(item for item in jobs if item['id'] == creative['id'])
            if analyzed['status'] in ('succeeded', 'failed'):
                break
            time.sleep(.05)
        assert analyzed['status'] == 'succeeded' and analyzed.get('analysis'), analyzed
        checks.append('independent creative analysis worker')

        media_path = project_path + '/media'
        media = call(alice, 0, 'POST', media_path + '/demo' + query,
                     {'expected_version': 1}, csrf, 'multi-media-demo')

        alignment = call(alice, 0, 'POST', media_path + '/alignments' + query,
                         {'expected_version': media['version'], 'clip_id': media['timeline']['clips'][0]['id'],
                          'mode': 'estimate', 'text': 'Synthetic subtitle alignment.'}, csrf, 'multi-alignment', 202)
        for _ in range(300):
            items = call(alice, 1, 'GET', media_path + '/alignments' + query)
            aligned = next(item for item in items if item['id'] == alignment['id'])
            if aligned['status'] in ('succeeded', 'failed'):
                break
            time.sleep(.05)
        assert aligned['status'] == 'succeeded' and aligned['cues'], aligned
        checks.append('independent subtitle estimate worker; no ASR quality claim')
        exported = call(alice, 1, 'POST', media_path + '/exports' + query,
                        {'expected_version': media['version']}, csrf, 'multi-media-export', 202)
        for _ in range(600):
            media = call(alice, 0, 'GET', media_path + query)
            result = next(item for item in media['exports'] if item['id'] == exported['id'])
            if result['status'] in ('succeeded', 'failed', 'cancelled'):
                break
            time.sleep(.1)
        assert result['status'] == 'succeeded', result
        video = call(alice, 0, 'GET', result['file_url'])
        assert video == call(alice, 1, 'GET', result['file_url'])
        output = evidence / 'distributed-export.mp4'
        output.write_bytes(video)
        subprocess.run(['ffmpeg', '-v', 'error', '-i', str(output), '-f', 'null', '-'], env=env, check=True, timeout=30)
        checks.append('independent FFmpeg export worker and shared objects decode across both APIs')

        # Fault injection is restricted to the disposable database created above.
        # Kill actual workers after the durable dispatch boundary, then expire
        # that lease to verify visible uncertainty without a 30-second wait.
        crash_task = call(alice, 0, 'POST', '/api/v1/tasks' + query,
                          {**body, 'prompt': 'Synthetic process crash task'}, csrf, 'multi-crash-task', 202)
        assert crash_task['id'].isalnum()
        def sql(statement):
            return subprocess.run([str(args.pg_bin / 'psql'), *pg_args, '-d', dbname, '-At', '-c', statement],
                                  env=pg_env, check=True, text=True, capture_output=True).stdout.strip()
        for _ in range(100):
            state = sql("SELECT state FROM execution_jobs WHERE id='" + crash_task['id'] + "'")
            if state == 'dispatch_started':
                break
            time.sleep(.01)
        assert state == 'dispatch_started', state
        for proc in workers:
            proc.kill()
            proc.wait(timeout=5)
        sql("UPDATE execution_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id='" + crash_task['id'] + "'")
        workers = [start('worker', index) for index in (2, 3)]
        for _ in range(100):
            failed = call(alice, 1, 'GET', '/api/v1/tasks/' + crash_task['id'] + query)
            if failed['status'] == 'failed':
                break
            time.sleep(.05)
        assert failed['status'] == 'failed' and failed['error_code'] == 'execution_outcome_unknown', failed
        assert sql("SELECT state||':'||attempt FROM execution_jobs WHERE id='" + crash_task['id'] + "'") == 'unknown:1'
        checks.append('SIGKILL after dispatch: API reports unknown and no worker resubmits')
        members = f'/api/v1/organizations/{tenant}/members'
        call(alice, 0, 'PUT', members, {'email': b['user']['email'], 'role': 'viewer'}, csrf)
        call(bob, 1, 'GET', '/api/v1/tasks' + query)
        call(alice, 0, 'PUT', members, {'email': b['user']['email'], 'role': 'remove'}, csrf)
        call(bob, 1, 'GET', '/api/v1/tasks' + query, status=403)
        checks.append('cross-instance membership revocation and tenant isolation')
        call(alice, 0, 'POST', '/api/v1/auth/logout', {}, csrf)
        assert call(alice, 1, 'GET', '/api/v1/auth/session')['authenticated'] is False
        checks.append('cross-instance session revocation')
        print(json.dumps({'result': 'passed', 'checks': checks, 'api_processes': 2, 'worker_processes': 2,
                          'postgres': 'real', 'blob_store': 'shared-file', 'real_s3_verified': False,
                          'live_model_calls': 0, 'creative_load': load_report, 'evidence': str(evidence)}))
    finally:
        for proc in reversed(processes):
            proc.terminate()
        for proc in reversed(processes):
            try:
                proc.wait(timeout=15)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
        for log in logs:
            log.close()
        subprocess.run([str(args.pg_bin / 'dropdb'), *pg_args, dbname], env=pg_env, check=True)


if __name__ == '__main__':
    main()
