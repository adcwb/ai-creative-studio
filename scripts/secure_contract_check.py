#!/usr/bin/env python3
"""Validate real secure-mode responses and tenant/CSRF/RBAC boundaries against OpenAPI.
Uses only isolated synthetic accounts, local mock providers and a temporary DATA_DIR.
Run with the openapi-spec-validator environment Python and a Go toolchain on PATH.
"""
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
import warnings
with warnings.catch_warnings():
    warnings.simplefilter('ignore', DeprecationWarning)
    from jsonschema import RefResolver
from openapi_schema_validator import OAS30Validator
ROOT = Path(__file__).resolve().parents[1]

def main():
    spec = json.loads((ROOT / 'api/openapi.json').read_text())
    resolver = RefResolver.from_schema(spec)
    count = 0
    with tempfile.TemporaryDirectory(prefix='studio-secure-contract-') as folder:
        root = Path(folder)
        binary = root / 'server'
        subprocess.run(['go', 'build', '-o', str(binary), './cmd/server'], cwd=ROOT / 'backend', check=True)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        origin = f'http://127.0.0.1:{port}'
        env = {'PATH': os.environ['PATH'], 'DATA_DIR': str(root / 'data'), 'HTTP_ADDR': f'127.0.0.1:{port}', 'FRONTEND_ORIGIN': origin, 'AUTH_MODE': 'secure', 'AUTH_ALLOW_REGISTRATION': 'true', 'AUTH_COOKIE_SECURE': 'false', 'IMAGE_PROVIDER': 'mock-svg', 'TEXT_PROVIDER': 'mock-text'}
        def client():
            return urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        alice, bob, anonymous = client(), client(), client()
        def call(opener, method, path, route, body=None, csrf='', status=200):
            nonlocal count
            headers = {'Content-Type': 'application/json', 'Origin': origin, 'X-CSRF-Token': csrf, 'Idempotency-Key': 'secure-contract-operation'}
            request = urllib.request.Request(origin + path, method=method, headers=headers, data=None if body is None else json.dumps(body).encode())
            try:
                response = opener.open(request, timeout=15)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read()
                assert response.status == status, (method, path, response.status, status, raw[:500])
                value = json.loads(raw)
                operation = spec['paths'][route][method.lower()]
                definition = operation['responses'].get(str(status)) or operation['responses'].get('default')
                assert definition is not None, (route, method, status, 'missing response contract')
                if '$ref' in definition:
                    definition = resolver.resolve(definition['$ref'])[1]
                schema = definition['content']['application/json']['schema']
                OAS30Validator(schema, resolver=resolver).validate(value)
                count += 1
                return value
        with (root / 'server.log').open('w') as log:
            process = subprocess.Popen([str(binary)], env=env, stdout=log, stderr=log)
            try:
                for _ in range(100):
                    try:
                        with anonymous.open(origin + '/healthz', timeout=.2):
                            break
                    except OSError:
                        if process.poll() is not None:
                            raise RuntimeError('secure server exited')
                        time.sleep(.05)
                session_route = '/api/v1/auth/session'
                assert call(anonymous, 'GET', session_route, session_route)['authenticated'] is False
                for opener, email in ((alice, 'alice@example.invalid'), (bob, 'bob@example.invalid')):
                    session = call(opener, 'POST', '/api/v1/auth/register', '/api/v1/auth/register', {'email': email, 'password': 'Synthetic-password-2026!'})
                    assert session['authenticated'] is True
                a = call(alice, 'GET', session_route, session_route)
                b = call(bob, 'GET', session_route, session_route)
                org, other = a['spaces'][0]['id'], b['spaces'][0]['id']
                query = '?space_id=' + org
                aq, bq = a['csrf_token'], b['csrf_token']
                governance = '/api/v1/governance'
                report = call(alice, 'GET', governance + query, governance)
                assert report['currency'] == 'USD' and report['limit_minor'] == 0 and report['live_execution_enabled'] is False
                call(anonymous, 'GET', governance + query, governance, status=401)
                call(bob, 'GET', governance + query, governance, status=403)
                budget = governance + '/budget'
                payload = {'currency': 'USD', 'limit_minor': 300, 'expected_limit_minor': 0}
                call(alice, 'PUT', budget + query, budget, payload, status=403)
                assert call(alice, 'PUT', budget + query, budget, payload, aq)['limit_minor'] == 300
                call(alice, 'PUT', budget + query, budget, payload, aq, status=409)
                assert call(alice, 'GET', governance + query + '&currency=CNY', governance)['limit_minor'] == 0
                call(alice, 'PUT', budget + query, budget, {'currency': 'CNY', 'limit_minor': 500, 'expected_limit_minor': 0}, aq)
                assert call(alice, 'GET', governance + query + '&currency=USD', governance)['limit_minor'] == 300
                call(alice, 'PUT', budget + query, budget, {'currency': 'USD', 'limit_minor': -1, 'expected_limit_minor': 300}, aq, status=400)
                member = '/api/v1/organizations/' + org + '/members'
                member_route = '/api/v1/organizations/{id}/members'
                call(alice, 'GET', member, member_route)
                call(alice, 'PUT', member, member_route, {'email': b['user']['email'], 'role': 'viewer'}, aq)
                call(bob, 'GET', governance + query, governance)
                call(bob, 'PUT', budget + query, budget, {'currency': 'USD', 'limit_minor': 301, 'expected_limit_minor': 300}, bq, status=403)
                call(alice, 'PUT', member, member_route, {'email': b['user']['email'], 'role': 'remove'}, aq)
                call(bob, 'GET', governance + query, governance, status=403)
                assert call(bob, 'GET', governance + '?space_id=' + other, governance)['limit_minor'] == 0
                exchange = governance + '/exchange'
                call(alice, 'GET', exchange + query + '&from=USD&to=CNY', exchange, status=503)
                call(alice, 'GET', exchange + query + '&from=USD&to=USD', exchange)
                model_route = '/api/v1/model-config'
                model = call(alice, 'GET', model_route + query, model_route)
                assert model['secret_storage_available'] is False and model['live_enabled'] is False and model['runtime_applied'] is False
                call(bob, 'GET', model_route + query, model_route, status=403)
                declaration = {'expected_version': model['version'], 'entry': {'provider': 'openai-text', 'model': 'synthetic-model-declaration', 'capability': 'text', 'region': 'global', 'enabled': False, 'is_default': False, 'lifecycle': 'active'}, 'credential': {'action': 'keep'}}
                model = call(alice, 'PUT', model_route + query, model_route, declaration, aq)
                assert model['entries'][0]['secret_status'] == 'not_configured'
                call(alice, 'PUT', model_route + query, model_route, declaration, aq, status=409)
                validate_route = model_route + '/validate'
                call(alice, 'POST', validate_route + query, validate_route, {'id': model['entries'][0]['id']}, aq)
                call(alice, 'POST', model_route + '/live' + query, model_route + '/live', {}, aq, status=403)
                call(alice, 'POST', '/api/v1/auth/logout', '/api/v1/auth/logout', {}, aq)
                assert call(alice, 'GET', session_route, session_route)['authenticated'] is False
                print(json.dumps({'result': 'passed', 'contract_responses': count, 'checks': ['secure sessions', 'CSRF', 'tenant isolation', 'owner-only budget', 'CAS', 'currency separation', 'revocation', 'missing FX fail closed', 'credential-free model declarations', 'local validation only', 'live endpoint refused'], 'live_provider_calls': 0}))
            finally:
                process.terminate()
                process.wait(timeout=15)

if __name__ == '__main__':
    main()
