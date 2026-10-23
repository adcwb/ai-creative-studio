#!/usr/bin/env python3
"""Verify sentence cues in actual SRT/VTT/MP4 and local estimate contracts.
Only synthetic media and a loopback server; no credentials or paid upstream.
"""
import copy
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
    warnings.simplefilter("ignore", DeprecationWarning)
    from jsonschema import Draft4Validator, RefResolver

ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = Path(tempfile.mkdtemp(prefix="studio-alignment-evidence-"))
spec = json.loads((ROOT / "api/openapi.json").read_text())
resolver = RefResolver.from_schema(spec)
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    port = sock.getsockname()[1]
BASE = f"http://127.0.0.1:{port}"
QUERY = "?space_id=personal-demo"
process = None
responses = 0
checks = []


def call(path, body=None, key=None, method=None, status=200, schema=None):
    global responses
    headers = {"Content-Type": "application/json"}
    if key:
        headers["Idempotency-Key"] = key
    req = urllib.request.Request(BASE + path, data=None if body is None else json.dumps(body).encode(), headers=headers, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=30)
    except urllib.error.HTTPError as failure:
        response = failure
    with response:
        raw = response.read()
        assert response.status == status, (path, response.status, status, raw[:500])
        value = json.loads(raw) if "application/json" in response.headers.get("Content-Type", "") else raw
    if schema:
        for item in value if isinstance(value, list) else [value]:
            Draft4Validator(spec['components']['schemas'][schema], resolver=resolver).validate(item)
        responses += 1
    return value


def start():
    global process
    env = {"PATH": os.environ['PATH'], "DATA_DIR": str(EVIDENCE / "data"), "HTTP_ADDR": f"127.0.0.1:{port}", "IMAGE_PROVIDER": "mock-svg", "TEXT_PROVIDER": "mock-text", "ASR_PROVIDER": "", "OPENAI_API_KEY": ""}
    process = subprocess.Popen([str(ROOT / 'backend/bin/server')], env=env, stdout=(EVIDENCE / 'server.log').open('ab'), stderr=subprocess.STDOUT)
    for _ in range(100):
        try:
            call('/healthz')
            return
        except OSError:
            time.sleep(.05)
    raise AssertionError('server startup timeout')


def stop():
    if process and process.poll() is None:
        process.kill()
        process.wait(10)


def wait(fn, predicate):
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        value = fn()
        if predicate(value):
            return value
        time.sleep(.05)
    raise AssertionError(('timeout', value))


try:
    start()
    capability = call('/api/v1/alignment-capabilities')
    assert capability['estimate']['ready'] and not capability['asr']['ready']
    project = call('/api/v1/projects' + QUERY, {'title': '合成逐句字幕验收', 'brief': '逐句人工时间与本地估算仅验证字幕编码，不代表语音识别效果。'}, 'alignment-create-project', status=201)
    path = f"/api/v1/projects/{project['id']}/media"
    state = call(path + '/demo' + QUERY, {'expected_version': 1}, 'alignment-demo', schema='MediaState')
    timeline = copy.deepcopy(state['timeline'])
    audio_id = next(a['id'] for a in state['assets'] if a['kind'] == 'audio')
    timeline['clips'][0]['audio_asset_id'] = audio_id
    timeline['clips'][0].update({'subtitle': '旧整镜字幕不应与逐句字幕重复出现', 'subtitle_source': 'manual', 'subtitle_cues': [{'start': .25, 'end': .75, 'text': '第一句精确时间'}, {'start': 1.0, 'end': 1.75, 'text': '第二句精确时间'}]})
    timeline['clips'][1].update({'subtitle_source': 'manual', 'subtitle_cues': [{'start': .5, 'end': 1.5, 'text': '第二镜偏移后应从2.5秒出现'}]})
    save_body = {'expected_version': state['version'], 'timeline': timeline}
    saved = call(path + '/timeline' + QUERY, save_body, 'alignment-save-manual', 'PUT', schema='MediaState')
    assert call(path + '/timeline' + QUERY, save_body, 'alignment-save-manual', 'PUT', schema='MediaState') == saved
    for name, cues in [('overlap', [{'start': .2, 'end': 1.2, 'text': '甲'}, {'start': 1, 'end': 1.6, 'text': '乙'}]), ('range', [{'start': 1, 'end': 3, 'text': '越界'}]), ('empty', [{'start': 0, 'end': 1, 'text': ''}])]:
        invalid = copy.deepcopy(timeline)
        invalid['clips'][0]['subtitle_cues'] = cues
        call(path + '/timeline' + QUERY, {'expected_version': saved['version'], 'timeline': invalid}, 'alignment-invalid-' + name, 'PUT', status=400)
    assert call(path + QUERY)['version'] == saved['version']
    checks.append('manual cues persist exactly; overlap/range/empty cues rejected without mutation')
    exported = call(path + '/exports' + QUERY, {'expected_version': saved['version']}, 'alignment-export', status=202, schema='MediaExport')
    def export_read():
        return next(x for x in call(path + QUERY)['exports'] if x['id'] == exported['id'])
    done = wait(export_read, lambda x: x['status'] in ('succeeded', 'failed'))
    assert done['status'] == 'succeeded', done
    video = EVIDENCE / 'sentence-cues.mp4'
    video.write_bytes(call(done['file_url']))
    srt = call(done['subtitle_url']).decode()
    vtt = call(done['subtitle_vtt_url']).decode()
    (EVIDENCE / 'sentence-cues.srt').write_text(srt)
    (EVIDENCE / 'sentence-cues.vtt').write_text(vtt)
    for expected in ['00:00:00,250 --> 00:00:00,750', '00:00:01,000 --> 00:00:01,750', '00:00:02,500 --> 00:00:03,500']:
        assert expected in srt, srt
        assert expected.replace(',', '.') in vtt, vtt
    assert '旧整镜字幕' not in srt
    embedded = EVIDENCE / 'embedded.srt'
    subprocess.run(['ffmpeg', '-v', 'error', '-i', str(video), '-map', '0:s:0', '-c:s', 'srt', str(embedded)], check=True, timeout=30)
    actual = embedded.read_text()
    for expected in ['00:00:00,250 --> 00:00:00,750', '00:00:01,000 --> 00:00:01,750', '00:00:02,500 --> 00:00:03,500']:
        assert expected in actual, actual
    probe = subprocess.check_output(['ffprobe', '-v', 'error', '-show_format', '-show_streams', '-of', 'json', str(video)])
    (EVIDENCE / 'ffprobe.json').write_bytes(probe)
    assert abs(float(json.loads(probe)['format']['duration']) - 6) < .25
    subprocess.run(['ffmpeg', '-v', 'error', '-i', str(video), '-f', 'null', '-'], check=True, timeout=30)
    checks.append('SRT/VTT and actual MP4 embedded subtitles preserve per-sentence timestamps including second-clip offsets')
    estimate_body = {'clip_id': timeline['clips'][0]['id'], 'mode': 'estimate', 'text': '本地估算第一句。\n本地估算第二句。', 'expected_version': saved['version']}
    task = call(path + '/alignments' + QUERY, estimate_body, 'alignment-estimate', status=202, schema='AlignmentTask')
    same = call(path + '/alignments' + QUERY, estimate_body, 'alignment-estimate', schema='AlignmentTask')
    assert same['id'] == task['id']
    def get_task():
        return next(x for x in call(path + '/alignments' + QUERY, schema='AlignmentTask') if x['id'] == task['id'])
    task = wait(get_task, lambda x: x['status'] in ('succeeded', 'failed'))
    assert task['status'] == 'succeeded' and task['mode'] == 'estimate' and len(task['cues']) == 2, task
    assert task['warnings'], 'estimate must disclose that timing is not ASR'
    assert call(path + QUERY)['timeline'] == saved['timeline'], 'alignment must not automatically mutate timeline'
    changed = dict(estimate_body, text='同键不同文本必须冲突')
    call(path + '/alignments' + QUERY, changed, 'alignment-estimate', status=409)
    call(path + '/alignments' + QUERY, dict(estimate_body, expected_version=1), 'alignment-stale', status=409)
    call(path + '/alignments?space_id=team-demo', status=404)
    call(path + '/alignments' + QUERY, {'clip_id': estimate_body['clip_id'], 'mode': 'asr', 'audio_asset_id': audio_id, 'expected_version': saved['version']}, 'alignment-asr-unavailable', status=503)
    estimated = copy.deepcopy(saved['timeline'])
    estimated['clips'][0].update({'subtitle_cues': task['cues'], 'subtitle_source': 'estimated', 'subtitle_alignment_id': task['id']})
    applied = call(path + '/timeline' + QUERY, {'expected_version': saved['version'], 'timeline': estimated}, 'alignment-apply-estimate', 'PUT', schema='MediaState')
    forged = copy.deepcopy(estimated)
    forged['clips'][0]['subtitle_cues'][0]['text'] = '人工改动不能伪装原始估算来源'
    call(path + '/timeline' + QUERY, {'expected_version': applied['version'], 'timeline': forged}, 'alignment-forged-source', 'PUT', status=400)
    checks.append('estimate is disclosed/persisted/idempotent and never auto-applied; CAS and attribution guards reject stale or forged results; unconfigured ASR refuses')
    stop(); start()
    restored = call(path + QUERY, schema='MediaState')
    assert restored['timeline'] == applied['timeline']
    assert call(path + '/alignments' + QUERY, estimate_body, 'alignment-estimate', schema='AlignmentTask')['id'] == task['id']
    assert call(done['file_url']) == video.read_bytes()
    checks.append('manual/estimated cues, export and alignment receipt survive real process restart')
    evidence = {'checks': checks, 'contract_responses': responses, 'model_validation': False}
    (EVIDENCE / 'acceptance.json').write_text(json.dumps(evidence, ensure_ascii=False, indent=2))
    print(json.dumps(dict(evidence, evidence=str(EVIDENCE)), ensure_ascii=False))
finally:
    stop()
