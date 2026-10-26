#!/usr/bin/env python3
"""Actual API/PG contract rehearsal for managed evaluation; NEVER starts a worker.
Fresh synthetic DB, account, KEK and model metadata only. A deny-only moderator
provides defense in depth. No supplier request, real credential or charge occurs.
"""
import argparse
import base64
from datetime import datetime, timedelta, timezone
import http.cookiejar
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.parse
import urllib.request
import uuid

import live_eval

ROOT = Path(__file__).resolve().parents[1]


def build_environment(base, evidence):
    # CI need not export GOPATH/GOCACHE. Keep private fallbacks without
    # forwarding HOME, provider keys, proxy credentials or user Go settings.
    paths = {'GOPATH': evidence / 'go-path', 'GOCACHE': evidence / 'go-build'}
    build = {**base, 'GOENV': 'off', 'GOTOOLCHAIN': 'local'}
    for key, fallback in paths.items():
        build[key] = os.environ.get(key) or str(fallback)
    if os.environ.get('GOMODCACHE'):
        build['GOMODCACHE'] = os.environ['GOMODCACHE']
    return build


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--pg-bin', type=Path, required=True)
    parser.add_argument('--pg-socket', type=Path, required=True)
    parser.add_argument('--pg-port', type=int, required=True)
    parser.add_argument('--pg-user', default='agent')
    parser.add_argument('--pg-lib', type=Path)
    args = parser.parse_args()
    if not args.pg_socket.is_absolute() or not args.pg_user.isalnum() or not 0 < args.pg_port < 65536:
        parser.error('explicit local test PostgreSQL socket, port and synthetic user required')
    evidence = Path(tempfile.mkdtemp(prefix='studio-managed-eval-'))
    database = 'studio_eval_' + uuid.uuid4().hex[:16]
    env = {'PATH': os.environ['PATH'], 'PGPASSFILE': '/dev/null', 'PGSERVICEFILE': '/dev/null'}
    pg_env = {**env, **({'LD_LIBRARY_PATH': str(args.pg_lib)} if args.pg_lib else {})}
    pg_args = ['-h', str(args.pg_socket), '-p', str(args.pg_port), '-U', args.pg_user]
    subprocess.run([str(args.pg_bin/'createdb'), *pg_args, '-T', 'template0', '-E', 'UTF8', database], env=pg_env, check=True)
    process = log = None
    try:
        build_env = build_environment(env, evidence)
        subprocess.run(['go', 'build', '-o', str(evidence/'server'), './cmd/server'], cwd=ROOT/'backend', env=build_env, check=True)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
        origin = f'http://127.0.0.1:{port}'
        expiry = (datetime.now(timezone.utc)+timedelta(hours=1)).isoformat()
        moderator = evidence/'deny-review'
        moderator.write_text('#!/bin/sh\nexit 1\n'); moderator.chmod(0o700)
        policy = evidence/'policy.json'
        policy.write_text(json.dumps({'version':1, 'moderation_command':str(moderator), 'moderation_contract':'local-or-free-v1',
            'moderation_source':'synthetic deny-only rehearsal', 'moderation_expires_at':expiry,
            'prices':[{'provider':'openai-text','model':'gpt-6.1-sol','capability':'text','currency':'USD',
                       'request_contract':'text-64k-6000-v1','input_minor_per_million':'1','output_minor_per_million':'1',
                       'maximum_input_bytes':32000,'source':'synthetic only; not vendor pricing','expires_at':expiry}]}))
        process_env = {**env, 'STORAGE_BACKEND':'postgres','QUEUE_BACKEND':'postgres','STORAGE_AUTO_MIGRATE':'true',
            'DATABASE_URL':f'host={args.pg_socket} port={args.pg_port} user={args.pg_user} dbname={database} sslmode=disable passfile=/dev/null',
            'BLOB_STORE':'shared-file','BLOB_FILE_ROOT':str(evidence/'objects'),'DATA_DIR':str(evidence/'cache'),
            'AUTH_MODE':'secure','AUTH_ALLOW_REGISTRATION':'true','AUTH_COOKIE_SECURE':'false','FRONTEND_ORIGIN':origin,
            'HTTP_ADDR':f'127.0.0.1:{port}','IMAGE_PROVIDER':'mock-svg','TEXT_PROVIDER':'mock-text',
            'EXECUTION_POLICY_FILE':str(policy),'MODEL_CONFIG_KEK_BASE64':base64.b64encode(bytes([83])*32).decode()}
        log = (evidence/'server.log').open('w')
        process = subprocess.Popen([str(evidence/'server')], env=process_env, stdout=log, stderr=log)
        jar = http.cookiejar.CookieJar()
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(jar), live_eval.NoRedirect())
        csrf = ''
        def call(path, body=None, method=None):
            request = urllib.request.Request(origin+path, data=None if body is None else json.dumps(body).encode(), method=method,
                headers={'Origin':origin, 'Content-Type':'application/json','X-CSRF-Token':csrf})
            with opener.open(request, timeout=10) as response:
                return json.loads(response.read())
        for _ in range(200):
            if process.poll() is not None: raise RuntimeError('synthetic API exited; inspect isolated log')
            try:
                call('/healthz'); break
            except OSError: time.sleep(.05)
        else: raise RuntimeError('API startup timed out')
        registration = call('/api/v1/auth/register', {'email':'managed-eval@example.invalid','password':'Synthetic-password-2026!'})
        csrf = registration['csrf_token']; tenant = registration['spaces'][0]['id']
        query = '?' + urllib.parse.urlencode({'space_id':tenant})
        call('/api/v1/governance/budget'+query, {'currency':'USD','limit_minor':300,'expected_limit_minor':0}, 'PUT')
        config = call('/api/v1/model-config'+query)
        entry = {'provider':'openai-text','model':'gpt-6.1-sol','capability':'text','region':'global','enabled':True,'is_default':False,'lifecycle':'active'}
        config = call('/api/v1/model-config'+query, {'expected_version':config['version'],'entry':entry,
            'credential':{'action':'set','secret':'synthetic-managed-eval-secret'}}, 'PUT')
        config_id = config['entries'][0]['id']
        session = {'cookie':'; '.join(f'{cookie.name}={cookie.value}' for cookie in jar), 'csrf_token':csrf,'space_id':tenant}
        execution = argparse.Namespace(kind='analysis',provider='openai-text',model='gpt-6.1-sol',case_id='case-01',
            model_config_id=config_id,model_config_version=config['version'],currency='USD',budget_micros=3000000,
            reserve_micros=100000,timeout=1,base_url=origin,origin=origin,voice='',scenario='original',
            authorize_provider_model_budget=True,authorize_session_transmission=True,ledger=evidence/'ledger.json')
        report = {'run_id':'eval-'+uuid.uuid4().hex,'mode':'synthetic_api_acceptance_fixture','cost_state':'not_called'}
        assert live_eval.run(execution, session, report) == 1  # queued, not falsely reported as generation success
        assert report['status'] == 'queued' and report['cost_state'] == 'unknown_reserved'
        budget = call('/api/v1/governance'+query+'&currency=USD')
        assert budget['exposure_minor'] == 10 and budget['available_minor'] == 290
        assert len(budget['entries']) == 1 and budget['entries'][0]['state'] == 'reserved'
        result = subprocess.run([str(args.pg_bin/'psql'),*pg_args,'-d',database,'-At','-c',
            'SELECT state||\':\'||attempt FROM execution_jobs'], env=pg_env, capture_output=True, text=True, check=True).stdout.strip()
        assert result == 'queued:0', result
        revoked = {**entry, 'id':config_id, 'enabled':False,'lifecycle':'disabled'}
        call('/api/v1/model-config'+query, {'expected_version':config['version'],'entry':revoked,'credential':{'action':'revoke'}}, 'PUT')
        try:
            live_eval.run(execution, session, {'run_id':'eval-revoked','cost_state':'not_called'})
        except ValueError:
            pass
        else:
            raise AssertionError('revoked config was accepted')
        assert len(json.loads(execution.ledger.read_text())['reservations']) == 1
        summary = {'status':'passed','mode':'synthetic_api_acceptance_fixture','postgres':'real','api_processes':1,
            'worker_started':False,'supplier_calls':0,'real_credentials':False,'charges':0,
            'checks':['real session/catalog/budget/quote/creative API contract','atomic server reservation and queued job',
                      'unknown local reservation retained','configuration revocation rejected before re-reservation'],
            'evidence':str(evidence)}
        (evidence/'result.json').write_text(json.dumps(summary,indent=2)+'\n')
        print(json.dumps(summary))
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            try: process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill(); process.wait()
        if log is not None: log.close()
        subprocess.run([str(args.pg_bin/'dropdb'),*pg_args,database],env=pg_env,check=True)
        print('Evidence: '+str(evidence))


if __name__ == '__main__':
    main()
