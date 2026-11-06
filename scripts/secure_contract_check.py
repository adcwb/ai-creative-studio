#!/usr/bin/env python3
"""Validate real secure-mode responses and tenant/CSRF/RBAC boundaries against OpenAPI.
Uses only isolated synthetic accounts, local mock providers and a temporary DATA_DIR.
Run with the openapi-spec-validator environment Python and a Go toolchain on PATH.
"""
import http.cookiejar
import hashlib
import struct
import zlib
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
from jsonschema.exceptions import ValidationError
ROOT = Path(__file__).resolve().parents[1]

def main():
    spec = json.loads((ROOT / 'api/openapi.json').read_text())
    resolver = RefResolver.from_schema(spec)
    consent = dict(model_config_id='synthetic-config-id', model_config_version=1)
    for name, business in [('GatewayCreateTask', dict(prompt='Synthetic gateway request', kind='general')), ('GatewayGenerateScript', dict(expected_version=1)), ('GatewayCreativeJobInput', dict(expected_version=1, kind='image', prompt='Synthetic gateway request'))]:
        validator = OAS30Validator(spec['components']['schemas'][name], resolver=resolver)
        validator.validate({**business, **consent})
        for field in consent:
            invalid = {**business, **consent}
            del invalid[field]
            try:
                validator.validate(invalid)
            except ValidationError:
                pass
            else:
                raise AssertionError((name, field, 'gateway request must require model identity and version'))
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
        def call(opener, method, path, route, body=None, csrf='', status=200, key='secure-contract-operation', binary=None, binary_mime='application/octet-stream'):
            nonlocal count
            headers = {'Content-Type': 'application/json', 'Origin': origin, 'X-CSRF-Token': csrf, 'Idempotency-Key': key}
            if binary is not None:
                headers['Content-Type'] = binary_mime
                headers['X-Chunk-SHA256'] = hashlib.sha256(binary).hexdigest()
            request = urllib.request.Request(origin + path, method=method, headers=headers, data=binary if binary is not None else None if body is None else json.dumps(body).encode())
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
                preview_project = call(alice,'POST','/api/v1/projects'+query,'/api/v1/projects',dict(title='Independent animatic',brief='Local rendering only, no model required.'),aq,status=201,key='secure-preview-project')
                preview_project_path = '/api/v1/projects/' + preview_project['id']
                preview_shot = dict(id='preview-one',scene_index=0,description='Saved independent shot',shot_size='medium',camera_movement='static',duration_seconds=1,dialogue='PRIVATE_PREVIEW_DIALOGUE',sound='PRIVATE_PREVIEW_SOUND')
                preview_project = call(alice,'PUT',preview_project_path+'/storyboard'+query,'/api/v1/projects/{id}/storyboard',dict(expected_version=preview_project['version'],shots=[preview_shot]),aq,key='secure-preview-board')
                def preview_chunk(tag,data):
                    payload=tag+data
                    return struct.pack('>I',len(data))+payload+struct.pack('>I',zlib.crc32(payload)&0xffffffff)
                preview_png=b'\x89PNG\r\n\x1a\n'+preview_chunk(b'IHDR',struct.pack('>IIBBBBB',32,32,8,2,0,0,0))+preview_chunk(b'IDAT',zlib.compress((b'\x00'+b'\x33\x77\x55'*32)*32))+preview_chunk(b'IEND',b'')
                preview_upload=call(alice,'POST',preview_project_path+'/media/uploads'+query+'&filename=frame.png&role=shot&shot_id=preview-one','/api/v1/projects/{id}/media/uploads',csrf=aq,key='secure-preview-upload',binary=preview_png,binary_mime='image/png')
                animatic_path=preview_project_path+'/media/previews'
                animatic_route='/api/v1/projects/{id}/media/previews'
                preview_options=call(alice,'GET',animatic_path+'/options'+query,animatic_route+'/options')
                assert 'script' not in preview_options['project'] and 'exports' not in preview_options['media'] and preview_options['shot_episodes']['preview-one']==0
                call(bob,'GET',animatic_path+'/options'+query,animatic_route+'/options')
                preview_body=dict(expected_project_version=preview_project['version'],expected_media_version=preview_upload['version'],episode_number=1,aspect_ratio='16:9',sound_mode='silent',shots=[dict(shot_id='preview-one',asset_id=preview_upload['uploaded_asset_id'],duration=.5)])
                call(anonymous,'POST',animatic_path+query,animatic_route,preview_body,status=401,key='preview-anonymous')
                call(alice,'POST',animatic_path+query,animatic_route,preview_body,status=403,key='preview-csrf')
                call(bob,'POST',animatic_path+query,animatic_route,preview_body,bq,status=403,key='preview-viewer')
                call(alice,'POST',animatic_path+query,animatic_route,dict(preview_body,preview_source={}),aq,status=400,key='preview-forged-source')
                call(alice,'POST',animatic_path+query,animatic_route,dict(preview_body,expected_project_version=0),aq,status=400,key='preview-invalid-version')
                preview_export=call(alice,'POST',animatic_path+query,animatic_route,preview_body,aq,status=202,key='preview-original')
                assert preview_export['purpose']=='animatic' and preview_export['timeline']['subtitle_mode']=='burn' and 'ANIMATIC' in preview_export['timeline']['clips'][0]['subtitle']
                original_preview_source=preview_export['preview_source']
                preview_detail_path=animatic_path+'/'+preview_export['id']
                preview_detail_route=animatic_route+'/{preview_id}'
                assert call(bob,'GET',preview_detail_path+query,preview_detail_route)['preview_source']==original_preview_source
                call(bob,'GET',preview_detail_path+'?space_id='+other,preview_detail_route,status=404)
                call(anonymous,'GET',preview_detail_path+query,preview_detail_route,status=401)
                for action in ['cancel','resume']:
                    call(bob,'POST',preview_detail_path+'/'+action+query,preview_detail_route+'/'+action,{},bq,status=403,key='preview-viewer-'+action)
                    call(alice,'POST',preview_detail_path+'/'+action+query,preview_detail_route+'/'+action,{},status=403,key='preview-csrf-'+action)
                changed_preview_shot=dict(preview_shot,description='Later current plan')
                later_preview_project=call(alice,'PUT',preview_project_path+'/storyboard'+query,'/api/v1/projects/{id}/storyboard',dict(expected_version=preview_project['version'],shots=[changed_preview_shot]),aq,key='preview-later-plan')
                replay_preview=call(alice,'POST',animatic_path+query,animatic_route,preview_body,aq,status=202,key='preview-original')
                assert replay_preview['id']==preview_export['id'] and replay_preview['preview_source']==original_preview_source
                call(alice,'POST',animatic_path+query,animatic_route,preview_body,aq,status=409,key='preview-stale-source')
                for _ in range(100):
                    preview_done=call(alice,'GET',preview_detail_path+query,preview_detail_route)
                    if preview_done['status'] in ['succeeded','failed']:
                        break
                    time.sleep(.1)
                assert preview_done['status']=='succeeded',preview_done
                preview_page=call(alice,'GET',animatic_path+query+'&limit=1',animatic_route)
                assert len(preview_page['items'])==1 and 'preview_source' not in preview_page['items'][0] and 'PRIVATE_PREVIEW' not in json.dumps(preview_page)
                preview_media=call(alice,'GET',preview_project_path+'/media'+query,'/api/v1/projects/{id}/media')
                assert preview_media['version']==preview_upload['version'] and preview_media['timeline']['clips']==[] and not preview_media.get('selections')
                denied_delivery=call(alice,'POST',preview_project_path+'/social/jobs'+query,'/api/v1/projects/{id}/social/jobs',dict(expected_version=preview_media['version'],platform='tiktok',mode='export',export_id=preview_export['id'],confirmed=True),aq,status=400,key='preview-not-publishable')
                assert '预演' in denied_delivery['error']['message']
                library_route = '/api/v1/library/assets'
                asset = call(alice, 'POST', library_route + query, library_route, dict(kind='style',card=dict(name='Synthetic style',description='',prohibited_changes=[],style={}),references=[]), aq, status=201, key='secure-library-authorization-card')
                use_path = library_route + '/' + asset['asset']['id'] + '/use-authorization'
                use_route = library_route + '/{assetId}/use-authorization'
                assert call(alice,'GET',use_path+query,use_route)['authorization'] is None
                declaration = dict(expected_revision=0,status='granted',version_ids=[asset['version']['id']],purposes=['image_reference'],rights_holder='Synthetic creator',basis='own_work',evidence='Local original fixture',attested=True)
                call(anonymous,'GET',use_path+query,use_route,status=401)
                call(bob,'GET',use_path+'?space_id='+other,use_route,status=404)
                call(alice,'POST',use_path+query,use_route,declaration,status=403,key='secure-authorization-csrf')
                call(bob,'POST',use_path+query,use_route,declaration,bq,status=403,key='secure-authorization-viewer')
                forged={**declaration,'updated_by':'forged'}
                call(alice,'POST',use_path+query,use_route,forged,aq,status=400,key='secure-authorization-forgery')
                accepted=call(alice,'POST',use_path+query,use_route,declaration,aq,key='secure-authorization-grant')
                assert accepted['authorization']['updated_by']==a['user']['id'] and accepted['authorization']['declared_by']==a['user']['id']
                call(bob,'GET',use_path+query,use_route)
                revoked=call(alice,'POST',use_path+query,use_route,dict(expected_revision=1,status='revoked',reason='Synthetic withdrawal'),aq,key='secure-authorization-revoke')
                assert revoked['authorization']['revision']==2
                assert call(alice,'POST',use_path+query,use_route,declaration,aq,key='secure-authorization-grant')==accepted
                assert call(alice,'GET',use_path+query,use_route)['authorization']['status']=='revoked'

                structure_project = call(alice, 'POST', '/api/v1/projects' + query, '/api/v1/projects', {'title': 'Stable identity scope', 'brief': 'An explicit metadata-only identity upgrade.'}, aq, status=201, key='secure-structure-project')
                director_job_path = '/api/v1/projects/' + structure_project['id'] + '/creative-jobs'
                director_job_route = '/api/v1/projects/{id}/creative-jobs'
                director_job_body = dict(kind='image', expected_version=structure_project['version'], prompt='Synthetic detail permission fixture')
                director_job = call(alice,'POST',director_job_path+query,director_job_route,director_job_body,aq,status=202,key='secure-director-job')
                director_detail = director_job_path + '/' + director_job['id']
                director_detail_route = director_job_route + '/{jobId}'
                assert call(alice,'GET',director_detail+query,director_detail_route)['id'] == director_job['id']
                assert 'runtime_selection' not in call(bob,'GET',director_detail+query,director_detail_route)
                call(anonymous,'GET',director_detail+query,director_detail_route,status=401)
                call(bob,'GET',director_detail+'?space_id='+other,director_detail_route,status=404)
                call(alice,'POST',director_job_path+query,director_job_route,director_job_body,status=403,key='secure-director-csrf')
                call(bob,'POST',director_job_path+query,director_job_route,dict(director_job_body,use_director_context=True),bq,status=403,key='secure-director-viewer')
                call(alice,'POST',director_job_path+query,director_job_route,dict(director_job_body,director_context=dict(current={})),aq,status=400,key='secure-director-forged-source')
                structure_path = '/api/v1/projects/' + structure_project['id'] + '/structure-upgrades'
                structure_route = '/api/v1/projects/{id}/structure-upgrades'
                structure_body = {'expected_version': structure_project['version']}
                call(alice, 'POST', structure_path + query, structure_route, structure_body, status=403, key='secure-structure-no-csrf')
                call(bob, 'POST', structure_path + query, structure_route, structure_body, bq, status=403, key='secure-structure-viewer')
                upgraded = call(alice, 'POST', structure_path + query, structure_route, structure_body, aq, key='secure-structure-upgrade')
                assert upgraded['structure_version'] == 2 and 'structure_mappings' not in upgraded
                migration_source = call(alice, 'POST', '/api/v1/projects' + query, '/api/v1/projects', {'title': 'Migration access scope', 'brief': 'A source episode retained after an explicit owned copy.'}, aq, status=201, key='migration-secure-source')
                source_path = '/api/v1/projects/' + migration_source['id']
                project_route = '/api/v1/projects/{id}'
                migration_source = call(alice, 'PUT', source_path + '/script' + query, project_route + '/script', {'expected_version': migration_source['version'], 'script': {'title': 'Original episode', 'logline': 'A traveller reaches the river.', 'characters': [], 'scenes': [{'episode_number': 7, 'heading': 'River', 'action': 'A traveller arrives.', 'dialogue': []}]}}, aq, key='migration-secure-script')
                root_series = call(alice, 'POST', '/api/v1/series' + query, '/api/v1/series', {'title': 'Migration destination', 'summary': 'Independent episodes retain their owned source.'}, aq, status=201, key='migration-secure-series')
                season = call(alice, 'POST', '/api/v1/series/' + root_series['id'] + '/seasons' + query, '/api/v1/series/{id}/seasons', {}, aq, status=201, key='migration-secure-season')
                summary = call(alice, 'GET', source_path + '/migration-source' + query, project_route + '/migration-source')
                migration_input = {'source_episode_number': 7, 'target_season_id': season['id'], 'target_ordinal': 7, 'expected_project_version': migration_source['version'], 'expected_media_version': summary['media_version'], 'expected_season_version': season['row_version'], 'output_mode': 'series', 'storyboard_policy': 'include', 'shot_assignments': {}, 'character_assignments': {}}
                preview_path, preview_route = source_path + '/episode-migration-preview', project_route + '/episode-migration-preview'
                call(anonymous, 'GET', source_path + '/migration-source' + query, project_route + '/migration-source', status=401)
                call(alice, 'POST', preview_path + query, preview_route, migration_input, status=403, key='migration-secure-no-csrf')
                call(bob, 'POST', preview_path + query, preview_route, migration_input, bq, status=403, key='migration-secure-viewer')
                preview = call(alice, 'POST', preview_path + query, preview_route, migration_input, aq, key='migration-secure-preview')
                jobs_path, jobs_route = source_path + '/episode-migrations', project_route + '/episode-migrations'
                create_migration = {'input': migration_input, 'preview_digest': preview['digest']}
                call(bob, 'POST', jobs_path + query, jobs_route, create_migration, bq, status=403, key='migration-secure-viewer-create')
                migration = call(alice, 'POST', jobs_path + query, jobs_route, create_migration, aq, status=201, key='migration-secure-create')
                task_path, task_route = jobs_path + '/' + migration['id'], jobs_route + '/{migration_id}'
                call(bob, 'POST', task_path + '/continue' + query, task_route + '/continue', {'expected_version': migration['row_version']}, bq, status=403, key='migration-secure-viewer-step')
                migration = call(alice, 'POST', task_path + '/continue' + query, task_route + '/continue', {'expected_version': migration['row_version']}, aq, key='migration-secure-reserve')
                call(bob, 'GET', '/api/v1/projects/' + migration['target_project_id'] + query, project_route, status=404)
                migration = call(alice, 'POST', task_path + '/continue' + query, task_route + '/continue', {'expected_version': migration['row_version']}, aq, key='migration-secure-complete')
                assert migration['state'] == 'completed'
                copied = call(bob, 'GET', '/api/v1/projects/' + migration['target_project_id'] + query, project_route)
                assert copied['migration_source']['episode_number'] == 7
                assert call(alice, 'GET', source_path + query, project_route) == migration_source
                # Canonical authored material is separate from AI suggestions.
                story_path = '/api/v1/series/' + root_series['id'] + '/story-records'
                story_route = '/api/v1/series/{id}/story-records'
                canonical = {'expected_version': 0, 'title': 'Synthetic canonical story bible', 'kind': 'bible', 'status': 'active', 'required': True, 'body': {'evidence': [], 'bible': {'theme': 'Searching for truth', 'world_rules': 'Characters only know what has been revealed to them.', 'story_arc': '', 'visual_intent': '', 'immutable_rules': '', 'characters': [{'id': '2' * 32, 'name': 'Xiaoman', 'desire': 'Find the truth', 'flaw': '', 'arc': '', 'relationships': '', 'visual_intent': '', 'voice_intent': 'Descriptive prose only; no voice authorization.'}]}}}
                call(anonymous, 'GET', story_path + query, story_route, status=401)
                call(alice, 'POST', story_path + query, story_route, canonical, status=403, key='secure-story-no-csrf')
                call(bob, 'POST', story_path + query, story_route, canonical, bq, status=403, key='secure-story-viewer')
                first_story = call(alice, 'POST', story_path + query, story_route, canonical, aq, status=201, key='secure-story-create')
                page = call(bob, 'GET', story_path + query, story_route)
                assert len(page['items']) == 1 and 'body' not in page['items'][0]
                current_story_path, current_story_route = story_path + '/' + first_story['id'], story_route + '/{record_id}'
                update_story = json.loads(json.dumps(canonical))
                update_story['expected_version'] = 1
                update_story['body']['bible']['world_rules'] = 'A new canonical rule with the original version retained.'
                latest_story = call(alice, 'PUT', current_story_path + query, current_story_route, update_story, aq, key='secure-story-update')
                assert latest_story['number'] == 2
                call(alice, 'PUT', current_story_path + query, current_story_route, update_story, aq, status=409, key='secure-story-stale')
                assert call(alice, 'POST', story_path + query, story_route, canonical, aq, status=201, key='secure-story-create') == first_story
                historical = call(bob, 'GET', current_story_path + '/versions/' + first_story['version_id'] + query, current_story_route + '/versions/{version_id}')
                assert historical == first_story
                call(bob, 'GET', current_story_path + '/versions' + query + '&limit=1', current_story_route + '/versions')
                call(alice, 'GET', '/api/v1/series/' + root_series['id'] + '/story-episodes' + query, '/api/v1/series/{id}/story-episodes')
                context_path, context_route = '/api/v1/episodes/' + migration['target_project_id'] + '/context-previews', '/api/v1/episodes/{id}/context-previews'
                call(bob, 'POST', context_path + query, context_route, {}, bq, status=403, key='secure-story-context-viewer')
                context_preview = call(alice, 'POST', context_path + query, context_route, {}, aq, key='secure-story-context')
                assert context_preview['problems'] == [] and context_preview['included'][0]['number'] == 2
                call(alice, 'POST', context_path + query, context_route, {'actor_id': 'owner'}, aq, status=400, key='secure-story-forged-actor')
                snapshots_path = '/api/v1/episodes/' + migration['target_project_id'] + '/context-snapshots'
                snapshots_route = '/api/v1/episodes/{id}/context-snapshots'
                selection = {'query': 'canonical', 'record_ids': []}
                call(anonymous, 'GET', snapshots_path + query, snapshots_route, status=401)
                call(alice, 'POST', snapshots_path + query, snapshots_route, selection, status=403, key='context-no-csrf')
                call(bob, 'POST', snapshots_path + query, snapshots_route, selection, bq, status=403, key='context-viewer')
                pinned_context = call(alice, 'POST', snapshots_path + query, snapshots_route, selection, aq, status=201, key='context-original')
                assert pinned_context['context']['included'][0]['version_id'] == latest_story['version_id']
                assert call(alice, 'POST', snapshots_path + query, snapshots_route, selection, aq, status=201, key='context-original') == pinned_context
                call(alice, 'POST', snapshots_path + query, snapshots_route, {'query': 'changed'}, aq, status=409, key='context-original')
                pinned_path, pinned_route = snapshots_path + '/' + pinned_context['id'], snapshots_route + '/{snapshot_id}'
                assert call(bob, 'GET', pinned_path + query, pinned_route) == pinned_context
                fresh_route = pinned_route + '/freshness'
                freshness = call(bob, 'GET', pinned_path + '/freshness' + query, fresh_route)
                assert freshness['current'] and not freshness['material_changed']
                contexts = call(bob, 'GET', snapshots_path + query, snapshots_route)
                assert len(contexts['items']) == 1 and 'context' not in contexts['items'][0] and 'script' not in contexts['items'][0]
                call(alice, 'POST', snapshots_path + query, snapshots_route, {'actor_id': 'forged'}, aq, status=400, key='context-forged')
                empty_episode = call(alice, 'POST', '/api/v1/seasons/' + season['id'] + '/episodes' + query, '/api/v1/seasons/{id}/episodes', {'title': 'Context before a first script'}, aq, status=201, key='empty-context-episode')
                empty_path = '/api/v1/episodes/' + empty_episode['id'] + '/context-snapshots'
                empty_snapshot = call(alice, 'POST', empty_path + query, snapshots_route, {}, aq, status=201, key='empty-context-snapshot')
                assert empty_snapshot['script'] is None and any(p['code'] == 'script_required' for p in empty_snapshot['context']['problems'])
                moved_directory = call(bob, 'GET', pinned_path + '/freshness' + query, fresh_route)
                assert moved_directory['directory_changed'] and not moved_directory['current']
                call(bob, 'PUT', budget + query, budget, {'currency': 'USD', 'limit_minor': 301, 'expected_limit_minor': 300}, bq, status=403)
                call(alice, 'PUT', member, member_route, {'email': b['user']['email'], 'role': 'remove'}, aq)
                call(bob,'GET',director_detail+query,director_detail_route,status=403)
                call(bob,'GET',preview_detail_path+query,preview_detail_route,status=403)
                call(bob,'GET',animatic_path+'/options'+query,animatic_route+'/options',status=403)
                call(bob,'GET',preview_done['file_url'],'/api/v1/projects/{id}/media/exports/{export_id}/file',status=403)
                call(bob, 'GET', task_path + query, task_route, status=403)
                call(bob, 'GET', pinned_path + '/freshness' + query, fresh_route, status=403)
                call(bob, 'GET', pinned_path + query, pinned_route, status=403)
                call(bob, 'GET', snapshots_path + query, snapshots_route, status=403)
                call(bob, 'GET', story_path + query, story_route, status=403)
                call(bob, 'GET', governance + query, governance, status=403)
                assert call(bob, 'GET', governance + '?space_id=' + other, governance)['limit_minor'] == 0
                exchange = governance + '/exchange'
                call(alice, 'GET', exchange + query + '&from=USD&to=CNY', exchange, status=503)
                call(alice, 'GET', exchange + query + '&from=USD&to=USD', exchange)
                model_route = '/api/v1/model-config'
                model = call(alice, 'GET', model_route + query, model_route)
                assert model['secret_storage_available'] is True and model['live_enabled'] is False and model['runtime_applied'] is False
                call(bob, 'GET', model_route + query, model_route, status=403)
                declaration = {'expected_version': model['version'], 'entry': {'provider': 'openai-text', 'model': 'synthetic-model-declaration', 'capability': 'text', 'region': 'global', 'enabled': False, 'is_default': False, 'lifecycle': 'active'}, 'credential': {'action': 'keep'}}
                call(alice, 'PUT', model_route + query, model_route, declaration, aq, status=410)
                assert call(alice, 'GET', model_route + query, model_route)['entries'] == []
                validate_route = model_route + '/validate'
                call(alice, 'POST', validate_route + query, validate_route, {'id': 'missing'}, aq, status=404)
                call(alice, 'POST', model_route + '/revoke' + query, model_route + '/revoke', {'id':'missing','expected_version':0}, aq, status=404)
                call(alice, 'POST', model_route + '/live' + query, model_route + '/live', {}, aq, status=403)
                management = '/api/v1/model-management'
                gateways = '/api/v1/ai-gateways'
                gateway_models = '/api/v1/gateway-models'
                call(bob, 'GET', management + query, management, status=403)
                assert call(alice, 'GET', management + query, management)['gateways'] == []
                gateway_payload = {'expected_version': 0, 'gateway': {'name': 'Synthetic contract gateway', 'base_url': 'https://gateway.example.invalid/v1', 'enabled': True, 'timeout_seconds': 30}, 'credential': {'action': 'keep'}}
                call(alice, 'POST', gateways + query, gateways, gateway_payload, status=403)
                # Product routes must never use the legacy mock providers when a selection is absent.
                for route, target, payload in [
                    ('/api/v1/gateway-tasks', '/api/v1/gateway-tasks', dict(prompt='Synthetic no fallback', kind='general')),
                    ('/api/v1/gateway-tasks/{id}/retry', '/api/v1/gateway-tasks/missing/retry', dict(prompt='Synthetic no fallback', kind='general')),
                    ('/api/v1/projects/{id}/gateway-generations', '/api/v1/projects/missing/gateway-generations', dict(expected_version=1)),
                    ('/api/v1/projects/{id}/gateway-storyboard-generations', '/api/v1/projects/missing/gateway-storyboard-generations', dict(expected_version=1)),
                    ('/api/v1/projects/{id}/gateway-analysis', '/api/v1/projects/missing/gateway-analysis', dict(expected_version=1)),
                    ('/api/v1/projects/{id}/gateway-creative-jobs', '/api/v1/projects/missing/gateway-creative-jobs', dict(expected_version=1, kind='image', prompt='Synthetic no fallback')),
                ]:
                    denied = call(alice, 'POST', target + query, route, payload, aq, status=409)
                    assert denied['error']['code'] == 'gateway_model_required'
                strict = '/api/v1/gateway-tasks'
                call(anonymous, 'POST', strict + query, strict, dict(prompt='Synthetic no fallback', kind='general'), status=401)
                call(alice, 'POST', strict + query, strict, dict(prompt='Synthetic no fallback', kind='general'), status=403)
                configured = call(alice, 'POST', gateways + query, gateways, gateway_payload, aq)
                gateway = configured['gateways'][0]
                assert gateway['secret_status'] == 'not_configured'
                entry = {'gateway_id': gateway['id'], 'name': 'Synthetic model', 'model': 'vendor/synthetic', 'capabilities': ['text'], 'enabled': True, 'max_output_tokens': 128, 'json_output': True, 'currency': 'USD', 'maximum_minor': 10, 'price_source': 'Synthetic test bound'}
                model_payload = {'expected_version': 0, 'model': entry}
                call(bob, 'POST', gateway_models + '?space_id=' + other, gateway_models, model_payload, bq, status=400)
                configured = call(alice, 'POST', gateway_models + query, gateway_models, model_payload, aq)
                gateway_model = configured['models'][0]
                feature = '/api/v1/model-features/script_generation'
                feature_route = '/api/v1/model-features/{feature_key}'
                binding = {'expected_version': 0, 'binding': {'feature_key': 'script_generation', 'model_config_id': gateway_model['id'], 'enabled': True, 'allow_override': False}}
                call(alice, 'PUT', feature + query, feature_route, binding, aq)
                call(alice, 'PUT', feature + query, feature_route, binding, aq, status=409)
                call(alice, 'PUT', member, member_route, {'email': b['user']['email'], 'role': 'viewer'}, aq)
                call(bob, 'GET', management + query, management, status=403)
                call(bob, 'POST', gateways + query, gateways, gateway_payload, bq, status=403)
                catalog = call(bob, 'GET', '/api/v1/runtime-models' + query + '&feature_key=script_generation', '/api/v1/runtime-models')
                assert any(item['id'] == gateway_model['id'] and not item['available'] for item in catalog['models'])
                wire = json.dumps(catalog)
                assert 'base_url' not in wire and 'ciphertext' not in wire and 'secret_status' not in wire
                for usage in ['/api/v1/model-usage', '/api/v1/model-usage/calls']:
                    assert call(bob, 'GET', usage + query, usage)['total'] == 0
                call(alice, 'PUT', member, member_route, {'email': b['user']['email'], 'role': 'remove'}, aq)
                call(bob, 'GET', '/api/v1/model-usage' + query, '/api/v1/model-usage', status=403)
                # Personal recipes use immutable revisions without generating tasks.
                prompts = '/api/v1/prompt-templates'
                metadata = dict(name='Garden %_', description='literal search', category='Garden', tags=['botanical'], visibility='private')
                recipe = dict(syntax='plain', body='Literal {{braces}} in a soft garden 花朵', defaults=dict(kind='poster', ratio='4:5', style='natural'), notes='first')
                draft = dict(**metadata, version=recipe)
                call(alice, 'POST', prompts + query, prompts, draft, status=403)
                saved = call(alice, 'POST', prompts + query, prompts, draft, aq, status=201)
                target = prompts + '/' + saved['template']['id']
                source_task = call(alice, 'POST', '/api/v1/tasks' + query, '/api/v1/tasks', dict(prompt=recipe['body'], kind='poster', ratio='4:5', style='natural', prompt_source=dict(template_id=saved['template']['id'], version_id=saved['version']['id'], edited=False)), aq, status=202)
                assert source_task['prompt_source']['version_id'] == saved['version']['id'] and source_task['prompt_source']['edited'] is False
                route = prompts + '/{templateId}'
                assert call(alice, 'POST', prompts + query, prompts, draft, aq, status=201)['version']['id'] == saved['version']['id']
                preview = call(alice, 'POST', target + '/preview' + query, route + '/preview', dict(version_id=saved['version']['id']), aq)
                assert preview['prompt'] == recipe['body']
                call(alice, 'GET', target + '/versions/' + saved['version']['id'] + query, route + '/versions/{versionId}')
                call(alice, 'GET', target + '/versions' + query, route + '/versions')
                call(alice, 'PUT', target + '/favorite' + query, route + '/favorite', dict(enabled=True), aq)
                assert len(call(alice, 'GET', prompts + query + '&q=%25_&favorite=true', prompts)['items']) == 1
                call(bob, 'GET', target + '?space_id=' + other, route, status=404)
                call(alice, 'PUT', member, member_route, {'email': b['user']['email'], 'role': 'editor'}, aq)
                call(bob, 'GET', target + query, route, status=404)
                metadata['visibility'] = 'organization'
                shared = call(alice, 'PATCH', target + query, route, dict(**metadata, expected_row_version=saved['template']['row_version'], status='active'), aq)
                call(bob, 'GET', target + '/versions/' + saved['version']['id'] + query, route + '/versions/{versionId}')
                recipe['body'] = 'Organization editor revision 花朵'
                updated = call(bob, 'POST', target + '/versions' + query, route + '/versions', dict(expected_head_version_id=shared['version']['id'], version=recipe), bq, status=201)
                # This is a different body under Bob's same operation key.
                call(bob, 'POST', target + '/versions' + query, route + '/versions', dict(expected_head_version_id=updated['version']['id'], version=recipe), bq, status=409)
                call(alice, 'PUT', member, member_route, {'email': b['user']['email'], 'role': 'remove'}, aq)
                call(bob, 'GET', target + query, route, status=403)
                call(bob, 'POST', target + '/versions' + query, route + '/versions', dict(expected_head_version_id=shared['version']['id'], version=recipe), bq, status=403)
                variable_recipe = dict(syntax='variables_v1', body='{{subject}} under {{palette}} light', defaults=dict(kind='poster', ratio='4:5', style='natural'), notes='variable contract', variables=[dict(name='subject', label='Subject', type='text', required=True, max_length=100), dict(name='palette', label='Palette', type='enum', required=False, default='warm', options=['warm','cool'], max_length=20)])
                variable_saved = call(alice, 'POST', prompts + query, prompts, dict(name='Variable recipe', visibility='private', version=variable_recipe), aq, status=201, key='secure-variable-operation')
                variable_target = prompts + '/' + variable_saved['template']['id']
                missing = call(alice, 'POST', variable_target + '/preview' + query, route + '/preview', dict(version_id=variable_saved['version']['id']), aq, status=400)
                assert missing['error']['field'] == 'subject'
                expanded = call(alice, 'POST', variable_target + '/preview' + query, route + '/preview', dict(version_id=variable_saved['version']['id'], variables=dict(subject='White cat')), aq)
                assert expanded['prompt'] == 'White cat under warm light'
                variable_task = call(alice, 'POST', '/api/v1/tasks' + query, '/api/v1/tasks', dict(prompt=expanded['prompt'], kind='poster', ratio='4:5', style='natural', prompt_source=dict(template_id=variable_saved['template']['id'], version_id=variable_saved['version']['id'], variables=dict(subject='White cat'), edited=False)), aq, status=202, key='secure-variable-image')
                assert variable_task['prompt_source']['variables'] == {'subject':'White cat'}
                project = call(alice, 'POST', '/api/v1/projects' + query, '/api/v1/projects', dict(title='Prompt application contract', brief='Synthetic recipe application without a model call.'), aq, status=201, key='secure-prompt-project')
                applications = '/api/v1/projects/' + project['id'] + '/prompt-applications'
                application_route = '/api/v1/projects/{id}/prompt-applications'
                application_input = dict(template_id=variable_saved['template']['id'], version_id=variable_saved['version']['id'], variables=dict(subject='White cat'), expected_media_version=1)
                application = call(alice, 'POST', applications + query, application_route, application_input, aq, status=201, key='secure-prompt-application')
                assert application['prompt'] == 'White cat under warm light' and application['reference_asset_ids'] == []
                replayed = call(alice, 'POST', applications + query, application_route, application_input, aq, status=200, key='secure-prompt-application')
                assert replayed['id'] == application['id']
                call(alice, 'GET', applications + '/' + application['id'] + query, application_route + '/{applicationId}')
                call(bob, 'GET', applications + '/' + application['id'] + query, application_route + '/{applicationId}', status=403)
                call(alice, 'GET', '/api/v1/projects/' + project['id'] + '/media' + query, '/api/v1/projects/{id}/media')
                uploads = '/api/v1/projects/' + project['id'] + '/media/upload-sessions'
                upload_route = '/api/v1/projects/{id}/media/upload-sessions'
                def png_chunk(kind, payload):
                    return struct.pack('>I', len(payload)) + kind + payload + struct.pack('>I', zlib.crc32(kind + payload) & 0xffffffff)
                png = b'\x89PNG\r\n\x1a\n' + png_chunk(b'IHDR', struct.pack('>IIBBBBB', 1, 1, 8, 6, 0, 0, 0)) + png_chunk(b'IDAT', zlib.compress(b'\x00\x20\x50\x30\xff')) + png_chunk(b'IEND', b'')
                transfer_input = dict(name='synthetic.png', mime_type='image/png', size_bytes=len(png), sha256=hashlib.sha256(png).hexdigest(), role='reference', character='', shot_id='')
                transfer = call(alice, 'POST', uploads + query, upload_route, transfer_input, aq, status=201, key='secure-upload-create')
                assert call(alice, 'POST', uploads + query, upload_route, transfer_input, aq, status=201, key='secure-upload-create')['id'] == transfer['id']
                upload_target = uploads + '/' + transfer['id']
                call(alice, 'GET', upload_target + query, upload_route + '/{upload_id}')
                call(bob, 'GET', upload_target + query, upload_route + '/{upload_id}', status=403)
                call(alice, 'GET', uploads + query, upload_route)
                call(alice, 'POST', upload_target + '/complete' + query, upload_route + '/{upload_id}/complete', {}, aq, status=409)
                call(alice, 'PUT', upload_target + '/parts/0' + query, upload_route + '/{upload_id}/parts/{offset}', csrf=aq, binary=png)
                call(alice, 'PUT', upload_target + '/parts/0' + query, upload_route + '/{upload_id}/parts/{offset}', csrf=aq, binary=png)
                completed = call(alice, 'POST', upload_target + '/complete' + query, upload_route + '/{upload_id}/complete', {}, aq)
                assert completed['state'] == 'completed' and completed['uploaded_asset_id']
                lineage_route='/api/v1/projects/{id}/media/assets/{assetId}/lineage'
                lineage_path='/api/v1/projects/'+project['id']+'/media/assets/'+completed['uploaded_asset_id']+'/lineage'
                lineage=call(alice,'GET',lineage_path+query,lineage_route)
                assert lineage['status']=='recorded' and lineage['library_sources']==[] and lineage['content_sha256']==hashlib.sha256(png).hexdigest()
                call(anonymous,'GET',lineage_path+query,lineage_route,status=401)
                call(bob,'GET',lineage_path+query,lineage_route,status=403)
                call(alice,'GET',lineage_path+'?space_id='+other,lineage_route,status=403)

                assert call(alice, 'POST', upload_target + '/complete' + query, upload_route + '/{upload_id}/complete', {}, aq)['uploaded_asset_id'] == completed['uploaded_asset_id']
                cancelled = call(alice, 'POST', uploads + query, upload_route, transfer_input, aq, status=201, key='secure-upload-cancel-create')
                call(alice, 'POST', uploads + '/' + cancelled['id'] + '/cancel' + query, upload_route + '/{upload_id}/cancel', {}, aq)
                call(alice, 'POST', variable_target + '/copies' + query, route + '/copies', dict(version_id=variable_saved['version']['id'], name='Copied recipe', visibility='private'), aq, status=201)
                call(alice, 'POST', '/api/v1/auth/logout', '/api/v1/auth/logout', {}, aq)
                assert call(alice, 'GET', session_route, session_route)['authenticated'] is False
                print(json.dumps({'result': 'passed', 'contract_responses': count, 'checks': ['secure sessions', 'CSRF', 'tenant isolation', 'owner-only budget', 'CAS', 'currency separation', 'revocation', 'missing FX fail closed', 'credential-free model declarations', 'local validation only', 'live endpoint refused', 'gateway admin and viewer boundaries', 'gateway model ownership', 'feature binding CAS', 'sanitized runtime catalog', 'usage membership revocation', 'private prompt revisions, sharing, literal search and revoked replay', 'library use declarations: CSRF, viewer, forged actor, original grant receipt and current revocation', 'read-only media lineage, exact uploaded bytes and current membership boundaries', 'explicit stable structure upgrade, CSRF and viewer refusal', 'episode copy preview, hidden target, viewer/CSRF refusal and revoked progress access', 'canonical story versions, immutable retrieval snapshots, current-source comparisons, original receipts, null-script safeguards and revoked access', 'on-demand director detail: current membership, viewer reads, forbidden writes, CSRF, source forgery and revoked access', 'independent animatic: source options, byte-pinned render, original receipt after plan changes, silent watermark, unchanged edit, publishing refusal and revoked downloads'], 'live_provider_calls': 0}))
            finally:
                process.terminate()
                process.wait(timeout=15)

if __name__ == '__main__':
    main()
