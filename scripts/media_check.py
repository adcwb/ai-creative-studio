#!/usr/bin/env python3
"""Local synthetic media acceptance; never configures or calls an AI provider."""
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
EVIDENCE = Path(tempfile.mkdtemp(prefix="studio-media-evidence-"))
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    PORT = sock.getsockname()[1]
BASE = f"http://127.0.0.1:{PORT}"
QUERY = "?space_id=personal-demo"
checks = []
spec = json.loads((ROOT / "api/openapi.json").read_text())
resolver = RefResolver.from_schema(spec)
contract_count = 0
process = None


def request(path, body=None, key=None, method=None, status=200, mime="application/json"):
    headers = {"Content-Type": mime}
    if key:
        headers["Idempotency-Key"] = key
    data = body if isinstance(body, bytes) else json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE + path, data=data, headers=headers, method=method)
    try:
        response = urllib.request.urlopen(req, timeout=45)
    except urllib.error.HTTPError as exc:
        response = exc
    with response:
        payload = response.read()
        assert response.status == status, (path, response.status, status, payload[:500])
        value = json.loads(payload) if "application/json" in response.headers.get("Content-Type", "") else payload
        if isinstance(value, dict):
            global contract_count
            schema_name = "MediaState" if "assets" in value and "timeline" in value else "MediaExport" if "timeline" in value and "status" in value else None
            if schema_name:
                Draft4Validator(spec["components"]["schemas"][schema_name], resolver=resolver).validate(value)
                contract_count += 1
        return value


def wait(fn, predicate, seconds=45):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        value = fn()
        if predicate(value):
            return value
        time.sleep(.08)
    raise AssertionError(f"Timed out: {value}")


def start():
    global process
    env = {"PATH": os.environ["PATH"], "DATA_DIR": str(EVIDENCE / "data"), "HTTP_ADDR": f"127.0.0.1:{PORT}", "IMAGE_PROVIDER": "mock-svg", "TEXT_PROVIDER": "mock-text", "OPENAI_API_KEY": ""}
    process = subprocess.Popen([str(ROOT / "backend/bin/server")], env=env, stdout=(EVIDENCE / "server.log").open("ab"), stderr=subprocess.STDOUT)
    for _ in range(100):
        try:
            request("/healthz")
            return
        except (OSError, AssertionError):
            time.sleep(.05)
    raise AssertionError("Server did not start")


def stop():
    if process and process.poll() is None:
        process.kill()
        process.wait(10)


try:
    start()
    project = request("/api/v1/projects" + QUERY, {"title": "合成成片验收", "brief": "本地合成素材，仅验证导出、字幕和音轨，不代表模型生成。"}, "media-create-project", status=201)
    path = f"/api/v1/projects/{project['id']}/media"
    empty = request(path + QUERY)
    assert empty["version"] == 1 and empty["assets"] == []
    # Invalid content cannot become executable media or an external URL fetch.
    request(path + "/uploads" + QUERY, b"<svg onload=alert(1)></svg>", "media-invalid-svg", status=415, mime="image/svg+xml")
    request(path + "/uploads" + QUERY, b"https://127.0.0.1/private", "media-invalid-png", status=400, mime="image/png")
    request(path + "/uploads" + QUERY, b"0" * (32 * 1024 * 1024 + 1), "media-oversized-upload", status=413, mime="image/png")
    assert request(path + QUERY)["assets"] == []
    checks.append("upload MIME/content/32MiB limit rejection; no asset persisted")
    demo_body = {"expected_version": 1}
    media = request(path + "/demo" + QUERY, demo_body, "media-demo-acceptance")
    assert len(media["timeline"]["clips"]) > 0 and any(a["kind"] == "audio" for a in media["assets"])
    assert all(a["source"] == "synthetic" for a in media["assets"])
    demo = copy.deepcopy(media)
    assert request(path + "/demo" + QUERY, demo_body, "media-demo-acceptance") == demo
    checks.append("synthetic fixtures are labeled; duplicate demo does not duplicate assets")
    image_asset = next(a for a in media["assets"] if a["kind"] == "image")
    png = request(image_asset["file_url"])
    upload_path = path + "/uploads" + QUERY + "&filename=synthetic-upload.png&role=shot"
    uploaded = request(upload_path, png, "media-valid-upload", mime="image/png")
    assert len(uploaded["assets"]) == len(media["assets"]) + 1
    assert uploaded["assets"][-1]["source"] == "uploaded"
    assert request(upload_path, png, "media-valid-upload", mime="image/png") == uploaded
    request(upload_path, b"invalid", "media-valid-upload", status=409, mime="image/png")
    media = uploaded
    checks.append("valid synthetic PNG upload and exact replay; same-key altered file rejected")
    body = {"expected_version": media["version"], "timeline": copy.deepcopy(media["timeline"])}
    body["timeline"]["clips"][0]["subtitle"] = "合成验收字幕 Synthetic evidence"
    saved = request(path + "/timeline" + QUERY, body, "media-save-timeline", "PUT")
    assert saved["version"] == media["version"] + 1
    assert request(path + "/timeline" + QUERY, body, "media-save-timeline", "PUT") == saved
    request(path + "/timeline" + QUERY, body, "media-stale-timeline", "PUT", status=409)
    changed = copy.deepcopy(body)
    changed["timeline"]["clips"][0]["duration"] = 1
    request(path + "/timeline" + QUERY, changed, "media-save-timeline", "PUT", status=409)
    request(path + "?space_id=team-demo", status=404)
    checks.append("timeline save/replay/version conflict/key conflict/space isolation")
    export_body = {"expected_version": saved["version"]}
    exported = request(path + "/exports" + QUERY, export_body, "media-export-acceptance", status=202)
    export_id = exported["id"]
    replay = request(path + "/exports" + QUERY, export_body, "media-export-acceptance", status=202)
    assert replay["id"] == export_id
    def get_export():
        return next(e for e in request(path + QUERY)["exports"] if e["id"] == export_id)
    done = wait(get_export, lambda e: e["status"] in ("succeeded", "failed", "cancelled"))
    assert done["status"] == "succeeded", done
    video = EVIDENCE / "synthetic-demo.mp4"
    video.write_bytes(request(done["file_url"]))
    subtitles = request(done["subtitle_url"])
    (EVIDENCE / "synthetic-demo.srt").write_bytes(subtitles)
    assert "合成验收字幕" in subtitles.decode()
    probe = json.loads(subprocess.check_output(["ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", str(video)]))
    (EVIDENCE / "ffprobe.json").write_text(json.dumps(probe, ensure_ascii=False, indent=2))
    assert any(s["codec_type"] == "video" for s in probe["streams"])
    assert any(s["codec_type"] == "audio" for s in probe["streams"])
    assert any(s["codec_type"] == "subtitle" for s in probe["streams"])
    expected_duration = sum(c["duration"] for c in saved["timeline"]["clips"])
    assert abs(float(probe["format"]["duration"]) - expected_duration) < .25
    subprocess.run(["ffmpeg", "-v", "error", "-i", str(video), "-f", "null", "-"], check=True, timeout=30)
    volume = subprocess.run(["ffmpeg", "-hide_banner", "-i", str(video), "-vn", "-af", "volumedetect", "-f", "null", "-"], capture_output=True, text=True, check=True, timeout=30).stderr
    assert "max_volume:" in volume and "max_volume: -inf" not in volume
    (EVIDENCE / "audio-volume.txt").write_text(volume)
    checks.append("MP4 decodes completely; video/audio/subtitle tracks, audible signal energy and timeline duration verified")
    cancellation = request(path + "/exports" + QUERY, export_body, "media-cancel-export", status=202)
    cancel_path = path + f"/exports/{cancellation['id']}/cancel" + QUERY
    cancelled = request(cancel_path, {}, "media-cancel-request")
    assert cancelled["status"] == "cancelled", cancelled
    assert request(cancel_path, {}, "media-cancel-request") == cancelled
    request(path + f"/exports/{cancellation['id']}/file" + QUERY, status=404)
    interrupted = request(path + "/exports" + QUERY, export_body, "media-interrupted-export", status=202)
    assert interrupted["status"] == "running"
    stop()
    start()
    interrupted_replay = request(path + "/exports" + QUERY, export_body, "media-interrupted-export", status=202)
    assert interrupted_replay["id"] == interrupted["id"] and interrupted_replay["status"] == "failed"
    assert "中断" in interrupted_replay["error"]
    cancelled_replay = request(cancel_path, {}, "media-cancel-request")
    assert cancelled_replay["status"] == "cancelled"
    checks.append("cancel is persistent/idempotent; interrupted export is failed without automatic resubmission")
    restored = request(path + QUERY)
    assert restored["timeline"] == saved["timeline"]
    assert request(path + "/exports" + QUERY, export_body, "media-export-acceptance", status=202)["id"] == export_id
    assert request(done["file_url"]) == video.read_bytes()
    checks.append("completed export, timeline, assets and idempotency survive actual process restart")
    (EVIDENCE / "acceptance.json").write_text(json.dumps({"checks": checks, "project_id": project["id"], "duration": expected_duration, "model_validation": False, "contract_responses": contract_count}, ensure_ascii=False, indent=2))
    print(json.dumps({"passed": checks, "evidence": str(EVIDENCE)}, ensure_ascii=False))
finally:
    stop()
