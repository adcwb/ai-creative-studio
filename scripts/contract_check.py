#!/usr/bin/env python3
"""Check actual local demo API responses and provider guards against OpenAPI.
Never enables a ready external provider; all configuration cases are local only.

Dependencies: Python jsonschema; Go toolchain on PATH.
Creates only a temporary binary, log and DATA_DIR; starts a loopback server on
an ephemeral port. Every exit path stops the server before removing the temp dir.
Example:
  python3 scripts/contract_check.py
"""
import argparse
import json
import os
from pathlib import Path
import signal
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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    parser.add_argument("--go", default="go", help="Go executable, defaults to PATH")
    args = parser.parse_args()
    repo = args.repo.resolve()
    spec = json.loads((repo / "api/openapi.json").read_text())
    resolver = RefResolver.from_schema(spec)
    count = 0

    # No fixed port or existing data directory is ever used.
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    with tempfile.TemporaryDirectory(prefix="studio-contract-") as temporary:
        temp = Path(temporary)
        binary = temp / "server"
        subprocess.run(
            [args.go, "build", "-o", str(binary), "./cmd/server"],
            cwd=repo / "backend", check=True,
        )
        # Do not inspect/inherit any existing OPENAI_* credential or configuration.
        env = {
            "PATH": os.environ.get("PATH", ""),
            "HTTP_ADDR": f"127.0.0.1:{port}",
            "DATA_DIR": str(temp / "data"),
        }
        proc = None

        def stop():
            nonlocal proc
            if proc is not None and proc.poll() is None:
                proc.send_signal(signal.SIGTERM)
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait(timeout=5)
            proc = None

        with (temp / "server.log").open("w+") as log:
            def start(provider="mock-svg", key=""):
                nonlocal proc
                local_env = dict(env, IMAGE_PROVIDER=provider, OPENAI_API_KEY=key)
                proc = subprocess.Popen([str(binary)], env=local_env, stdout=log, stderr=log)
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline:
                    if proc.poll() is not None:
                        break
                    try:
                        with urllib.request.urlopen(base + "/healthz", timeout=.2):
                            return
                    except (OSError, urllib.error.URLError):
                        time.sleep(.05)
                log.flush()
                log.seek(0)
                raise RuntimeError("Temporary server did not become healthy:\n" + log.read())

            def call(method, path, route, body=None, key=None, expected=200):
                nonlocal count
                data = None if body is None else json.dumps(body).encode()
                headers = {"Content-Type": "application/json"}
                if key:
                    headers["Idempotency-Key"] = key
                request = urllib.request.Request(base + path, data=data, headers=headers, method=method)
                try:
                    response = urllib.request.urlopen(request, timeout=3)
                except urllib.error.HTTPError as error:
                    response = error
                with response:
                    status = response.status
                    raw = response.read()
                    content_type = response.headers.get_content_type()
                assert status == expected, (method, path, status, raw)
                definition = spec["paths"][route][method.lower()]["responses"][str(status)]
                schema = definition["content"][content_type]["schema"]
                value = json.loads(raw) if content_type == "application/json" else raw.decode()
                Draft4Validator(schema, resolver=resolver).validate(value)
                count += 1
                return value

            def wait_terminal(task_id):
                deadline = time.monotonic() + 8
                while time.monotonic() < deadline:
                    item = call("GET", f"/api/v1/tasks/{task_id}?space_id=personal-demo", "/api/v1/tasks/{id}")
                    if item["status"] in ["failed", "succeeded"]:
                        return item
                    time.sleep(.05)
                raise RuntimeError("Task did not reach a terminal state")

            try:
                start()
                call("GET", "/healthz", "/healthz")
                capabilities = call("GET", "/api/v1/capabilities", "/api/v1/capabilities")
                assert capabilities["provider"] == "mock-svg" and capabilities["mode"] == "mock" and capabilities["ready"]
                call("GET", "/api/v1/spaces", "/api/v1/spaces")
                catalog = call("GET", "/api/v1/templates", "/api/v1/templates")
                assert [(t["kind"], t["ratio"], t["style"]) for t in catalog["templates"]] == [
                    ("poster", "4:5", "illustration"),
                    ("portrait", "4:5", "studio"),
                    ("general", "1:1", "natural"),
                ]
                create = "/api/v1/tasks?space_id=personal-demo"
                parent_input = {
                    "prompt": "[mock-fail] Contract Case", "kind": "poster",
                    "expected_provider": "mock-svg", "expected_model": "",
                }
                parent = call("POST", create, "/api/v1/tasks", parent_input, "contract-original", 202)
                assert parent["ratio"] == "4:5" and parent["style"] == "natural"
                assert wait_terminal(parent["id"])["status"] == "failed"
                retry_url = f'/api/v1/tasks/{parent["id"]}/retry?space_id=personal-demo'
                attempt_input = {
                    "prompt": "  Contract Recovery Landscape  ", "kind": "general",
                    "ratio": "16:9", "style": "studio",
                    "expected_provider": "mock-svg", "expected_model": "",
                }
                child = call("POST", retry_url, "/api/v1/tasks/{id}/retry", attempt_input, "contract-retry-key", 202)
                assert child["retry_of"] == parent["id"] and child["id"] != parent["id"]
                replay = call("POST", retry_url, "/api/v1/tasks/{id}/retry", attempt_input, "contract-retry-key")
                assert child["id"] == replay["id"]
                expected_real = dict(attempt_input, expected_provider="openai-images", expected_model="gpt-image-2-2026-04-21")
                # Matching accepted keys take precedence over changed expectations.
                replay_changed_expectation = call("POST", retry_url, "/api/v1/tasks/{id}/retry", expected_real, "contract-retry-key")
                assert replay_changed_expectation["id"] == child["id"] and replay_changed_expectation["provider"] == "mock-svg"
                parent_changed_expectation = dict(parent_input, expected_provider="openai-images", expected_model="gpt-image-2-2026-04-21")
                assert call("POST", create, "/api/v1/tasks", parent_changed_expectation, "contract-original")["id"] == parent["id"]
                for path, route, key in [
                    (create, "/api/v1/tasks", "contract-guard-create"),
                    (retry_url, "/api/v1/tasks/{id}/retry", "contract-guard-retry"),
                ]:
                    rejected = call("POST", path, route, expected_real, key, 409)
                    assert rejected["error"]["code"] == "provider_changed"
                invalid_pairs = [
                    {"expected_provider": "mock-svg"},
                    {"expected_model": ""},
                    {"expected_provider": None, "expected_model": ""},
                    {"expected_provider": "mock-svg", "expected_model": None},
                    {"expected_provider": "unrecognized", "expected_model": ""},
                    {"expected_provider": "mock-svg", "expected_model": "gpt-image-2-2026-04-21"},
                    {"expected_provider": "openai-images", "expected_model": ""},
                ]
                for index, pair in enumerate(invalid_pairs):
                    invalid = {"prompt": "valid contract input", "kind": "poster", **pair}
                    for path, route in [(create, "/api/v1/tasks"), (retry_url, "/api/v1/tasks/{id}/retry")]:
                        rejected = call("POST", path, route, invalid, f"contract-invalid-guard-{index}", 400)
                        assert rejected["error"]["code"] == "invalid_input"
                call("POST", create, "/api/v1/tasks", attempt_input, "contract-retry-key", 409)
                call("POST", create, "/api/v1/tasks", {
                    "prompt": "valid contract case", "kind": "poster", "ratio": None,
                }, "contract-null-key", 400)
                done = wait_terminal(child["id"])
                assert done["status"] == "succeeded"
                assert done["result_mime"] == "image/svg+xml" and done["result_width"] == 1600 and done["result_height"] == 900
                svg = call("GET", done["result_url"], "/api/v1/tasks/{id}/result")
                assert 'viewBox="0 0 1600 900"' in svg and 'data-style="studio"' in svg
                asset = call("POST", "/api/v1/assets?space_id=personal-demo", "/api/v1/assets", {"task_id": done["id"]})
                assert asset["ratio"] == "16:9" and asset["style"] == "studio"
                assert asset["provider"] == "mock-svg" and asset["result_mime"] == "image/svg+xml" and asset["result_width"] == 1600 and asset["result_height"] == 900
                listed = call("GET", "/api/v1/assets?space_id=personal-demo&q=%20RECOVERY%20&kind=general", "/api/v1/assets")
                assert [a["id"] for a in listed] == [asset["id"]]
                assert call("GET", "/api/v1/assets?space_id=personal-demo&q=absent&kind=general", "/api/v1/assets") == []
                assert call("GET", "/api/v1/assets?space_id=team-demo&q=recovery&kind=general", "/api/v1/assets") == []
                call("GET", "/api/v1/assets?space_id=personal-demo&kind=invalid", "/api/v1/assets", expected=400)
                call("GET", f'/api/v1/tasks/{child["id"]}?space_id=team-demo', "/api/v1/tasks/{id}", expected=404)
                stop()
                start()
                restored = call("POST", retry_url, "/api/v1/tasks/{id}/retry", attempt_input, "contract-retry-key")
                assert restored["id"] == child["id"] and restored["retry_of"] == parent["id"] and restored["status"] == "succeeded"
                final = call("GET", "/api/v1/assets?space_id=personal-demo&q=recovery&kind=general", "/api/v1/assets")
                assert final == listed
                stop()
                configurations = [
                    ("openai-images", "", "missing_api_key"),
                    ("bad-synthetic-secret-provider", "", "invalid_provider"),
                    ("openai-images", "synthetic\ninvalid", "invalid_configuration"),
                ]
                for provider, key, reason in configurations:
                    start(provider, key)
                    capabilities = call("GET", "/api/v1/capabilities", "/api/v1/capabilities")
                    assert not capabilities["ready"] and capabilities["reason_code"] == reason
                    source_replay = call("POST", create, "/api/v1/tasks", parent_input, "contract-original")
                    child_replay = call("POST", retry_url, "/api/v1/tasks/{id}/retry", expected_real, "contract-retry-key")
                    assert source_replay["id"] == parent["id"] and source_replay["provider"] == "mock-svg"
                    assert child_replay["id"] == child["id"] and child_replay["provider"] == "mock-svg"
                    for path, route, new_key in [
                        (create, "/api/v1/tasks", "contract-unavailable-create"),
                        (retry_url, "/api/v1/tasks/{id}/retry", "contract-unavailable-retry"),
                    ]:
                        rejected = call("POST", path, route, expected_real, new_key, 503)
                        assert rejected["error"]["code"] == "provider_not_ready"
                    call("GET", done["result_url"], "/api/v1/tasks/{id}/result")
                    assert call("GET", "/api/v1/assets?space_id=personal-demo&q=recovery&kind=general", "/api/v1/assets") == listed
                    tasks = call("GET", "/api/v1/tasks?space_id=personal-demo", "/api/v1/tasks")
                    assert len(tasks) == 2, "Rejected guarded/unavailable requests must not create tasks"
                    stop()
                log.flush()
                log.seek(0)
                recorded_log = log.read()
                snapshot = (temp / "data/state.json").read_text()
                for sensitive in ["bad-synthetic-secret-provider", "synthetic\ninvalid", "synthetic\\ninvalid"]:
                    assert sensitive not in recorded_log and sensitive not in snapshot
                print(f"PASS: {count} actual HTTP responses conform to OpenAPI schemas (including polling).")
                print("Verified: catalog/defaults, capability states, atomic provider guards, invalid expectation pairs, accepted-key replay before readiness/expectations, 503 without queueing, task/asset metadata, persistent search and historical results across restarts.")
                print("No real credential was read and no external provider was enabled; all runtime data and processes were temporary.")
            finally:
                stop()


if __name__ == "__main__":
    main()
