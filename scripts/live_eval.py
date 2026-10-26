#!/usr/bin/env python3
"""Dry-run creative contract inventory. Live execution is currently unavailable.
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
    'video': ['gemini-video', 'ark-video', 'minimax-video'],
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
    # The previous global-provider path did not bind a managed model version,
    # exact request quote or authoritative server budget. Never retain it as an
    # alternate execution path while the replacement harness is incomplete.
    raise ValueError('managed live execution harness is not implemented')


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
        if args.execute_live:
            raise ValueError('live harness unavailable: managed configuration, exact quote and server budget binding are not implemented; dry-run only')
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
        report.update(status='prepared_not_executable', execution_supported=False, execution_blockers=['managed_eval_harness_not_implemented'], fixture=FIXTURE, prompt=PROMPT)
    with args.report.open('w') as stream:
        json.dump(report, stream, ensure_ascii=False, indent=2)
        stream.write('\n')
    print(json.dumps({'report': str(args.report), 'mode': report['mode'], 'status': report['status']}))
    return code

if __name__ == '__main__':
    sys.exit(main())
