#!/usr/bin/env python3
"""Explicit managed evaluation against a loopback Studio server.
Default dry-run never reads credentials, existing configuration or makes requests.
"""
import argparse
import datetime
import fcntl
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import re
import stat
import struct
import zlib
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
    'video': ['gemini-video', 'ark-video', 'minimax-video'],
    'transcription': ['openai-asr'],
}
FIXTURE = {'title': 'Synthetic clockwork garden', 'brief': '合成测试：纸片机器人在虚构花园寻找一枚蓝色齿轮。没有真人、品牌或个人数据。'}
PROMPT = 'Synthetic test only: a paper robot finds a blue gear in an imaginary garden. No real people or brands.'

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def validate(args):
    if args.kind not in PROVIDERS or args.provider not in PROVIDERS[args.kind]:
        raise ValueError('provider does not implement the requested modality in this harness')
    if not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}', args.model):
        raise ValueError('explicit model required')
    if args.budget_micros <= 0 or args.reserve_micros <= 0 or args.reserve_micros > args.budget_micros:
        raise ValueError('positive reserve must fit budget (integer millionths of selected currency)')
    approved = {'USD': 3000000, 'CNY': 5000000}
    if args.currency not in approved or args.budget_micros > approved[args.currency]:
        raise ValueError('only separately approved USD 3 / CNY 5 caps are supported; no conversion or top-up')
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
    lock = os.open(str(path) + '.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    if not stat.S_ISREG(os.fstat(lock).st_mode):
        os.close(lock)
        raise ValueError('ledger lock must be a regular file')
    with os.fdopen(lock, 'w') as stream:
        fcntl.flock(stream, fcntl.LOCK_EX)
        try:
            read_fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
        except FileNotFoundError:
            ledger = {'currency': args.currency, 'reservations': []}
        else:
            with os.fdopen(read_fd, 'r') as source:
                info = os.fstat(source.fileno())
                if not stat.S_ISREG(info.st_mode) or info.st_size > 1024 * 1024 or info.st_mode & 0o077:
                    raise ValueError('ledger must be a private bounded regular file')
                ledger = json.loads(source.read())
        rows = ledger.get('reservations')
        if not isinstance(rows, list) or len(rows) > 10000 or any(not isinstance(row, dict) or not valid_int(row.get('reserved_micros'), 1) or row.get('state') != 'unknown' for row in rows):
            raise ValueError('invalid reservation ledger; do not infer free budget')
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


def candidate(args):
    plan = json.loads((Path(__file__).resolve().parents[1] / 'eval/candidate-plan.json').read_text())
    case = next((row for row in plan['cases'] if row['id'] == args.case_id), None)
    if case is None or any(case[key] != getattr(args, key) for key in ('kind', 'provider', 'model')):
        raise ValueError('case, provider, model and kind must match the reviewed candidate plan exactly')
    return case


def validate_execution(args):
    validate(args)
    if not args.authorize_provider_model_budget or not args.authorize_session_transmission:
        raise ValueError('separate provider/model/budget and session-transmission authorization required')
    case = candidate(args)
    if not case['harness_supported'] or case['execution_blockers']:
        raise ValueError('candidate contract is blocked; no model or specification substitution')
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', args.model_config_id) or args.model_config_version < 1:
        raise ValueError('explicit managed configuration ID and positive version required')
    if args.budget_micros % 10000 or args.reserve_micros % 10000:
        raise ValueError('live caps must be whole minor units; no silent rounding')
    if args.kind == 'speech' and not re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9_.:/-]{0,127}', args.voice):
        raise ValueError('explicit account-authorized voice required')
    if args.kind != 'speech' and args.voice:
        raise ValueError('voice only applies to speech')
    if args.scenario == 'reference' and args.kind != 'image':
        raise ValueError('reference scenario only applies to image cases')
    return case


def synthetic_png():
    # Original geometric pixels; no external files, faces or user data.
    width = height = 32
    rows = b''.join(b'\x00' + b''.join(bytes((40, 90 if x < 16 else 180, 200, 255)) for x in range(width)) for _ in range(height))
    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data) & 0xffffffff)
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 6, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(rows)) + chunk(b'IEND', b'')


def valid_int(value, minimum=0):
    return type(value) is int and minimum <= value <= 9007199254740991


def validate_budget(value, args):
    limit = args.budget_micros // 10000
    reserve_minor = args.reserve_micros // 10000
    if value.get('currency') != args.currency or value.get('minor_units') != 2:
        raise ValueError('authoritative budget currency/unit mismatch')
    if not all(valid_int(value.get(key)) for key in ('limit_minor', 'exposure_minor', 'available_minor')):
        raise ValueError('malformed authoritative budget')
    if not 0 < value['limit_minor'] <= limit or value['available_minor'] < reserve_minor or value['exposure_minor'] + value['available_minor'] != value['limit_minor']:
        raise ValueError('server budget must fit approved cap and remaining exposure; configure it separately')


def validate_quote(value, args):
    if (not valid_int(value.get('model_config_version'), 1) or value.get('model_config_id') != args.model_config_id or value.get('model_config_version') != args.model_config_version or
        value.get('currency') != args.currency or value.get('minor_units') != 2 or value.get('contract') != 'studio-request-v1' or
        not valid_int(value.get('maximum_minor'), 1) or value['maximum_minor'] * 10000 > args.reserve_micros or
        not isinstance(value.get('source'), str) or not value['source'].strip() or
        not re.fullmatch(r'[a-f0-9]{64}', value.get('request_sha256', ''))):
        raise ValueError('quote is not exact, bound to this configuration, or within the original currency cap')
    expires = datetime.datetime.fromisoformat(value['expires_at'].replace('Z', '+00:00'))
    if expires.tzinfo is None or expires <= datetime.datetime.now(datetime.timezone.utc):
        raise ValueError('quote expired or timezone missing')


def run(args, auth, report):
    validate_execution(args)  # Also protects direct callers, not just main().
    for field in ('cookie', 'csrf_token', 'space_id'):
        if not isinstance(auth.get(field), str) or not auth[field] or any(c in auth[field] for c in '\r\n'):
            raise ValueError('stdin must contain cookie, csrf_token and space_id strings')
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', auth['space_id']):
        raise ValueError('invalid organization ID')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    def call(path, body=None, key=None, mime='application/json', method=None):
        headers = {'Cookie': auth['cookie'], 'X-CSRF-Token': auth['csrf_token'], 'Origin': args.origin, 'Content-Type': mime}
        if key:
            headers['Idempotency-Key'] = key
        payload = body if isinstance(body, bytes) else None if body is None else json.dumps(body).encode()
        request = urllib.request.Request(args.base_url + path, data=payload, headers=headers, method=method)
        with opener.open(request, timeout=min(args.timeout, 30)) as response:
            raw = response.read(1024 * 1024 + 1)
            if len(raw) > 1024 * 1024:
                raise ValueError('response too large')
            return json.loads(raw)
    session = call('/api/v1/auth/session')
    if session.get('mode') != 'secure' or session.get('authenticated') is not True or auth['space_id'] not in [row.get('id') for row in session.get('spaces', [])]:
        raise ValueError('authenticated secure session in requested organization required')
    query = '?' + urllib.parse.urlencode({'space_id': auth['space_id']})
    budget_path = '/api/v1/governance' + query + '&currency=' + args.currency
    def check_catalog():
        catalog = call('/api/v1/runtime-models' + query)
        found = next((row for row in catalog.get('models', []) if row.get('id') == args.model_config_id), {})
        capability = 'text' if args.kind == 'analysis' else 'asr' if args.kind == 'transcription' else args.kind
        if catalog.get('governance_ready') is not True or found.get('available') is not True or not valid_int(found.get('version'), 1) or any(found.get(key) != expected for key, expected in
            [('version', args.model_config_version), ('provider', args.provider), ('model', args.model), ('capability', capability)]):
            raise ValueError('managed model unavailable, changed or mismatched; no fallback')
    check_catalog()
    validate_budget(call(budget_path), args)
    project = call('/api/v1/projects' + query, FIXTURE, report['run_id'] + '-project')
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', project.get('id', '')) or not valid_int(project.get('version'), 1):
        raise ValueError('invalid synthetic project response')
    report['project_id'] = project['id']
    base = '/api/v1/projects/' + project['id']
    job_input = {'kind': args.kind, 'expected_version': project['version']}
    if args.kind != 'analysis':
        job_input['prompt'] = PROMPT[:200] if args.kind == 'speech' else PROMPT
    if args.scenario == 'reference':
        pixels = synthetic_png()
        state = call(base + '/media/uploads' + query + '&role=reference&filename=synthetic-reference.png', pixels, report['run_id'] + '-reference', 'image/png')
        assets = state.get('assets', [])
        if len(assets) != 1 or assets[0].get('kind') != 'image' or not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', assets[0].get('id', '')):
            raise ValueError('synthetic reference upload response ambiguous')
        job_input['reference_asset_id'] = assets[0]['id']
        report['reference_sha256'] = hashlib.sha256(pixels).hexdigest()
    if args.kind == 'transcription':
        state = call(base + '/media/demo' + query, {'expected_version': 1}, report['run_id'] + '-demo')
        audio = next((asset for asset in state.get('assets', []) if asset.get('kind') == 'audio'), {})
        if not 0 < audio.get('duration', 0) <= 30 or not state.get('timeline', {}).get('clips'):
            raise ValueError('bounded synthetic audio fixture unavailable')
        timeline = state['timeline']
        timeline['clips'][0]['audio_asset_id'] = audio['id']
        state = call(base + '/media/timeline' + query, {'expected_version': state['version'], 'timeline': timeline}, report['run_id'] + '-timeline', method='PUT')
        job_input = {'mode': 'transcription', 'clip_id': timeline['clips'][0]['id'], 'audio_asset_id': audio['id'], 'expected_version': state['version']}
        report['audio_fixture'] = 'application-generated synthetic tone; not speech quality evidence'
        report['audio_seconds'] = audio['duration']
    quote_input = {'operation': 'alignment' if args.kind == 'transcription' else 'creative', 'project_id': project['id'], 'model_config_id': args.model_config_id,
                   'model_config_version': args.model_config_version, 'input': job_input}
    if args.voice:
        quote_input['voice'] = args.voice
    quote = call('/api/v1/runtime-quote' + query, quote_input)
    validate_quote(quote, args)
    # Read again immediately before local reservation; server acceptance still
    # performs authoritative authorization, quote validation and atomic reserve.
    check_catalog()
    validate_budget(call(budget_path), args)
    validate_quote(quote, args)
    reserve(args.ledger, args, report['run_id'])
    report['cost_state'] = 'unknown_reserved'
    report['quote_maximum_minor'] = quote['maximum_minor']
    report['quote_request_sha256'] = quote['request_sha256']
    request = {**job_input, 'model_config_id': args.model_config_id, 'model_config_version': args.model_config_version,
               'budget_currency': args.currency, 'budget_maximum_minor': args.reserve_micros // 10000,
               'quote_request_sha256': quote['request_sha256']}
    if args.voice:
        request['voice'] = args.voice
    jobs_path = base + ('/media/alignments' if args.kind == 'transcription' else '/creative-jobs')
    job = call(jobs_path + query, request, report['run_id'] + '-generation')
    if not re.fullmatch(r'[A-Za-z0-9_-]{1,128}', job.get('id', '')):
        raise ValueError('acceptance outcome unknown')
    report['job_id'] = job['id']
    deadline = time.monotonic() + args.timeout
    while job.get('status') in ('queued', 'running') and time.monotonic() < deadline:
        time.sleep(1)
        jobs = call(jobs_path + query)
        job = next(row for row in jobs if row.get('id') == report['job_id'])
    report['status'] = job.get('status', 'unknown') if job.get('status') in ('queued', 'running', 'succeeded', 'failed', 'cancelled') else 'unknown'
    report['checks'] = {'provider_matches': job.get('provider') == args.provider, 'model_matches': job.get('model') == args.model,
                        'completed': job.get('status') == 'succeeded', 'result_present': bool(job.get('analysis') if args.kind == 'analysis' else job.get('transcript') if args.kind == 'transcription' else job.get('asset_id'))}
    if args.kind == 'transcription':
        report['checks']['text_only'] = job.get('mode') == 'transcription' and job.get('timing_basis') == 'transcript-only' and job.get('cues') == []
    report['quality_review'] = {'semantic_quality': 'human_review_required', 'character_consistency': 'not_measured', 'lip_sync': 'not_measured', 'billing': 'authoritative_vendor_evidence_required'}
    # Success is not evidence of actual cost. Neither ledger is settled here.
    return 0 if all(report['checks'].values()) else 1


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--kind', choices=PROVIDERS, required=True)
    parser.add_argument('--provider', required=True)
    parser.add_argument('--model', required=True)
    parser.add_argument('--budget-micros', type=int, required=True)
    parser.add_argument('--reserve-micros', type=int, required=True, help='operator-verified conservative maximum cost for this single request')
    parser.add_argument('--currency', default='USD')
    parser.add_argument('--case-id', default='')
    parser.add_argument('--model-config-id', default='')
    parser.add_argument('--model-config-version', type=int, default=0)
    parser.add_argument('--voice', default='')
    parser.add_argument('--scenario', choices=('original', 'reference'), default='original')
    parser.add_argument('--base-url', default='http://127.0.0.1:8080')
    parser.add_argument('--origin', default='http://localhost:5173')
    parser.add_argument('--timeout', type=int, default=180)
    parser.add_argument('--ledger', type=Path, default=Path('live-eval-ledger.json'))
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--execute-live', action='store_true')
    parser.add_argument('--authorize-provider-model-budget', action='store_true')
    parser.add_argument('--authorize-session-transmission', action='store_true')
    args = parser.parse_args(argv)
    try:
        validate(args)
        if args.case_id:
            candidate(args)
        if args.execute_live:
            validate_execution(args)
    except ValueError as error:
        parser.error(str(error))
    report = {'run_id': 'eval-' + uuid.uuid4().hex, 'mode': 'live' if args.execute_live else 'dry-run', 'provider': args.provider, 'model': args.model, 'kind': args.kind, 'budget_micros': args.budget_micros, 'reserve_micros': args.reserve_micros, 'currency': args.currency, 'fixture_sha256': hashlib.sha256(json.dumps(FIXTURE, sort_keys=True).encode()).hexdigest(), 'model_quality_verified': False, 'cost_state': 'not_called', 'matrix': PROVIDERS, 'case_id': args.case_id, 'scenario': args.scenario, 'model_config_id': args.model_config_id, 'model_config_version': args.model_config_version}
    # Claim the report path before any live side effect; rerunning an existing
    # report must never submit a second paid request.
    fd = os.open(args.report, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump({**report, 'status': 'starting'}, stream, indent=2)
        stream.flush()
        os.fsync(stream.fileno())
        code = 0
        if args.execute_live:
            try:
                auth_raw = sys.stdin.read(16385)
                if len(auth_raw) > 16384:
                    raise ValueError('session input too large')
                auth = json.loads(auth_raw)
                code = run(args, auth, report)
            except Exception as error:
                # Do not persist exception URLs, response bodies, cookies or credentials.
                report.update(status='incomplete', failure_type=type(error).__name__)
                code = 1
        else:
            case = candidate(args) if args.case_id else None
            report.update(status='prepared_not_authorized', execution_supported=bool(case and case['harness_supported'] and not case['execution_blockers']), execution_blockers=case['execution_blockers'] if case else ['explicit_reviewed_case_required'], adapter_contract=case['adapter_contract'] if case else None, fixture=FIXTURE, prompt=PROMPT)
        stream.seek(0)
        stream.truncate()
        json.dump(report, stream, ensure_ascii=False, indent=2)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())
    print(json.dumps({'report': str(args.report), 'mode': report['mode'], 'status': report['status']}))
    return code

if __name__ == '__main__':
    sys.exit(main())
