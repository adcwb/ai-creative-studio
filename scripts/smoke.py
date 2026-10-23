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


def request(path, body=None, key=None, method=None):
    headers = {"Content-Type": "application/json"}
    if key:
        headers["Idempotency-Key"] = key
    req = urllib.request.Request(
        BASE + path,
        data=json.dumps(body).encode() if body is not None else None,
        headers=headers, method=method,
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
    env = dict(os.environ, AUTH_MODE="demo", TEXT_PROVIDER="mock-text", SPEECH_PROVIDER="", VIDEO_PROVIDER="", ASR_PROVIDER="", DATA_DIR=directory, HTTP_ADDR=f"127.0.0.1:{PORT}", IMAGE_PROVIDER="mock-svg", OPENAI_API_KEY="")
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
        project_body = {"title": "合成短剧：钟楼来信", "brief": "一位修钟师与快递员合作，在雨停之前送达旧信。"}
        project = request("/api/v1/projects" + SPACE, project_body, "process-project-create")
        project_path = "/api/v1/projects/" + project["id"] + SPACE
        generation_path = "/api/v1/projects/" + project["id"] + "/generations" + SPACE
        save_path = "/api/v1/projects/" + project["id"] + "/script" + SPACE
        assert project["version"] == 1
        generation_body = {"expected_version": 1}
        generation = request(generation_path, generation_body, "process-project-generation")
        await_value(lambda: request(project_path), lambda p: p.get("latest_generation", {}).get("status") == "running")
        crash()
        start()
        project = await_value(lambda: request(project_path), lambda p: p.get("latest_generation", {}).get("status") == "succeeded")
        assert project["version"] == 2 and project["script_brief"] == project_body["brief"]
        assert len(project["script"]["characters"]) == 2 and len(project["script"]["scenes"]) == 3
        assert project["latest_generation"]["id"] == generation["id"]
        assert project["latest_generation"]["provider"] == "mock-script"
        assert request(generation_path, generation_body, "process-project-generation")["id"] == generation["id"]
        assert len(request(generation_path)) == 1
        edited_script = json.loads(json.dumps(project["script"]))
        edited_script["scenes"][0]["action"] = "合成的人工编辑，进程重启后必须保留。"
        save_body = {"script": edited_script, "expected_version": 2}
        saved = request(save_path, save_body, "process-project-save", method="PUT")
        assert saved["version"] == 3 and saved["script"] == edited_script
        crash()
        start()
        assert request(project_path)["script"] == edited_script
        assert request(save_path, save_body, "process-project-save", method="PUT")["version"] == 3
        assert request("/api/v1/projects" + SPACE, project_body, "process-project-create")["version"] == 3
        assert request("/api/v1/projects?space_id=team-demo") == []
        failure_input = {"title": project_body["title"], "brief": "[mock-fail] 合成失败路径必须保留人工剧本", "expected_version": 3}
        updated = request(project_path, failure_input, "process-project-update", method="PUT")
        assert updated["version"] == 4 and updated["script_brief"] == project_body["brief"]
        request(generation_path, {"expected_version": 4}, "process-project-failure")
        failed_project = await_value(lambda: request(project_path), lambda p: p["latest_generation"]["status"] == "failed")
        assert failed_project["version"] == 4 and failed_project["script"] == edited_script
        assert failed_project["latest_generation"]["error_code"] == "mock_script_failed"
        # Script generation remains usable when the image provider lacks a key.
        crash()
        env["IMAGE_PROVIDER"] = "openai-images"
        env["OPENAI_API_KEY"] = ""
        start()
        assert request("/api/v1/capabilities")["ready"] is False
        independent = request("/api/v1/projects" + SPACE, project_body, "process-independent-project")
        independent_path = "/api/v1/projects/" + independent["id"]
        request(independent_path + "/generations" + SPACE, {"expected_version": 1}, "process-independent-generation")
        independent = await_value(lambda: request(independent_path + SPACE), lambda p: p.get("latest_generation", {}).get("status") == "succeeded")
        assert independent["version"] == 2 and independent["latest_generation"]["provider"] == "mock-script"
        # Storyboards use the saved script snapshot and survive a real SIGKILL.
        board_path = independent_path + "/storyboard" + SPACE
        boards_path = independent_path + "/storyboard-generations" + SPACE
        board_body = {"expected_version": independent["version"]}
        board_attempt = request(boards_path, board_body, "process-board-generate")
        await_value(lambda: request(independent_path + SPACE), lambda p: p.get("latest_storyboard_generation", {}).get("status") == "running")
        crash()
        start()
        boarded = await_value(lambda: request(independent_path + SPACE), lambda p: p.get("latest_storyboard_generation", {}).get("status") == "succeeded")
        assert boarded["version"] == independent["version"] + 1
        assert boarded["latest_storyboard_generation"]["id"] == board_attempt["id"]
        assert boarded["latest_storyboard_generation"]["provider"] == "mock-storyboard"
        assert boarded["storyboard"]["source_script_version"] == boarded["script_version"]
        assert boarded["storyboard_stale"] is False
        assert request(boards_path, board_body, "process-board-generate")["id"] == board_attempt["id"]
        assert len(request(boards_path)) == 1
        shots = json.loads(json.dumps(boarded["storyboard"]["shots"]))
        shots[0]["description"] = "合成分镜人工修改，重启后保留。"
        board_save = {"expected_version": boarded["version"], "shots": shots}
        board_saved = request(board_path, board_save, "process-board-save", method="PUT")
        crash()
        start()
        assert request(independent_path + SPACE)["storyboard"]["shots"] == shots
        assert request(board_path, board_save, "process-board-save", method="PUT")["version"] == board_saved["version"]
        revised_script = json.loads(json.dumps(board_saved["script"]))
        revised_script["scenes"][0]["action"] = "[mock-fail] 合成分镜失败不能覆盖已保存计划。"
        revised = request(independent_path + "/script" + SPACE, {"expected_version": board_saved["version"], "script": revised_script}, "process-board-source", method="PUT")
        assert revised["storyboard_stale"] is True
        request(boards_path, {"expected_version": revised["version"]}, "process-board-failure")
        board_failed = await_value(lambda: request(independent_path + SPACE), lambda p: p["latest_storyboard_generation"]["status"] == "failed")
        assert board_failed["version"] == revised["version"]
        assert board_failed["storyboard"] == board_saved["storyboard"]
        assert board_failed["storyboard_stale"] is True
        crash()
        start()
        assert request(independent_path + SPACE)["storyboard"] == board_saved["storyboard"]
        print("PASS: image/script recovery and receipts; storyboard running SIGKILL recovery, saved edits and receipt restart replay, source staleness and failure preserving old plan; unavailable image provider independence")
    finally:
        if process is not None and process.poll() is None:
            process.terminate()
            process.wait(timeout=8)
