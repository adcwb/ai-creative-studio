"""Exercise the built API across real process crashes with isolated temporary data."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request
import urllib.parse

BINARY = Path(__file__).resolve().parents[1] / "backend/bin/server"
with socket.socket() as sock:
    sock.bind(("127.0.0.1", 0))
    PORT = sock.getsockname()[1]
BASE = f"http://127.0.0.1:{PORT}"
SPACE = "?space_id=personal-demo"


def request(path, body=None, key=None):
    headers = {"Content-Type": "application/json"}
    if key:
        headers["Idempotency-Key"] = key
    req = urllib.request.Request(
        BASE + path,
        data=json.dumps(body).encode() if body is not None else None,
        headers=headers,
    )
    with urllib.request.urlopen(req, timeout=3) as response:
        data = response.read()
        return json.loads(data) if "application/json" in response.headers["Content-Type"] else data


def await_value(fn, predicate):
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        try:
            value = fn()
            if predicate(value):
                return value
        except (OSError, TimeoutError):
            pass
        time.sleep(0.05)
    raise AssertionError("API did not reach expected state")


with tempfile.TemporaryDirectory(prefix="studio-process-smoke-") as directory:
    env = dict(os.environ, DATA_DIR=directory, HTTP_ADDR=f"127.0.0.1:{PORT}", IMAGE_PROVIDER="mock-svg", OPENAI_API_KEY="")
    process = None

    def start():
        global process
        process = subprocess.Popen([str(BINARY)], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        await_value(lambda: request("/healthz"), lambda value: value["status"] == "ok")

    def crash():
        process.kill()
        process.wait(timeout=5)

    try:
        start()
        catalog = request("/api/v1/templates")
        assert len(catalog["templates"]) == 3
        original_payload = {"prompt": "[mock-fail] 进程恢复测试海报", "kind": "poster"}
        original = request("/api/v1/tasks" + SPACE, original_payload, "process-smoke-original")
        original_path = "/api/v1/tasks/" + original["id"] + SPACE
        failed = await_value(lambda: request(original_path), lambda value: value["status"] == "failed")
        assert failed["ratio"] == "4:5" and failed["style"] == "natural"
        payload = {"prompt": "进程恢复测试海报", "kind": "poster", "ratio": "16:9", "style": "studio"}
        retry_path = "/api/v1/tasks/" + original["id"] + "/retry" + SPACE
        task = request(retry_path, payload, "process-smoke-retry")
        assert task["retry_of"] == original["id"]
        task_path = "/api/v1/tasks/" + task["id"] + SPACE
        await_value(lambda: request(task_path), lambda value: value["status"] == "running")
        crash()
        start()
        completed = await_value(lambda: request(task_path), lambda value: value["status"] == "succeeded")
        assert completed["retry_of"] == original["id"]
        assert completed["ratio"] == "16:9" and completed["style"] == "studio"
        svg = request(completed["result_url"])
        assert "非 AI 生成".encode() in svg
        asset = request("/api/v1/assets" + SPACE, {"task_id": task["id"]})
        crash()
        start()
        assert request("/api/v1/assets" + SPACE)[0]["id"] == asset["id"]
        assert request(completed["result_url"]) == svg
        assert request(retry_path, payload, "process-smoke-retry")["id"] == task["id"]
        assert request(original_path)["status"] == "failed"
        assert request(original_path)["prompt"] == original_payload["prompt"]
        assert request("/api/v1/tasks" + SPACE, original_payload, "process-smoke-original")["id"] == original["id"]
        assert len(request("/api/v1/tasks" + SPACE)) == 2
        search = "&" + urllib.parse.urlencode({"q": "恢复测试", "kind": "poster"})
        results = request("/api/v1/assets" + SPACE + search)
        assert len(results) == 1 and results[0]["id"] == asset["id"]
        assert results[0]["ratio"] == "16:9" and results[0]["style"] == "studio"
        assert request("/api/v1/assets" + SPACE + "&kind=portrait") == []
        assert request("/api/v1/assets?space_id=team-demo" + search) == []
        print("PASS: backend catalog, failed-parent retry execution, SIGKILL recovery, persisted retry lineage/SVG/assets, restart-safe idempotency, server-side search and space filtering")
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            process.wait(timeout=8)
