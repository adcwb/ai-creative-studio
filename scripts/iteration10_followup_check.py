#!/usr/bin/env python3
"""Real local secure HTTP acceptance for poster-to-PHOTO normalization and publication gates.
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
    with tempfile.TemporaryDirectory(prefix='studio-iteration10-followup-') as folder:
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
                a = call(alice, 'POST', '/api/v1/auth/register', body={'email':'photo-owner@example.invalid','password':'Synthetic-photo-password!'})
                b = call(bob, 'POST', '/api/v1/auth/register', body={'email':'photo-other@example.invalid','password':'Synthetic-photo-password!'})
                tenant = a['spaces'][0]['id']; query = '?space_id=' + tenant; csrf = a['csrf_token']
                project = call(alice, 'POST', '/api/v1/projects' + query, body={'title':'Synthetic PHOTO contract','brief':'Synthetic poster normalization; no external authorization or posting.'}, csrf=csrf, status=201)
                base = '/api/v1/projects/' + project['id']; route = '/api/v1/projects/{id}'
                poster = {'width':256,'height':256,'background':'#ffffff','layers':[{'id':'text','kind':'text','text':'Synthetic PHOTO','x':8,'y':8,'width':240,'height':60,'font_size':18,'color':'#123456','opacity':1}]}
                media = call(alice,'PUT',base+'/media/poster'+query,body={'expected_version':1,'poster':poster},csrf=csrf,key='photo-save-poster')
                media = call(alice,'POST',base+'/media/poster/exports'+query,body={'expected_version':media['version']},csrf=csrf,key='photo-render-poster')
                original = media['assets'][-1]
                original_bytes = call(alice,'GET',original['file_url'])
                endpoint = base+'/social/photos/normalize'; contract = route+'/social/photos/normalize'
                payload = {'expected_version':media['version'],'asset_id':original['id'],'confirmed':True}
                call(anonymous,'POST',endpoint+query,contract,payload,status=401)
                call(bob,'POST',endpoint+query,contract,payload,b['csrf_token'],status=403)
                call(alice,'POST',endpoint+query,contract,payload,status=403)
                call(alice,'POST',endpoint+query,contract,{**payload,'confirmed':False},csrf,'photo-no-consent',status=400)
                prepared = call(alice,'POST',endpoint+query,contract,payload,csrf,'photo-normalize')
                normalized_id = prepared['normalized_asset_id']
                normalized = next(item for item in prepared['assets'] if item['id']==normalized_id)
                assert normalized['mime_type']=='image/jpeg' and normalized['source']=='social-photo-normalized'
                photo_bytes = call(alice,'GET',normalized['file_url'])
                assert photo_bytes[:3]==b'\xff\xd8\xff'
                assert call(alice,'GET',original['file_url'])==original_bytes
                assert call(alice,'POST',endpoint+query,contract,payload,csrf,'photo-normalize')['normalized_asset_id']==normalized_id
                call(alice,'POST',endpoint+query,contract,payload,csrf,'photo-stale',status=409)
                call(bob,'GET',normalized['file_url'],status=403)
                call(anonymous,'GET',normalized['file_url'],status=401)
                intent={'expected_version':prepared['version'],'platform':'tiktok','mode':'direct','media_type':'PHOTO','photo_asset_ids':[normalized_id],'photo_cover_index':0,'public_media_confirmed':True,'confirmed':True,'rights_confirmed':True,'privacy_level':'SELF_ONLY'}
                call(alice,'POST',base+'/social/jobs'+query,route+'/social/jobs',intent,csrf,'photo-no-live',status=503)
                members = '/api/v1/organizations/'+tenant+'/members'
                call(alice,'PUT',members,body={'email':b['user']['email'],'role':'viewer'},csrf=csrf)
                call(bob,'POST',endpoint+query,contract,{**payload,'expected_version':prepared['version']},b['csrf_token'],'photo-viewer',status=403)
                process.terminate();process.wait(timeout=15);start()
                restored = call(alice,'POST',endpoint+query,contract,payload,csrf,'photo-normalize')
                assert restored['normalized_asset_id']==normalized_id and len(restored['assets'])==len(prepared['assets'])
                assert call(alice,'GET',normalized['file_url'])==photo_bytes
                assert call(alice,'GET',base+'/social'+query,route+'/social')['jobs']==[]
                print(json.dumps({'result':'passed','http_responses':count,'checks':['poster PNG to explicit owned JPEG','original PNG unchanged','normalization OpenAPI','consent and CSRF','tenant/viewer/download denial','CAS and exact normalization receipt replay','restart recovery','unconfigured PHOTO create refuses remote execution'],'social_calls':0,'real_credentials':False}))
            finally:
                if process and process.poll() is None:
                    process.terminate();process.wait(timeout=15)

if __name__ == '__main__':
    main()
