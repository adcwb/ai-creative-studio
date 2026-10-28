#!/usr/bin/env python3
"""Real local secure HTTP acceptance for layered posters and social export.
Synthetic users/assets only. No social application config, OAuth or remote call.
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
    with tempfile.TemporaryDirectory(prefix='studio-iteration10-secure-') as folder:
        evidence = Path(folder)
        binary = evidence / 'server'
        subprocess.run(['go', 'build', '-o', str(binary), './cmd/server'], cwd=ROOT / 'backend', check=True)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        origin = f'http://127.0.0.1:{port}'
        env = {'PATH': os.environ['PATH'], 'HTTP_ADDR': f'127.0.0.1:{port}', 'DATA_DIR': str(evidence / 'data'), 'FRONTEND_ORIGIN': origin, 'AUTH_MODE': 'secure', 'AUTH_ALLOW_REGISTRATION': 'true', 'AUTH_COOKIE_SECURE': 'false', 'IMAGE_PROVIDER': 'mock-svg', 'TEXT_PROVIDER': 'mock-text'}
        def client():
            return urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        alice, bob, anonymous = client(), client(), client()
        def call(opener, method, path, route=None, body=None, csrf='', key='fixture-operation', status=200):
            nonlocal count
            request = urllib.request.Request(origin + path, method=method, headers={'Origin': origin, 'X-CSRF-Token': csrf, 'Idempotency-Key': key, 'Content-Type': 'application/json'}, data=None if body is None else json.dumps(body).encode())
            try:
                response = opener.open(request, timeout=30)
            except urllib.error.HTTPError as error:
                response = error
            with response:
                raw = response.read()
                assert response.status == status, (method, path, status, response.status, raw[:300])
                count += 1
                if 'application/json' not in response.headers.get('Content-Type', ''):
                    return raw
                value = json.loads(raw)
                if route:
                    responses = spec['paths'][route][method.lower()]['responses']
                    definition = responses.get(str(status)) or responses.get('default')
                    assert definition, (route, status, 'missing response definition')
                    if '$ref' in definition:
                        definition = resolver.resolve(definition['$ref'])[1]
                    OAS30Validator(definition['content']['application/json']['schema'], resolver=resolver).validate(value)
                return value
        process = None
        with (evidence / 'server.log').open('w') as log:
            def start():
                nonlocal process
                process = subprocess.Popen([str(binary)], env=env, stdout=log, stderr=log)
                for _ in range(200):
                    try:
                        with anonymous.open(origin + '/healthz', timeout=.2):
                            return
                    except OSError:
                        assert process.poll() is None, 'isolated API exited'
                        time.sleep(.05)
                raise AssertionError('isolated API startup timed out')
            try:
                start()
                a = call(alice, 'POST', '/api/v1/auth/register', body={'email': 'poster-alice@example.invalid', 'password': 'Synthetic-poster-passphrase!'})
                b = call(bob, 'POST', '/api/v1/auth/register', body={'email': 'poster-bob@example.invalid', 'password': 'Synthetic-poster-passphrase!'})
                tenant = a['spaces'][0]['id']; query = '?space_id=' + tenant; csrf = a['csrf_token']
                project = call(alice, 'POST', '/api/v1/projects' + query, body={'title': 'Synthetic secure creative acceptance', 'brief': 'Only synthetic text and logos; no real external publication.'}, csrf=csrf, status=201)
                base = '/api/v1/projects/' + project['id']; route = '/api/v1/projects/{id}'
                media = call(alice, 'POST', base + '/media/demo' + query, body={'expected_version': 1}, csrf=csrf)
                asset = next(item for item in media['assets'] if item['kind'] == 'image')
                poster = {'width': 256, 'height': 256, 'background': '#ffffff', 'layers': [{'id':'text','kind':'text','text':'Synthetic 合成','x':10,'y':10,'width':220,'height':60,'font_size':20,'color':'#111111','opacity':1}]}
                body = {'expected_version': media['version'], 'poster':poster}
                path = base + '/media/poster'; schema = route + '/media/poster'
                call(anonymous, 'PUT', path + query, schema, body, status=401)
                call(bob, 'PUT', path + query, schema, body, b['csrf_token'], status=403)
                call(alice, 'PUT', path + query, schema, body, status=403)
                saved = call(alice, 'PUT', path + query, schema, body, csrf, 'save-poster')
                assert call(alice, 'PUT', path + query, schema, body, csrf, 'save-poster')['version'] == saved['version']
                call(alice, 'PUT', path + query, schema, body, csrf, 'stale-poster', status=409)
                rendered = call(alice, 'POST', path + '/exports' + query, schema + '/exports', {'expected_version':saved['version']}, csrf, 'render-poster')
                png = rendered['assets'][-1]
                assert call(alice, 'GET', png['file_url']).startswith(b'\x89PNG\r\n\x1a\n')
                call(bob, 'GET', png['file_url'], status=403)
                social = call(alice, 'GET', base + '/social' + query, route + '/social')
                payload = {'expected_version':social['version'], 'platform':'xiaohongshu','mode':'export','asset_id':png['id'],'confirmed':True,'title':'','caption':''}
                jobpath = base + '/social/jobs'; jobschema = route + '/social/jobs'
                call(bob, 'POST', jobpath + query, jobschema, payload, b['csrf_token'], status=403)
                prepared = call(alice, 'POST', jobpath + query, jobschema, payload, csrf, 'social-export')
                assert prepared['jobs'][0]['status'] == 'prepared'
                assert len(call(alice, 'POST', jobpath + query, jobschema, payload, csrf, 'social-export')['jobs']) == 1
                call(alice, 'POST', jobpath + query, jobschema, {**payload,'mode':'direct','expected_version':prepared['version']}, csrf, 'no-direct', status=503)
                members = '/api/v1/organizations/' + tenant + '/members'
                call(alice, 'PUT', members, body={'email':b['user']['email'],'role':'viewer'},csrf=csrf)
                call(bob, 'GET', base + '/social' + query, route + '/social')
                call(bob, 'PUT', path + query, schema, {**body,'expected_version':prepared['version']}, b['csrf_token'], 'viewer-poster', status=403)
                call(bob, 'POST', jobpath + query, jobschema, {**payload,'expected_version':prepared['version']}, b['csrf_token'], 'viewer-social', status=403)
                process.terminate();process.wait(timeout=15);start()
                restored = call(alice, 'GET', base + '/social' + query, route + '/social')
                assert len(restored['jobs']) == 1 and restored['jobs'][0]['id'] == prepared['jobs'][0]['id']
                restored_poster = call(alice, 'GET', base + '/media' + query)['poster']
                assert restored_poster['width'] == 256 and restored_poster['layers'][0]['text'] == poster['layers'][0]['text']
                assert call(alice, 'GET', restored['jobs'][0]['download_url']).startswith(b'\x89PNG')
                call(alice, 'PUT', members, body={'email':b['user']['email'],'role':'remove'}, csrf=csrf)
                call(bob, 'GET', restored['jobs'][0]['download_url'], status=403)
                print(json.dumps({'result':'passed','http_responses':count,'checks':['poster actual PNG','new OpenAPI response contracts','tenant isolation','CSRF','viewer write denial','CAS','idempotency','file restart persistence','membership revocation','social export is not publication','unconfigured direct dispatch refused'],'social_calls':0,'real_credentials':False}))
            finally:
                if process and process.poll() is None:
                    process.terminate();process.wait(timeout=15)

if __name__ == '__main__':
    main()
