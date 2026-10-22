"""Exercise the built API across real process crashes with isolated temporary data."""
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request

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
    env = dict(os.environ, DATA_DIR=directory, HTTP_ADDR=f"127.0.0.1:{PORT}")
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
        payload = {"prompt": "进程恢复测试海报", "kind": "poster"}
        task = request("/api/v1/tasks" + SPACE, payload, "process-smoke-key")
        task_path = "/api/v1/tasks/" + task["id"] + SPACE
        await_value(lambda: request(task_path), lambda value: value["status"] == "running")
        crash()
        start()
        completed = await_value(lambda: request(task_path), lambda value: value["status"] == "succeeded")
        svg = request(completed["result_url"])
        assert "非 AI 生成".encode() in svg
        asset = request("/api/v1/assets" + SPACE, {"task_id": task["id"]})
        crash()
        start()
        assert request("/api/v1/assets" + SPACE)[0]["id"] == asset["id"]
        assert request(completed["result_url"]) == svg
        assert request("/api/v1/tasks" + SPACE, payload, "process-smoke-key")["id"] == task["id"]
        assert len(request("/api/v1/tasks" + SPACE)) == 1
        print("PASS: SIGKILL recovery, persisted SVG/assets, restart-safe idempotency")
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            process.wait(timeout=8)
