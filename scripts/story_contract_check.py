#!/usr/bin/env python3
"""Real secure HTTP contracts for continuity review, using only local synthetic services."""
import base64
import http.cookiejar
import http.server
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
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
    calls = []
    class Upstream(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass
        def do_POST(self):
            assert self.path == '/v1/chat/completions'
            assert self.headers['Authorization'] == 'Bearer synthetic-continuity-key'
            body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            assert body['model'] == 'synthetic-continuity-text' and body['max_completion_tokens'] == 512
            assert 'response_format' not in body
            data = json.loads(body['messages'][1]['content'])
            assert 'sources' not in data and 'omitted' not in data
            record = data['material'][0]
            findings = [dict(id='', title='Synthetic finding ' + str(i), kind='ambiguity', severity='note', confidence='low', explanation='A creator must decide whether this is intentional.', evidence=[dict(record_id=record['id'], version_id=record['version_id'], quote=record['title'])]) for i in range(3)]
            result = dict(summary='Synthetic local-only continuity review.', findings=findings)
            calls.append(body)
            raw = json.dumps(dict(choices=[dict(finish_reason='stop', message=dict(content=json.dumps(result)))])).encode()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
    upstream = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Upstream)
    thread = threading.Thread(target=upstream.serve_forever, daemon=True)
    thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix='studio-story-contract-') as folder:
            root = Path(folder)
            binary = root / 'server'
            subprocess.run(['go', 'build', '-o', str(binary), './cmd/server'], cwd=ROOT / 'backend', check=True)
            with socket.socket() as sock:
                sock.bind(('127.0.0.1', 0))
                port = sock.getsockname()[1]
            origin = f'http://127.0.0.1:{port}'
            moderator = root / 'moderate'
            moderator.write_text('#!/bin/sh\ncat >/dev/null\nprintf \'{"provider":"synthetic-fixture","model":"synthetic","status":"allow","categories":[],"checked_at":"%s"}\' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"\n')
            moderator.chmod(0o700)
            env = dict(PATH=os.environ['PATH'], DATA_DIR=str(root / 'data'), HTTP_ADDR=f'127.0.0.1:{port}', FRONTEND_ORIGIN=origin, AUTH_MODE='secure', AUTH_ALLOW_REGISTRATION='true', AUTH_COOKIE_SECURE='false', IMAGE_PROVIDER='mock-svg', TEXT_PROVIDER='mock-text', MODEL_CONFIG_KEK_BASE64=base64.b64encode(bytes([71])*32).decode(), MODEL_GATEWAY_ALLOWED_HOSTS=f'127.0.0.1:{upstream.server_port}', GATEWAY_MODERATION_COMMAND=str(moderator), GATEWAY_MODERATION_SOURCE='synthetic local-only continuity fixture', GATEWAY_MODERATION_EXPIRES_AT=time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(time.time()+3600)))
            process = subprocess.Popen([str(binary)], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
            count = 0
            def client():
                return urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
            owner, viewer, anonymous = client(), client(), client()
            def call(actor, method, path, route, body=None, csrf='', status=200, key=None):
                nonlocal count
                headers = {'Content-Type':'application/json', 'Origin':origin, 'X-CSRF-Token':csrf, 'Idempotency-Key':key or f'story-contract-{count}'}
                request = urllib.request.Request(origin+path, method=method, headers=headers, data=None if body is None else json.dumps(body).encode())
                try:
                    response = actor.open(request, timeout=15)
                except urllib.error.HTTPError as error:
                    response = error
                with response:
                    value = json.load(response)
                    assert response.status == status, (method, path, status, response.status, value)
                definition = spec['paths'][route][method.lower()]['responses'][str(status)]
                while '$ref' in definition:
                    definition = resolver.resolve(definition['$ref'])[1]
                schema = definition['content']['application/json']['schema']
                OAS30Validator(schema, resolver=resolver).validate(value)
                count += 1
                return value
            try:
                deadline = time.monotonic()+15
                while True:
                    try:
                        owner.open(origin+'/healthz', timeout=1).close()
                        break
                    except (OSError, urllib.error.URLError):
                        if process.poll() is not None or time.monotonic()>deadline:
                            raise RuntimeError('isolated story contract server failed to start')
                        time.sleep(0.03)
                a = call(owner,'POST','/api/v1/auth/register','/api/v1/auth/register',dict(email='story-contract-owner@example.invalid',password='Synthetic-story-contract-password!'))
                b = call(viewer,'POST','/api/v1/auth/register','/api/v1/auth/register',dict(email='story-contract-viewer@example.invalid',password='Synthetic-story-contract-password!'))
                aq,bq=a['csrf_token'],b['csrf_token']
                space=a['spaces'][0]['id'];query='?space_id='+space
                root_series=call(owner,'POST','/api/v1/series'+query,'/api/v1/series',dict(title='Continuity contract',summary='A traveller has incomplete knowledge.'),aq,status=201)
                season=call(owner,'POST','/api/v1/series/'+root_series['id']+'/seasons'+query,'/api/v1/series/{id}/seasons',{},aq,status=201)
                episode=call(owner,'POST','/api/v1/seasons/'+season['id']+'/episodes'+query,'/api/v1/seasons/{id}/episodes',dict(title='The doorway'),aq,status=201)
                members='/api/v1/organizations/'+space+'/members'
                call(owner,'PUT',members,'/api/v1/organizations/{id}/members',dict(email=b['user']['email'],role='viewer'),aq)
                story_path='/api/v1/series/'+root_series['id']+'/story-records';story_route='/api/v1/series/{id}/story-records'
                bible=dict(expected_version=0,title='Canonical story bible',kind='bible',status='active',required=True,body=dict(evidence=[],bible=dict(theme='Uncertainty and trust',world_rules='Knowledge is not shared automatically.',story_arc='',visual_intent='',immutable_rules='',characters=[dict(id='9'*32,name='Canonical traveller',desire='',flaw='',arc='',relationships='',visual_intent='',voice_intent='')])) )
                call(owner,'POST',story_path+query,story_route,bible,aq,status=201)
                project_path='/api/v1/projects/'+episode['id'];project_route='/api/v1/projects/{id}'
                project=call(owner,'GET',project_path+query,project_route)
                script=dict(structure_version=2,title='Doorway',logline='A traveller hesitates.',characters=[dict(id='4'*32,name='Traveller',description='A cautious traveller.')],scenes=[dict(id='3'*32,episode_number=1,heading='The doorway',action='A traveller hesitates before opening the door.',dialogue=[])])
                project=call(owner,'PUT',project_path+'/script'+query,project_route+'/script',dict(expected_version=project['version'],script=script),aq)
                report=call(owner,'POST','/api/v1/ai-gateways'+query,'/api/v1/ai-gateways',dict(expected_version=0,gateway=dict(name='Synthetic review gateway',base_url=f'http://127.0.0.1:{upstream.server_port}/v1',enabled=True,timeout_seconds=30),credential=dict(action='set',secret='synthetic-continuity-key')),aq)
                model=call(owner,'POST','/api/v1/gateway-models'+query,'/api/v1/gateway-models',dict(expected_version=0,model=dict(gateway_id=report['gateways'][0]['id'],name='Synthetic review text',model='synthetic-continuity-text',capabilities=['text'],enabled=True,max_output_tokens=512,json_output=False)),aq)['models'][0]
                call(owner,'PUT','/api/v1/model-features/story_continuity'+query,'/api/v1/model-features/{feature_key}',dict(expected_version=0,binding=dict(feature_key='story_continuity',model_config_id=model['id'],enabled=True,allow_override=True)),aq)
                snapshots='/api/v1/episodes/'+episode['id']+'/context-snapshots'
                snapshot=call(owner,'POST',snapshots+query,'/api/v1/episodes/{id}/context-snapshots',{},aq,status=201)
                checks='/api/v1/episodes/'+episode['id']+'/continuity-checks';check_route='/api/v1/episodes/{id}/continuity-checks'
                request=dict(context_snapshot_id=snapshot['id'],model_config_id=model['id'],model_config_version=model['version'])
                call(anonymous,'GET',checks+query,check_route,status=401)
                call(viewer,'POST',checks+query,check_route,request,bq,status=403)
                call(owner,'POST',checks+query,check_route,request,status=403)
                call(owner,'POST',checks+query,check_route,{**request,'created_by':'forged'},aq,status=400)
                call(owner,'POST',checks+query,check_route,dict(context_snapshot_id=snapshot['id']),aq,status=409)
                check=call(owner,'POST',checks+query,check_route,request,aq,status=202,key='story-http-check-original')
                detail=checks+'/'+check['id'];detail_route=check_route+'/{check_id}'
                deadline=time.monotonic()+8
                while check['state'] in ('queued','running'):
                    assert time.monotonic()<deadline, 'local review did not finish'
                    time.sleep(.025)
                    check=call(owner,'GET',detail+query,detail_route)
                assert check['state']=='succeeded' and len(check['result']['findings'])==3
                assert call(owner,'POST',checks+query,check_route,request,aq,status=202,key='story-http-check-original')['id']==check['id']
                page=call(viewer,'GET',checks+query,check_route)
                assert 'result' not in page['items'][0] and 'selection' not in check
                findings=check['result']['findings']
                first=detail+'/findings/'+findings[0]['id'];first_route=detail_route+'/findings/{finding_id}'
                view=call(viewer,'GET',first+'/review'+query,first_route+'/review')
                script['scenes'][0]['action']='The traveller pauses, then deliberately keeps the door closed.'
                call(owner,'PUT',project_path+'/script'+query,project_route+'/script',dict(expected_version=project['version'],script=script),aq)
                view=call(owner,'GET',first+'/review'+query,first_route+'/review')
                assert view['sources_changed'] and view['script_changed'] and view['current_script_version']==2
                proposal=dict(expected_version=0,title='Author-confirmed uncertainty',kind='fact',status='active',required=False,body=dict(evidence=[],fact=dict(subject='Traveller',predicate='does not know',value='Who is outside',perspective='unconfirmed',story_time='',known_by=[])))
                adoption=dict(action='adopt',reason='The creator preserves uncertainty.',review_sha256=view['review_sha256'],acknowledge_changed=False,proposal=proposal)
                decision_route=first_route+'/decisions';decision_path=first+'/decisions'
                call(viewer,'POST',decision_path+query,decision_route,adoption,bq,status=403)
                call(owner,'POST',decision_path+query,decision_route,adoption,aq,status=409)
                adoption['acknowledge_changed']=True
                decision=call(owner,'POST',decision_path+query,decision_route,adoption,aq,status=201,key='story-http-adoption-original')
                target=story_path+'/'+decision['target_record_id'];target_route=story_route+'/{record_id}'
                later=json.loads(json.dumps(proposal));later['expected_version']=1;later['title']='Later creator revision'
                call(owner,'PUT',target+query,target_route,later,aq)
                assert call(owner,'POST',decision_path+query,decision_route,adoption,aq,status=201,key='story-http-adoption-original')==decision
                assert call(owner,'GET',target+query,target_route)['number']==2
                for finding,action in zip(findings[1:],['intentional','reject']):
                    path=detail+'/findings/'+finding['id'];current=call(owner,'GET',path+'/review'+query,first_route+'/review')
                    data=dict(action=action,reason='The creator makes an explicit artistic decision.',review_sha256=current['review_sha256'],acknowledge_changed=False)
                    result=call(owner,'POST',path+'/decisions'+query,decision_route,data,aq,status=201)
                    assert result['action']==action and 'version_id' not in result
                # A cast saves explicit identities and pinned assets, never a model call.
                options_path='/api/v1/episodes/'+episode['id']+'/cast-options';options_route='/api/v1/episodes/{id}/cast-options'
                options=call(viewer,'GET',options_path+query,options_route)
                asset=call(owner,'POST','/api/v1/library/assets'+query,'/api/v1/library/assets',dict(kind='character',card=dict(name='Traveller asset',description='',prohibited_changes=[],character=dict(appearance='A traveller',hair='',costume='Raincoat',expression='',voice_notes='Not consent')),references=[]),aq,status=201)
                cast_path='/api/v1/episodes/'+episode['id']+'/cast';cast_route='/api/v1/episodes/{id}/cast'
                cast=dict(expected_version=0,title='Episode cast',kind='cast',status='active',required=True,body=dict(evidence=[],cast=dict(episode_id=episode['id'],script_version=options['script_version'],script_sha256=options['script_sha256'],bible_version_id=options['bible']['version_id'],characters=[dict(script_character_id='4'*32,script_character_name='Traveller',series_character_id='9'*32,series_character_name='Canonical traveller',library_asset_id=asset['asset']['id'],asset_version_id=asset['version']['id'],asset_name=asset['version']['card']['name'],notes='A cautious performance, not a voice authorization.')])) )
                call(viewer,'PUT',cast_path+query,cast_route,cast,bq,status=403)
                call(owner,'PUT',cast_path+query,cast_route,cast,status=403)
                call(owner,'PUT',cast_path+query,cast_route,{**cast,'actor_id':'forged'},aq,status=400)
                accepted_cast=call(owner,'PUT',cast_path+query,cast_route,cast,aq,key='story-http-cast-original')
                assert accepted_cast['number']==1
                assert call(owner,'PUT',cast_path+query,cast_route,cast,aq,key='story-http-cast-original')==accepted_cast
                call(viewer,'GET',options_path+'?space_id='+b['spaces'][0]['id'],options_route,status=404)
                project=call(owner,'GET',project_path+query,project_route)
                script['characters'][0]['name']='A different display name'
                project=call(owner,'PUT',project_path+'/script'+query,project_route+'/script',dict(expected_version=project['version'],script=script),aq)
                stale_cast=json.loads(json.dumps(cast));stale_cast['expected_version']=1
                call(owner,'PUT',cast_path+query,cast_route,stale_cast,aq,status=409)
                assert call(owner,'PUT',cast_path+query,cast_route,cast,aq,key='story-http-cast-original')==accepted_cast
                options=call(owner,'GET',options_path+query,options_route)
                assert options['current']['body']['cast']['characters'][0]['script_character_name']=='Traveller'
                stale_cast['body']['cast']['script_version']=options['script_version'];stale_cast['body']['cast']['script_sha256']=options['script_sha256'];stale_cast['body']['cast']['characters'][0]['script_character_name']=script['characters'][0]['name']
                reconciled=call(owner,'PUT',cast_path+query,cast_route,stale_cast,aq)
                assert reconciled['number']==2 and reconciled['body']['cast']['characters'][0]['asset_version_id']==asset['version']['id']
                history=call(viewer,'GET',story_path+'/'+reconciled['id']+'/versions'+query,story_route+'/{record_id}/versions')
                assert len(history['items'])==2
                call(owner,'PUT',members,'/api/v1/organizations/{id}/members',dict(email=b['user']['email'],role='remove'),aq)
                call(viewer,'GET',detail+query,detail_route,status=403)
                call(viewer,'GET',first+'/review'+query,first_route+'/review',status=403)
                assert len(calls)==1, 'local decisions resent a paid check'
                print(json.dumps(dict(result='passed',contract_responses=count,synthetic_gateway_calls=len(calls),live_provider_calls=0,checks=['real guarded text protocol','no pricing configuration','strict schemas','CSRF and roles','pinned evidence','current script comparison','explicit source acknowledgement','adoption CAS','original decision replay preserves later edits','intentional misdirection and rejection','explicit cast identities, asset pinning, CAS and original replay','revoked access'])))
            finally:
                process.terminate()
                try:
                    process.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    process.kill();process.wait()
    finally:
        upstream.shutdown();upstream.server_close();thread.join()
if __name__=='__main__':
    main()
