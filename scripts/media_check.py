#!/usr/bin/env python3
"""Local synthetic media acceptance; never configures or calls an AI provider."""
import copy
import hashlib
import json
import os
from pathlib import Path
import signal
import socket
import struct
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import warnings
import zlib

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
            schema_name = "MediaUploadResult" if path.split("?")[0].endswith("/uploads") and status == 200 else "MediaState" if "assets" in value and "timeline" in value else "MediaExport" if "timeline" in value and "status" in value else None
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
    env = {"PATH": os.environ["PATH"], "AUTH_MODE": "demo", "DATA_DIR": str(EVIDENCE / "data"), "HTTP_ADDR": f"127.0.0.1:{PORT}", "IMAGE_PROVIDER": "mock-svg", "TEXT_PROVIDER": "mock-text", "OPENAI_API_KEY": ""}
    process = subprocess.Popen([str(ROOT / "backend/bin/server")], env=env, start_new_session=True, stdout=(EVIDENCE / "server.log").open("ab"), stderr=subprocess.STDOUT)
    for _ in range(100):
        try:
            request("/healthz")
            return
        except (OSError, AssertionError):
            time.sleep(.05)
    raise AssertionError("Server did not start")


def stop():
    if process and process.poll() is None:
        # Kill only this fixture's process group, including any local encoder.
        os.killpg(process.pid, signal.SIGKILL)
        process.wait(10)


def uncompressed_png(width, height):
    """Valid synthetic RGB pixels, deliberately beyond the retired byte quota."""
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
    compressor = zlib.compressobj(level=0)
    row = b"\0" + b"\x20\x60\x40" * width
    pixels = b"".join(compressor.compress(row) for _ in range(height)) + compressor.flush()
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)) + chunk(b"IDAT", pixels) + chunk(b"IEND", b"")


try:
    start()
    project = request("/api/v1/projects" + QUERY, {"title": "合成成片验收", "brief": "本地合成素材，仅验证导出、字幕和音轨，不代表模型生成。"}, "media-create-project", status=201)
    path = f"/api/v1/projects/{project['id']}/media"
    empty = request(path + QUERY)
    assert empty["version"] == 1 and empty["assets"] == []
    # Invalid content cannot become executable media or an external URL fetch.
    request(path + "/uploads" + QUERY, b"<svg onload=alert(1)></svg>", "media-invalid-svg", status=415, mime="image/svg+xml")
    request(path + "/uploads" + QUERY, b"https://127.0.0.1/private", "media-invalid-png", status=400, mime="image/png")
    rejected = request(path + "/uploads" + QUERY, b"0" * (32 * 1024 * 1024 + 1), "media-invalid-large-png", status=400, mime="image/png")
    assert rejected["error"]["code"] == "invalid_media"
    assert request(path + QUERY)["assets"] == []
    checks.append("upload MIME and invalid content rejection, including above the retired 32MiB quota; no asset persisted")
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
    assert uploaded["uploaded_asset_id"] == uploaded["assets"][-1]["id"]
    assert request(upload_path, png, "media-valid-upload", mime="image/png") == uploaded
    request(upload_path, b"invalid", "media-valid-upload", status=409, mime="image/png")
    media = uploaded
    checks.append("valid synthetic PNG upload and exact replay; same-key altered file rejected")
    large_png = uncompressed_png(4096, 3072)
    assert len(large_png) > 32 * 1024 * 1024
    large_sha = hashlib.sha256(large_png).hexdigest()
    large_upload = request(upload_path, large_png, "media-large-valid-upload", mime="image/png")
    assert len(large_upload["assets"]) == len(media["assets"]) + 1
    large_asset = next(a for a in large_upload["assets"] if a["id"] == large_upload["uploaded_asset_id"])
    assert large_asset["kind"] == "image" and large_asset["source"] == "uploaded"
    assert large_asset["content_sha256"] == large_sha
    assert hashlib.sha256(request(large_asset["file_url"])).hexdigest() == large_sha
    assert request(upload_path, large_png, "media-large-valid-upload", mime="image/png") == large_upload
    del large_png
    media = large_upload
    checks.append("valid 4096x3072 PNG larger than 32MiB accepted with exact content SHA, download and idempotent replay; retired per-file quota not restored")
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
    assert interrupted["status"] == "queued" and interrupted["render_version"] == 1
    stop()
    disk = json.loads((EVIDENCE / "data/media/state.json").read_text())
    interrupted_disk = next(e for p in disk["projects"].values() for e in p["exports"] if e["id"] == interrupted["id"])
    assert interrupted_disk["status"] in ("queued", "running"), interrupted_disk
    start()
    interrupted_replay = request(path + "/exports" + QUERY, export_body, "media-interrupted-export", status=202)
    assert interrupted_replay["id"] == interrupted["id"] and interrupted_replay["timeline"] == saved["timeline"]
    assert interrupted_replay["status"] in ("queued", "running", "succeeded"), interrupted_replay
    def recovered_export():
        return next(e for e in request(path + QUERY)["exports"] if e["id"] == interrupted["id"])
    recovered = wait(recovered_export, lambda e: e["status"] in ("succeeded", "failed", "cancelled"))
    assert recovered["status"] == "succeeded", recovered
    recovered_video = EVIDENCE / "synthetic-recovered.mp4"
    recovered_video.write_bytes(request(recovered["file_url"]))
    subprocess.run(["ffmpeg", "-v", "error", "-i", str(recovered_video), "-f", "null", "-"], check=True, timeout=30)
    cancelled_replay = request(cancel_path, {}, "media-cancel-request")
    assert cancelled_replay["status"] == "cancelled"
    checks.append("cancel is persistent/idempotent; interrupted versioned local export resumes the same snapshot/ID and produces a decodable MP4 without any model request")
    restored = request(path + QUERY)
    assert restored["timeline"] == saved["timeline"]
    assert request(path + "/exports" + QUERY, export_body, "media-export-acceptance", status=202)["id"] == export_id
    assert request(done["file_url"]) == video.read_bytes()
    assert hashlib.sha256(request(large_asset["file_url"])).hexdigest() == large_sha
    assert len(restored["exports"]) == 3 and len({e["id"] for e in restored["exports"]}) == 3
    assert request(path + "/exports" + QUERY, export_body, "media-interrupted-export", status=202)["id"] == interrupted["id"]
    checks.append("completed export, timeline, assets and idempotency survive actual process restart")
    (EVIDENCE / "acceptance.json").write_text(json.dumps({"checks": checks, "project_id": project["id"], "duration": expected_duration, "model_validation": False, "contract_responses": contract_count}, ensure_ascii=False, indent=2))
    print(json.dumps({"passed": checks, "evidence": str(EVIDENCE)}, ensure_ascii=False))
finally:
    stop()
