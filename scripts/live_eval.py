#!/usr/bin/env python3
"""One explicitly authorized creative request against a loopback Studio server.
Default dry-run never reads credentials, existing configuration or makes requests.
"""
import argparse
import fcntl
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

PROVIDERS = {
    'analysis': ['openai-text', 'anthropic-text', 'gemini-text', 'ark-text', 'minimax-text'],
    'image': ['openai-images', 'gemini-images', 'gemini-interactions-images', 'ark-images', 'minimax-images', 'jimeng-images'],
    'speech': ['openai-speech', 'gemini-speech', 'bytedance-speech', 'minimax-speech'],
    'video': ['openai-video', 'gemini-video', 'ark-video', 'minimax-video'],
}
FIXTURE = {'title': 'Synthetic clockwork garden', 'brief': '合成测试：纸片机器人在虚构花园寻找一枚蓝色齿轮。没有真人、品牌或个人数据。'}
PROMPT = 'Synthetic test only: a paper robot finds a blue gear in an imaginary garden. No real people or brands.'

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def validate(args):
    if args.provider not in PROVIDERS[args.kind]:
        raise ValueError('provider does not implement the requested modality in this harness')
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}', args.model):
        raise ValueError('explicit model required')
    if args.budget_micros <= 0 or args.reserve_micros <= 0 or args.reserve_micros > args.budget_micros:
        raise ValueError('positive reserve must fit budget (integer millionths of selected currency)')
    if not re.fullmatch(r'[A-Z]{3}', args.currency):
        raise ValueError('currency must be an explicit three-letter code')
    if not 1 <= args.timeout <= 600:
        raise ValueError('timeout must be 1..600 seconds')
    url = urllib.parse.urlsplit(args.base_url)
    try:
        loopback = ipaddress.ip_address(url.hostname or '').is_loopback
    except ValueError:
        loopback = False
    if url.scheme != 'http' or not loopback or url.username or url.password or url.path or url.query or url.fragment:
        raise ValueError('base URL must be a literal loopback HTTP origin without credentials/path')


def reserve(path, args, run_id):
    # Lock held across read/replace; existing unknown charges remain fully reserved.
    lock = os.open(str(path) + '.lock', os.O_CREAT | os.O_RDWR, 0o600)
    with os.fdopen(lock, 'w') as stream:
        fcntl.flock(stream, fcntl.LOCK_EX)
        ledger = json.loads(path.read_text()) if path.exists() else {'currency': args.currency, 'reservations': []}
        if ledger['currency'] != args.currency:
            raise ValueError('ledger currency mismatch')
        used = sum(row['reserved_micros'] for row in ledger['reservations'])
        if used + args.reserve_micros > args.budget_micros:
            raise ValueError('cumulative reservations exceed budget; reconcile vendor invoices before another run')
        ledger['reservations'].append({'run_id': run_id, 'reserved_micros': args.reserve_micros, 'state': 'unknown', 'provider': args.provider, 'model': args.model})
        temporary = path.with_name(path.name + '.' + run_id + '.tmp')
        fd = os.open(temporary, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, 'w') as output:
            json.dump(ledger, output, indent=2)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        directory = os.open(path.parent, os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)


def run(args, auth, report):
    for field in ('cookie', 'csrf_token', 'space_id'):
        if not isinstance(auth.get(field), str) or not auth[field] or any(c in auth[field] for c in '\r\n'):
            raise ValueError('stdin must contain cookie, csrf_token and space_id strings')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    def call(path, body=None, key=None):
        headers = {'Cookie': auth['cookie'], 'X-CSRF-Token': auth['csrf_token'], 'Origin': args.origin, 'Content-Type': 'application/json'}
        if key:
            headers['Idempotency-Key'] = key
        request = urllib.request.Request(args.base_url + path, data=None if body is None else json.dumps(body).encode(), headers=headers)
        with opener.open(request, timeout=min(args.timeout, 30)) as response:
            raw = response.read(1024 * 1024 + 1)
            if len(raw) > 1024 * 1024:
                raise ValueError('response too large')
            return json.loads(raw)
    session = call('/api/v1/auth/session')
    if session.get('mode') != 'secure' or not session.get('authenticated'):
        raise ValueError('live evaluation requires authenticated secure mode')
    if auth['space_id'] not in [space['id'] for space in session.get('spaces', [])]:
        raise ValueError('session is not a member of requested organization')
    reserve(args.ledger, args, report['run_id'])
    report['cost_state'] = 'unknown_reserved'
    query = '?' + urllib.parse.urlencode({'space_id': auth['space_id']})
    project = call('/api/v1/projects' + query, FIXTURE, report['run_id'] + '-project')
    report['project_id'] = project['id']
    path = '/api/v1/projects/' + project['id'] + '/creative-jobs'
    job = call(path + query, {'kind': args.kind, 'expected_version': project['version'], 'expected_provider': args.provider, 'expected_model': args.model, 'prompt': PROMPT}, report['run_id'] + '-generation')
    report['job_id'] = job['id']
    deadline = time.monotonic() + args.timeout
    while job['status'] in ('queued', 'running') and time.monotonic() < deadline:
        time.sleep(1)
        jobs = call(path + query)
        job = next(row for row in jobs if row['id'] == job['id'])
    report.update(status=job['status'], provider=job['provider'], model=job.get('model'), error_code=job.get('error_code'), asset_id=job.get('asset_id'))
    report['checks'] = {'provider_matches': job['provider'] == args.provider, 'model_matches': job.get('model') == args.model, 'completed': job['status'] == 'succeeded', 'result_present': bool(job.get('analysis') if args.kind == 'analysis' else job.get('asset_id'))}
    report['quality_review'] = {'semantic_quality': 'human_review_required', 'character_consistency': 'not_measured', 'lip_sync': 'not_measured', 'billing': 'vendor_invoice_required'}
    return 0 if all(report['checks'].values()) else 1


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--kind', choices=PROVIDERS, required=True)
    parser.add_argument('--provider', required=True)
    parser.add_argument('--model', required=True)
    parser.add_argument('--budget-micros', type=int, required=True)
    parser.add_argument('--reserve-micros', type=int, required=True, help='operator-verified conservative maximum cost for this single request')
    parser.add_argument('--currency', default='USD')
    parser.add_argument('--base-url', default='http://127.0.0.1:8080')
    parser.add_argument('--origin', default='http://localhost:5173')
    parser.add_argument('--timeout', type=int, default=180)
    parser.add_argument('--ledger', type=Path, default=Path('live-eval-ledger.json'))
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--execute-live', action='store_true')
    parser.add_argument('--authorize-provider-model-budget', action='store_true')
    args = parser.parse_args(argv)
    try:
        validate(args)
        if args.execute_live and not args.authorize_provider_model_budget:
            raise ValueError('live execution requires explicit provider/model/budget authorization')
    except ValueError as error:
        parser.error(str(error))
    report = {'run_id': 'eval-' + uuid.uuid4().hex, 'mode': 'live' if args.execute_live else 'dry-run', 'provider': args.provider, 'model': args.model, 'kind': args.kind, 'budget_micros': args.budget_micros, 'reserve_micros': args.reserve_micros, 'currency': args.currency, 'fixture_sha256': hashlib.sha256(json.dumps(FIXTURE, sort_keys=True).encode()).hexdigest(), 'model_quality_verified': False, 'cost_state': 'not_called', 'matrix': PROVIDERS}
    # Claim the report path before any live side effect; rerunning an existing
    # report must never submit a second paid request.
    fd = os.open(args.report, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump({**report, 'status': 'starting'}, stream, indent=2)
        stream.flush()
        os.fsync(stream.fileno())
    code = 0
    if args.execute_live:
        try:
            auth = json.loads(sys.stdin.read(16385))
            code = run(args, auth, report)
        except Exception as error:
            # Do not persist exception URLs, response bodies, cookies or credentials.
            report.update(status='incomplete', failure_type=type(error).__name__)
            code = 1
    else:
        report.update(status='prepared', fixture=FIXTURE, prompt=PROMPT)
    with args.report.open('w') as stream:
        json.dump(report, stream, ensure_ascii=False, indent=2)
        stream.write('\n')
    print(json.dumps({'report': str(args.report), 'mode': report['mode'], 'status': report['status']}))
    return code

if __name__ == '__main__':
    sys.exit(main())
