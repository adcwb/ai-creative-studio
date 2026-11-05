#!/usr/bin/env python3
"""Check actual local image/project API responses and guards against OpenAPI.
Never enables a ready external provider; all configuration cases are local only.

Dependencies: Python jsonschema; Go toolchain on PATH.
Creates only a temporary binary, log and DATA_DIR; starts a loopback server on
an ephemeral port. Every exit path stops the server before removing the temp dir.
Example:
  python3 scripts/contract_check.py
"""
import argparse
import copy
import json
import os
from pathlib import Path
import signal
import shutil
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
    parser.add_argument("--server-binary", type=Path, help="Use an already built server for this runtime instead of invoking Go")
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
        if args.server_binary:
            shutil.copyfile(args.server_binary.resolve(), binary)
            binary.chmod(0o700)
        else:
            subprocess.run(
                [args.go, "build", "-o", str(binary), "./cmd/server"],
                cwd=repo / "backend", check=True,
            )
        # Do not inspect/inherit any existing OPENAI_* credential or configuration.
        env = {
            "PATH": os.environ.get("PATH", ""),
            "HTTP_ADDR": f"127.0.0.1:{port}",
            "AUTH_MODE": "demo", "DATA_DIR": str(temp / "data"),
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
                local_env = dict(env, IMAGE_PROVIDER=provider, OPENAI_API_KEY=key, IMAGE_MODEL="gpt-image-2-2026-04-21")
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

            def project_call(method, suffix="", body=None, key=None, expected=200, space="personal-demo"):
                path = "/api/v1/projects" + suffix
                route = "/api/v1/projects"
                if suffix:
                    route += "/{id}"
                    if suffix.endswith("/storyboard-generations"):
                        route += "/storyboard-generations"
                    elif suffix.endswith("/storyboard"):
                        route += "/storyboard"
                    elif suffix.endswith("/structure-upgrades"):
                        route += "/structure-upgrades"
                    elif suffix.endswith("/script"):
                        route += "/script"
                    elif suffix.endswith("/generations"):
                        route += "/generations"
                return call(method, path + "?space_id=" + space, route, body, key, expected)

            def wait_script(project_id):
                deadline = time.monotonic() + 8
                while time.monotonic() < deadline:
                    project = project_call("GET", "/" + project_id)
                    if project.get("latest_generation", {}).get("status") in ["succeeded", "failed"]:
                        return project
                    time.sleep(.05)
                raise RuntimeError("Script generation did not reach a terminal state")

            def wait_storyboard(project_id):
                deadline = time.monotonic() + 8
                while time.monotonic() < deadline:
                    project = project_call("GET", "/" + project_id)
                    if project.get("latest_storyboard_generation", {}).get("status") in ["succeeded", "failed"]:
                        return project
                    time.sleep(.05)
                raise RuntimeError("Storyboard generation did not reach a terminal state")

            def check_creative():
                caps = call("GET", "/api/v1/creative-capabilities", "/api/v1/creative-capabilities")
                assert caps["provider"] == "mock-text" and caps["ready"]
                media_caps = call("GET", "/api/v1/creative-media-capabilities", "/api/v1/creative-media-capabilities")
                assert media_caps["image"]["ready"]
                registry = call("GET", "/api/v1/creative-registry", "/api/v1/creative-registry")
                assert {item["id"] for item in registry} >= {"openai", "anthropic", "google", "ark", "minimax", "jimeng"}
                p = project_call("POST", body={"title": "创作合同", "brief": "两位旧友在车站交换一本书。"}, key="creative-contract-create", expected=201)
                root = "/api/v1/projects/" + p["id"]
                q = "?space_id=personal-demo"
                route = "/api/v1/projects/{id}"
                body = {"expected_version": p["version"], "expected_provider": caps["provider"]}
                job = call("POST", root + "/analysis" + q, route + "/analysis", body, "creative-analysis", 202)
                assert call("POST", root + "/analysis" + q, route + "/analysis", body, "creative-analysis")["id"] == job["id"]
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline:
                    jobs = call("GET", root + "/creative-jobs" + q, route + "/creative-jobs")
                    if jobs[0]["status"] in ["succeeded", "failed"]:
                        break
                    time.sleep(.05)
                assert jobs[0]["status"] == "succeeded" and jobs[0]["analysis"]["questions"]
                call("GET", root + "/creative-jobs?space_id=team-demo", route + "/creative-jobs", expected=404)
                conflict = call("POST", root + "/analysis" + q, route + "/analysis", dict(body, expected_version=999), "creative-analysis", 409)
                assert conflict["error"]["code"] == "idempotency_conflict"
                stop(); start()
                assert call("POST", root + "/analysis" + q, route + "/analysis", body, "creative-analysis")["status"] == "succeeded"

            def check_library():
                route = "/api/v1/library/assets"
                q = "?space_id=personal-demo"
                body = {"kind": "character", "card": {"name": "Reference actor", "description": "Stable costume", "prohibited_changes": ["Copper button"], "character": {"appearance": "", "hair": "Short", "costume": "Green coat", "expression": "", "voice_notes": ""}}, "references": []}
                original = call("POST", route + q, route, body, "library-contract-create", 201)
                asset = original["asset"]["id"]
                root = route + "/" + asset
                detail_route = route + "/{assetId}"
                call("GET", root + "?space_id=team-demo", detail_route, expected=404)
                next_body = copy.deepcopy(body)
                next_body["expected_asset_version_id"] = original["version"]["id"]
                next_body["card"]["character"]["costume"] = "Blue coat"
                newer = call("POST", root + "/versions" + q, detail_route + "/versions", next_body, "library-contract-next", 201)
                call("POST", root + "/versions" + q, detail_route + "/versions", next_body, "library-contract-stale", 409)
                versions = call("GET", root + "/versions" + q, detail_route + "/versions")
                assert [v["number"] for v in versions["items"]] == [2, 1]
                old = call("GET", root + "/versions/" + original["version"]["id"] + q, detail_route + "/versions/{versionId}")
                assert old == original["version"]
                project = project_call("POST", body={"title": "Library binding", "brief": "Reuse one pinned character version."}, key="library-contract-project", expected=201)
                bind_url = "/api/v1/projects/" + project["id"] + "/library-bindings" + q
                bind_route = "/api/v1/projects/{id}/library-bindings"
                binding = {"expected_media_version": 1, "library_asset_id": asset, "asset_version_id": old["id"], "usage": "character", "shot_id": ""}
                call("POST", bind_url, bind_route, binding, "library-contract-bind", 201)
                view = call("GET", bind_url, bind_route)
                assert view["items"][0]["asset_version_id"] == old["id"]
                use_path, use_route = root + "/use-authorization", detail_route + "/use-authorization"
                grant = dict(expected_revision=0, status="granted", version_ids=[old["id"]], purposes=["image_reference"], rights_holder="Synthetic creator", basis="own_work", evidence="Synthetic original fixture", attested=True)
                missing = copy.deepcopy(grant); del missing["expected_revision"]
                call("POST", use_path + q, use_route, missing, "library-grant-missing-revision", 400)
                missing["expected_revision"] = None
                call("POST", use_path + q, use_route, missing, "library-grant-null-revision", 400)
                accepted = call("POST", use_path + q, use_route, grant, "library-grant-original")
                assert accepted["authorization"]["revision"] == 1 and accepted["authorization"]["version_ids"] == [old["id"]]
                call("POST", use_path + q, use_route, grant, "library-grant-stale", 409)
                changed = copy.deepcopy(grant); changed["expected_revision"] = 1; changed["version_ids"] = ["foreign-version"]
                call("POST", use_path + q, use_route, changed, "library-grant-foreign-version", 404)
                call("POST", use_path + q, use_route, dict(expected_revision=1,status="revoked",reason="Synthetic withdrawal"), "library-revoke-original")
                assert call("POST", use_path + q, use_route, grant, "library-grant-original") == accepted
                assert call("GET", use_path + q, use_route)["authorization"]["status"] == "revoked"
                archive = {"expected_asset_version_id": newer["version"]["id"]}
                call("POST", root + "/archive" + q, detail_route + "/archive", archive, "library-contract-archive")
                stop(); start()
                assert call("POST", route + q, route, body, "library-contract-create") == original
                call("POST", bind_url, bind_route, binding, "library-contract-bind")
                assert len(call("GET", bind_url, bind_route)["items"]) == 1
                assert call("GET", use_path + q, use_route)["authorization"]["status"] == "revoked"
                assert call("POST", use_path + q, use_route, grant, "library-grant-original") == accepted
                call("GET", route + q + "&status=archived", route)
                for kind, fields in [("style",dict(palette="Blue and amber",linework="Open lines",composition="Keep negative space",lighting="Side light",texture="Paper")),("voice",dict(timbre="Clear",performance="Restrained",pace="Pause before answering",pronunciation="Keep names consistent"))]:
                    intent=dict(kind=kind,card=dict(name=kind+" intent",description="Creative intent, not authorization or native model control.",prohibited_changes=["Keep choice "+str(i) for i in range(31)],**{kind:fields}),references=[])
                    original_intent=call("POST",route+q,route,intent,"library-intent-"+kind,201)
                    assert original_intent["version"]["card"][kind]==fields and len(original_intent["version"]["card"]["prohibited_changes"])==31
                    typed=call("GET",route+q+"&kind="+kind,route)
                    assert len(typed["items"])==1 and typed["items"][0]["id"]==original_intent["asset"]["id"]
                    changed=copy.deepcopy(intent);changed["expected_asset_version_id"]=original_intent["version"]["id"];changed["card"]["description"]="A later authored revision."
                    intent_root=route+"/"+original_intent["asset"]["id"]
                    revised=call("POST",intent_root+"/versions"+q,detail_route+"/versions",changed,"library-intent-next-"+kind,201)
                    assert revised["version"]["number"]==2
                    mixed=copy.deepcopy(intent);mixed["card"]["character"]={}
                    call("POST",route+q,route,mixed,"library-intent-mixed-"+kind,400)
                    if kind=="voice":
                        fake=copy.deepcopy(intent);fake["references"]=[dict(project_id=project["id"],asset_id="not-a-voice",role="reference",view_angle="")]
                        call("POST",route+q,route,fake,"library-intent-image-not-voice",400)
                    stop();start()
                    assert call("POST",route+q,route,intent,"library-intent-"+kind)==original_intent
                    assert call("GET",intent_root+"/versions/"+original_intent["version"]["id"]+q,detail_route+"/versions/{versionId}")==original_intent["version"]

            def check_structure():
                p = project_call("POST", body={"title": "Stable source contract", "brief": "An original script keeps associations across edits."}, key="structure-contract-project", expected=201)
                suffix = "/" + p["id"]
                project_call("POST", suffix + "/generations", {"expected_version": p["version"]}, "structure-contract-script", 202)
                legacy = wait_script(p["id"])
                assert "structure_version" not in legacy and "id" not in legacy["script"]["scenes"][0]
                body = {"expected_version": legacy["version"]}
                upgraded = project_call("POST", suffix + "/structure-upgrades", body, "structure-contract-upgrade")
                assert upgraded["structure_version"] == 2 and upgraded["script"]["structure_version"] == 2
                assert "structure_mappings" not in upgraded
                assert upgraded["version"] == legacy["version"] + 1
                assert upgraded["script_version"] == legacy["script_version"] + 1
                edited = copy.deepcopy(upgraded["script"])
                edited["characters"][0]["name"] = "Renamed character"
                edited["scenes"].reverse()
                changed = project_call("PUT", suffix + "/script", {"expected_version": upgraded["version"], "script": edited}, "structure-contract-rename")
                assert changed["script"]["scenes"][-1]["id"] == upgraded["script"]["scenes"][0]["id"]
                old_id = upgraded["script"]["characters"][0]["id"]
                for scene in changed["script"]["scenes"]:
                    for line in scene["dialogue"]:
                        if line["character_id"] == old_id:
                            assert line["character"] == "Renamed character"
                project_call("PUT", suffix + "/script", {"expected_version": changed["version"], "script": legacy["script"]}, "structure-contract-old-client", 400)
                project_call("POST", suffix + "/structure-upgrades", body, "structure-contract-other-space", 404, space="team-demo")
                project_call("POST", suffix + "/structure-upgrades", dict(body, expected_version=999), "structure-contract-upgrade", 409)
                stop(); start()
                assert project_call("POST", suffix + "/structure-upgrades", body, "structure-contract-upgrade") == changed

            def check_episode_migrations():
                source = project_call("POST", body={"title": "Migration source", "brief": "Three independent original episodes remain unchanged."}, key="migration-contract-project", expected=201)
                suffix = "/" + source["id"]
                project_call("POST", suffix + "/generations", {"expected_version": 1}, "migration-contract-script", 202)
                source = wait_script(source["id"])
                script = copy.deepcopy(source["script"])
                for scene, ep in zip(script["scenes"], [1, 7, 120]):
                    scene["episode_number"] = ep
                source = project_call("PUT", suffix + "/script", {"expected_version": source["version"], "script": script}, "migration-contract-scenes")
                q = "?space_id=personal-demo"
                root = call("POST", "/api/v1/series" + q, "/api/v1/series", {"title": "Migration destination", "summary": "An explicit copy into independent projects."}, "migration-contract-series", 201)
                season = call("POST", "/api/v1/series/" + root["id"] + "/seasons" + q, "/api/v1/series/{id}/seasons", {}, "migration-contract-season", 201)
                path = "/api/v1/projects/" + source["id"]
                route = "/api/v1/projects/{id}"
                summary = call("GET", path + "/migration-source" + q, route + "/migration-source")
                assert summary["episodes"] == [1, 7, 120]
                data = {"source_episode_number": 7, "target_season_id": season["id"], "target_ordinal": 7, "expected_project_version": source["version"], "expected_media_version": summary["media_version"], "expected_season_version": season["row_version"], "output_mode": "series", "storyboard_policy": "include", "shot_assignments": {}, "character_assignments": {}}
                preview = call("POST", path + "/episode-migration-preview" + q, route + "/episode-migration-preview", data, "migration-contract-preview")
                assert preview["issues"] == [] and preview["script"]["scenes"][0]["episode_number"] == 1
                create = {"input": data, "preview_digest": preview["digest"]}
                job = call("POST", path + "/episode-migrations" + q, route + "/episode-migrations", create, "migration-contract-create", 201)
                assert job["state"] == "accepted"
                page = call("GET", path + "/episode-migrations" + q, route + "/episode-migrations")
                assert "plan" not in page["items"][0] and "copies" not in page["items"][0]
                project_call("GET", "/" + job["target_project_id"], expected=404)
                step = path + "/episode-migrations/" + job["id"]
                step_route = route + "/episode-migrations/{migration_id}"
                job = call("POST", step + "/continue" + q, step_route + "/continue", {"expected_version": job["row_version"]}, "migration-contract-reserve")
                assert job["state"] == "copying"
                assert call("POST", step + "/continue" + q, step_route + "/continue", {"expected_version": 1}, "migration-contract-reserve-replay")["row_version"] == job["row_version"]
                stop(); start()
                job = call("GET", step + q, step_route)
                job = call("POST", step + "/continue" + q, step_route + "/continue", {"expected_version": job["row_version"]}, "migration-contract-publish")
                assert job["state"] == "completed"
                target = project_call("GET", "/" + job["target_project_id"])
                assert target["migration_source"]["episode_number"] == 7 and len(target["script"]["scenes"]) == 1
                assert project_call("GET", suffix) == source
                target = project_call("PUT", "/" + target["id"], {"title": "Edited target", "brief": "A completed migration must not overwrite this edit.", "expected_version": target["version"]}, "migration-contract-edit-target")
                replay = call("POST", path + "/episode-migrations" + q, route + "/episode-migrations", create, "migration-contract-create", 201)
                assert replay["id"] == job["id"] and project_call("GET", "/" + target["id"]) == target
                call("GET", step + "?space_id=team-demo", step_route, expected=404)
                season = call("GET", "/api/v1/seasons/" + season["id"] + q, "/api/v1/seasons/{id}")
                data = dict(data, source_episode_number=120, target_ordinal=120, expected_season_version=season["row_version"])
                preview = call("POST", path + "/episode-migration-preview" + q, route + "/episode-migration-preview", data, "migration-contract-preview-stop")
                job = call("POST", path + "/episode-migrations" + q, route + "/episode-migrations", {"input": data, "preview_digest": preview["digest"]}, "migration-contract-create-stop", 201)
                step = path + "/episode-migrations/" + job["id"]
                job = call("POST", step + "/continue" + q, step_route + "/continue", {"expected_version": job["row_version"]}, "migration-contract-reserve-stop")
                job = call("POST", step + "/cancel" + q, step_route + "/cancel", {"expected_version": job["row_version"]}, "migration-contract-stop")
                assert job["state"] == "cancelled"
                project_call("GET", "/" + job["target_project_id"], expected=404)

            def check_history():
                project = project_call("POST", body={"title": "历史合同", "brief": "两位旧友在车站相遇并交换一本书。"}, key="history-create", expected=201)
                pid = project["id"]
                suffix = "/" + pid
                history_route = "/api/v1/projects/{id}/history"
                history_url = "/api/v1/projects/" + pid + "/history?space_id=personal-demo"
                assert call("GET", history_url, history_route) == []
                project_call("POST", suffix + "/generations", {"expected_version": 1}, "history-script", 202)
                ready = wait_script(pid)
                old = copy.deepcopy(ready["script"])
                edited = copy.deepcopy(old)
                edited["title"] = "手工修改后的历史合同"
                saved = project_call("PUT", suffix + "/script", {"expected_version": ready["version"], "script": edited}, "history-edit")
                revisions = call("GET", history_url, history_route)
                assert len(revisions) == 1 and revisions[0]["script"] == old
                rid = revisions[0]["id"]
                restore_route = "/api/v1/projects/{id}/history/{history_id}/restore"
                restore_url = "/api/v1/projects/" + pid + "/history/" + rid + "/restore?space_id=personal-demo"
                body = {"expected_version": saved["version"]}
                restored = call("POST", restore_url, restore_route, body, "history-restore")
                assert restored["script"] == old and restored["version"] == saved["version"] + 1
                assert restored["script_version"] == saved["script_version"] + 1
                assert call("POST", restore_url, restore_route, body, "history-restore") == restored
                assert call("POST", restore_url, restore_route, body, "history-stale", 409)["error"]["code"] == "version_conflict"
                changed = {"expected_version": restored["version"]}
                assert call("POST", restore_url, restore_route, changed, "history-restore", 409)["error"]["code"] == "idempotency_conflict"
                assert len(call("GET", history_url, history_route)) == 2
                call("GET", history_url.replace("personal-demo", "team-demo"), history_route, expected=404)
                stop(); start()
                assert call("POST", restore_url, restore_route, body, "history-restore") == restored
                assert len(call("GET", history_url, history_route)) == 2

            def check_storyboards():
                created = project_call("POST", body={"title": "分镜合同雨夜", "brief": "两位旧友在车站相遇，讨论一起举办摄影展。"}, key="board-project-create", expected=201)
                pid = created["id"]
                suffix = "/" + pid
                assert created["script_version"] == 0 and not created["storyboard_stale"]
                assert project_call("GET", suffix + "/storyboard-generations") == []
                sample = copy.deepcopy(spec["components"]["schemas"]["StoryboardShot"]["example"])
                missing = project_call("POST", suffix + "/storyboard-generations", {"expected_version": 1}, "board-no-script", 409)
                assert missing["error"]["code"] == "script_missing"
                missing = project_call("PUT", suffix + "/storyboard", {"expected_version": 1, "shots": [sample]}, "board-no-plan", 400)
                assert missing["error"]["code"] == "invalid_input"
                for method, tail, body in [("GET", "/storyboard-generations", None), ("POST", "/storyboard-generations", {"expected_version": 1}), ("PUT", "/storyboard", {"expected_version": 1, "shots": [sample]})]:
                    project_call(method, suffix + tail, body, "board-cross-space", 404, "team-demo")
                project_call("POST", suffix + "/generations", {"expected_version": 1}, "board-source-generate", 202)
                source = wait_script(pid)
                assert source["script_version"] == 1
                gen_body = {"expected_version": source["version"]}
                generation = project_call("POST", suffix + "/storyboard-generations", gen_body, "board-first-generate", 202)
                assert generation["provider"] == "mock-storyboard" and generation["source_script_version"] == 1
                assert "source_script" not in generation
                assert project_call("POST", suffix + "/storyboard-generations", gen_body, "board-first-generate")["id"] == generation["id"]
                # Accepted writes are blocked across both generation kinds while pending.
                active = project_call("GET", suffix)
                if active["latest_storyboard_generation"]["status"] in ["queued", "running"]:
                    blocked = project_call("PUT", suffix + "/script", {"expected_version": source["version"], "script": source["script"]}, "board-active-script", 409)
                    assert blocked["error"]["code"] == "generation_active"
                    blocked = project_call("POST", suffix + "/generations", gen_body, "board-active-generation", 409)
                    assert blocked["error"]["code"] == "generation_active"
                ready = wait_storyboard(pid)
                assert ready["latest_storyboard_generation"]["status"] == "succeeded"
                assert ready["version"] == source["version"] + 1 and ready["script_version"] == 1 and not ready["storyboard_stale"]
                assert ready["storyboard"]["source_script_version"] == 1 and ready["storyboard"]["shots"]
                shots = copy.deepcopy(ready["storyboard"]["shots"])
                shots[0].update(description="手工编辑后保留的镜头内容", duration_seconds=9, dialogue="", shot_size="close_up", camera_movement="pan")
                save_body = {"expected_version": ready["version"], "shots": shots}
                saved = project_call("PUT", suffix + "/storyboard", save_body, "board-save-first")
                assert saved["storyboard"]["shots"] == shots and saved["script_version"] == 1 and not saved["storyboard_stale"]
                assert project_call("PUT", suffix + "/storyboard", save_body, "board-save-first") == saved
                assert project_call("PUT", suffix + "/storyboard", save_body, "board-save-stale", 409)["error"]["code"] == "version_conflict"
                different = copy.deepcopy(save_body); different["shots"][0]["description"] = "同键不同内容"
                assert project_call("PUT", suffix + "/storyboard", different, "board-save-first", 409)["error"]["code"] == "idempotency_conflict"
                for index, change in enumerate([{"scene_index": -1}, {"scene_index": 4}, {"duration_seconds": 0}, {"duration_seconds": 61}, {"duration_seconds": 1.5}, {"shot_size": "other"}, {"camera_movement": "other"}, {"description": ""}, {"description": "长" * 1001}, {"dialogue": "长" * 2001}, {"dialogue": None}, {"id": "invalid id"}]):
                    invalid = copy.deepcopy(shots); invalid[0].update(change)
                    project_call("PUT", suffix + "/storyboard", {"expected_version": saved["version"], "shots": invalid}, f"board-invalid-shot-{index}", 400)
                for index, invalid_shots in enumerate([[], [sample] * 121, [sample, sample], None]):
                    project_call("PUT", suffix + "/storyboard", {"expected_version": saved["version"], "shots": invalid_shots}, f"board-invalid-list-{index}", 400)
                project_call("PUT", suffix + "/storyboard", dict(save_body, expected_version=saved["version"], source_script_version=1), "board-forged-source", 400)
                missing_dialogue = copy.deepcopy(shots); del missing_dialogue[0]["dialogue"]
                project_call("PUT", suffix + "/storyboard", {"expected_version": saved["version"], "shots": missing_dialogue}, "board-missing-dialogue", 400)
                for index, invalid in enumerate([{}, {"expected_version": 0}, {"expected_version": None}, {"expected_version": "1"}, {"expected_version": saved["version"], "source_script": source["script"]}]):
                    project_call("POST", suffix + "/storyboard-generations", invalid, f"board-invalid-gen-{index}", 400)
                project_call("PUT", suffix + "/storyboard", {"expected_version": saved["version"], "shots": shots}, expected=400)
                # Project brief edits do not change script revision or mark plan stale.
                revised = project_call("PUT", suffix, {"expected_version": saved["version"], "title": saved["title"], "brief": "已更新需求，但尚未修改已保存剧本。"}, "board-update-brief")
                assert revised["script_version"] == 1 and not revised["storyboard_stale"]
                script = copy.deepcopy(revised["script"])
                script["scenes"][0]["action"] = "[mock-fail] 保存的新剧本触发模拟分镜失败。"
                stale = project_call("PUT", suffix + "/script", {"expected_version": revised["version"], "script": script}, "board-change-script")
                assert stale["script_version"] == 2 and stale["storyboard_stale"] and stale["storyboard"] == saved["storyboard"]
                stale_saved = project_call("PUT", suffix + "/storyboard", {"expected_version": stale["version"], "shots": shots}, "board-save-old-plan")
                assert stale_saved["storyboard_stale"] and stale_saved["storyboard"]["source_script_version"] == 1
                project_call("POST", suffix + "/storyboard-generations", {"expected_version": stale_saved["version"]}, "board-fail-generate", 202)
                failed = wait_storyboard(pid)
                assert failed["latest_storyboard_generation"]["error_code"] == "mock_storyboard_failed"
                assert failed["version"] == stale_saved["version"] and failed["storyboard"] == stale_saved["storyboard"] and failed["storyboard_stale"]
                # Restart proves persisted source revision, edits, failure and old receipts.
                stop(); start()
                assert project_call("GET", suffix) == failed
                assert project_call("PUT", suffix + "/storyboard", save_body, "board-save-first") == failed
                assert project_call("POST", suffix + "/storyboard-generations", gen_body, "board-first-generate")["id"] == generation["id"]
                assert len(project_call("GET", suffix + "/storyboard-generations")) == 2
                script["scenes"][0]["action"] = "旧友在车站重新握手，准备共同举办摄影展。"
                recovered_source = project_call("PUT", suffix + "/script", {"expected_version": failed["version"], "script": script}, "board-fix-source")
                project_call("POST", suffix + "/storyboard-generations", {"expected_version": recovered_source["version"]}, "board-recover-generate", 202)
                final = wait_storyboard(pid)
                assert not final["storyboard_stale"] and final["storyboard"]["source_script_version"] == 3
                assert final["version"] == recovered_source["version"] + 1

            def check_projects():
                assert project_call("GET") == []
                project_call("GET", space="invalid", expected=400)
                initial = {"title": "  Contract 雨夜来信  ", "brief": "  两名旧友因一封旧信在雨夜车站重逢，决定重新合作。  "}
                # The project domain intentionally does not consume image keys.
                created = project_call("POST", body=initial, key="contract-original", expected=201)
                project_id = created["id"]
                suffix = "/" + project_id
                assert created["version"] == 1 and created["title"] == initial["title"].strip()
                assert "script" not in created and "script_brief" not in created and "latest_generation" not in created
                assert project_call("GET", suffix) == created
                assert project_call("GET", suffix + "/generations") == []
                assert project_call("POST", body={k: v.strip() for k, v in initial.items()}, key="contract-original") == created
                conflict = project_call("POST", body=dict(initial, title="不同名称"), key="contract-original", expected=409)
                assert conflict["error"]["code"] == "idempotency_conflict"
                for index, invalid in enumerate([
                    {"title": "", "brief": "有效的创意需求"},
                    {"title": None, "brief": "有效的创意需求"},
                    {"title": "有效名称", "brief": None},
                    {"title": "有效名称", "brief": "短"},
                    {"title": "有效名称", "brief": "长" * 2001},
                    {"title": "有效名称"},
                    {"title": "有效名称", "brief": "有效的创意需求", "provider": "openai-images"},
                ]):
                    project_call("POST", body=invalid, key=f"project-invalid-{index}", expected=400)
                project_call("POST", body=initial, expected=400)
                project_call("POST", body=initial, key="bad", expected=400)
                project_call("GET", "/" + "0" * 32, expected=404)
                sample = copy.deepcopy(spec["components"]["schemas"]["Script"]["example"])
                for method, tail, body in [
                    ("GET", "", None), ("GET", "/generations", None),
                    ("PUT", "", dict(initial, expected_version=1)),
                    ("POST", "/generations", {"expected_version": 1}),
                    ("PUT", "/script", {"expected_version": 1, "script": sample}),
                ]:
                    project_call(method, suffix + tail, body, "project-cross-space", 404, "team-demo")
                manual = project_call("POST", body={"title": "Manual script", "brief": "A manually authored short film."}, key="manual-project-create", expected=201)
                authored = project_call("PUT", "/" + manual["id"] + "/script", {"expected_version": 1, "script": sample}, "manual-project-script")
                assert authored["script"] == sample and authored["script_provider"] == "manual"
                for index, invalid in enumerate([{}, {"expected_version": 0}, {"expected_version": None}, {"expected_version": "1"}, {"expected_version": 1, "brief": "不能绕过已保存需求"}]):
                    project_call("POST", suffix + "/generations", invalid, f"project-invalid-gen-{index}", 400)
                generation_body = {"expected_version": 1}
                attempt = project_call("POST", suffix + "/generations", generation_body, "project-generate-first", 202)
                assert attempt["provider"] == "mock-script" and attempt["source_brief"] == created["brief"]
                assert attempt["base_version"] == 1 and attempt["project_id"] == project_id
                assert project_call("POST", suffix + "/generations", generation_body, "project-generate-first")["id"] == attempt["id"]
                ready = wait_script(project_id)
                assert ready["version"] == 2 and ready["latest_generation"]["status"] == "succeeded"
                assert ready["script_brief"] == created["brief"]
                assert len(ready["script"]["characters"]) == 2 and len(ready["script"]["scenes"]) == 3
                assert project_call("POST", suffix + "/generations", generation_body, "project-generate-first")["status"] == "succeeded"
                for method, tail, body in [
                    ("PUT", "", dict(initial, expected_version=1)),
                    ("POST", "/generations", {"expected_version": 1}),
                    ("PUT", "/script", {"expected_version": 1, "script": ready["script"]}),
                ]:
                    rejected = project_call(method, suffix + tail, body, "project-stale-version", 409)
                    assert rejected["error"]["code"] == "version_conflict"
                invalid_script = copy.deepcopy(ready["script"])
                invalid_script["scenes"][0]["dialogue"][0]["character"] = "不存在的角色"
                project_call("PUT", suffix + "/script", {"expected_version": 2, "script": invalid_script}, "project-invalid-ref", 400)
                project_call("PUT", suffix + "/script", {"expected_version": 2, "script": None}, "project-null-script", 400)
                project_call("PUT", suffix + "/script", {"expected_version": 2, "script": ready["script"], "script_brief": "伪造来源"}, "project-false-source", 400)
                edited = copy.deepcopy(ready["script"])
                edited["scenes"][0]["action"] = "手工保存的动作内容，刷新和重启后仍应保留。"
                save_body = {"expected_version": 2, "script": edited}
                saved = project_call("PUT", suffix + "/script", save_body, "project-save-draft")
                assert saved["version"] == 3 and saved["script"] == edited and saved["script_brief"] == created["brief"]
                assert project_call("PUT", suffix + "/script", save_body, "project-save-draft") == saved
                assert project_call("POST", body=initial, key="contract-original") == saved
                changed_save = copy.deepcopy(save_body)
                changed_save["script"]["title"] = "同键不能偷偷修改"
                assert project_call("PUT", suffix + "/script", changed_save, "project-save-draft", 409)["error"]["code"] == "idempotency_conflict"
                failure_update = {"title": created["title"], "brief": "[mock-fail] 故障路径必须保留已经编辑的旧稿", "expected_version": 3}
                updated = project_call("PUT", suffix, failure_update, "project-update-failure")
                assert updated["version"] == 4 and updated["script"] == edited and updated["script_brief"] == created["brief"]
                assert project_call("PUT", suffix, failure_update, "project-update-failure") == updated
                failed_attempt = project_call("POST", suffix + "/generations", {"expected_version": 4}, "project-generate-failure", 202)
                failed = wait_script(project_id)
                assert failed["version"] == 4 and failed["script"] == edited and failed["script_brief"] == created["brief"]
                assert failed["latest_generation"]["id"] == failed_attempt["id"]
                assert failed["latest_generation"]["error_code"] == "mock_script_failed"
                assert project_call("POST", suffix + "/generations", {"expected_version": 4}, "project-generate-failure")["status"] == "failed"
                recovery = {"title": created["title"], "brief": "旧友决定在清晨车站共同举办摄影展。", "expected_version": 4}
                assert project_call("PUT", suffix, recovery, "project-update-recovery")["version"] == 5
                project_call("POST", suffix + "/generations", {"expected_version": 5}, "project-generate-recovery", 202)
                final_project = wait_script(project_id)
                assert final_project["version"] == 6 and final_project["script_brief"] == recovery["brief"]
                # Old accepted receipts return current projection, never roll back.
                assert project_call("PUT", suffix + "/script", save_body, "project-save-draft") == final_project
                assert project_call("POST", body=initial, key="contract-original") == final_project
                assert project_call("POST", suffix + "/generations", generation_body, "project-generate-first")["id"] == attempt["id"]
                attempts = project_call("GET", suffix + "/generations")
                assert len(attempts) == 3 and len({a["id"] for a in attempts}) == 3
                assert project_call("GET", space="team-demo") == []
                other = project_call("POST", body={"title": "另一个项目", "brief": "另一个项目的完整创意需求"}, key="project-second-create", expected=201)
                crossed = project_call("PUT", "/" + other["id"], {"title": "目标错位", "brief": "另一个项目的新需求", "expected_version": 1}, "project-save-draft", 409)
                assert crossed["error"]["code"] == "idempotency_conflict"
                return {"id": project_id, "final": final_project, "initial": initial, "save": save_body, "attempt": attempt}

            def check_series():
                q = '?space_id=personal-demo'
                profile = dict(duration_seconds=120, output_resolution='2160p', aspect_ratio='9:16')
                initial = dict(title='合成系列合同', summary='逐集独立创作并保留过去的成片。', profile=profile)
                root = call('POST', '/api/v1/series'+q, '/api/v1/series', initial, 'series-root-contract', 201)
                season = call('POST', '/api/v1/series/'+root['id']+'/seasons'+q, '/api/v1/series/{id}/seasons', {}, 'series-season-contract', 201)
                episodes=[]
                for index in range(21):
                    episodes.append(call('POST', '/api/v1/seasons/'+season['id']+'/episodes'+q, '/api/v1/seasons/{id}/episodes', dict(title='合成剧集'+str(index+1)), 'series-episode-'+str(index), 201))
                episode=episodes[0]
                directory='/api/v1/seasons/'+season['id']+'/episodes'
                page=call('GET', directory+q, '/api/v1/seasons/{id}/episodes')
                assert len(page['items'])==20 and page['next_cursor']
                tail=call('GET', directory+q+'&cursor='+page['next_cursor'], '/api/v1/seasons/{id}/episodes')
                assert len(tail['items'])==1 and tail['items'][0]['ordinal']==21 and not tail['next_cursor']
                call('GET', directory+q+'&status=planned&cursor='+page['next_cursor'], '/api/v1/seasons/{id}/episodes', expected=400)
                call('GET', '/api/v1/series'+q+'&cursor='+page['next_cursor'], '/api/v1/series', expected=400)
                detail=call('GET', '/api/v1/episodes/'+episode['id']+q, '/api/v1/episodes/{id}')
                assert detail['episode']['profile']==profile and detail['season']['id']==season['id']
                call('GET', '/api/v1/episodes/'+episode['id']+'?space_id=team-demo', '/api/v1/episodes/{id}', expected=404)
                project=call('GET','/api/v1/projects/'+episode['project_id']+q,'/api/v1/projects/{id}')
                assert project['series_episode_id']==episode['id'] and not project.get('script')
                media=call('GET','/api/v1/projects/'+project['id']+'/media'+q,'/api/v1/projects/{id}/media')
                assert media['timeline']['output_resolution']=='2160p' and media['timeline']['aspect_ratio']=='9:16'
                changed=call('PUT','/api/v1/projects/'+project['id']+q,'/api/v1/projects/{id}',dict(title='目录显示当前项目标题',brief='目录不能回写或展示过期创建种子。',expected_version=1),'series-project-title')
                detail=call('GET','/api/v1/episodes/'+episode['id']+q,'/api/v1/episodes/{id}')
                assert detail['episode']['title']==changed['title'] and detail['episode']['project_version']==2
                parent=call('GET','/api/v1/seasons/'+season['id']+q,'/api/v1/seasons/{id}')
                moved=call('POST','/api/v1/seasons/'+season['id']+'/episode-order'+q,'/api/v1/seasons/{id}/episode-order',dict(id=episode['id'],ordinal=7,expected_version=1,expected_parent_version=parent['row_version']),'series-move-seven')
                assert moved['ordinal']==7
                latest=call('GET','/api/v1/series/'+root['id']+q,'/api/v1/series/{id}')
                call('PATCH','/api/v1/series/'+root['id']+q,'/api/v1/series/{id}',dict(**initial,expected_version=latest['row_version'],status='archived'),'series-archive-contract')
                call('POST','/api/v1/seasons/'+season['id']+'/episodes'+q,'/api/v1/seasons/{id}/episodes',dict(title='归档后不能新增'),'series-after-archive',409)
                stop(); start()
                restored=call('GET','/api/v1/episodes/'+episode['id']+q,'/api/v1/episodes/{id}')
                assert restored['episode']['ordinal']==7 and restored['episode']['title']==changed['title']

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
                project_case = check_projects()
                check_storyboards()
                check_history()
                check_structure()
                check_creative()
                check_library()
                check_series()
                check_episode_migrations()
                stop()
                configurations = [
                    ("openai-images", "", "missing_api_key"),
                    ("bad-synthetic-secret-provider", "", "unsupported_provider"),
                    ("openai-images", "synthetic\ninvalid", "missing_api_key"),
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
                    project_suffix = "/" + project_case["id"]
                    assert project_call("GET", project_suffix) == project_case["final"]
                    assert project_call("POST", body=project_case["initial"], key="contract-original") == project_case["final"]
                    assert project_call("PUT", project_suffix + "/script", project_case["save"], "project-save-draft") == project_case["final"]
                    assert project_call("POST", project_suffix + "/generations", {"expected_version": 1}, "project-generate-first")["id"] == project_case["attempt"]["id"]
                    if reason == "missing_api_key" and key == "":
                        independent = project_call("POST", body={"title": "独立模拟剧本", "brief": "图像配置不可用时仍能生成结构化模拟剧本"}, key="project-image-independent", expected=201)
                        project_call("POST", "/" + independent["id"] + "/generations", {"expected_version": 1}, "project-independent-run", 202)
                        assert wait_script(independent["id"])["latest_generation"]["status"] == "succeeded"
                    stop()
                log.flush()
                log.seek(0)
                recorded_log = log.read()
                snapshot = (temp / "data/state.json").read_text()
                for sensitive in ["bad-synthetic-secret-provider", "synthetic\ninvalid", "synthetic\\ninvalid"]:
                    assert sensitive not in recorded_log and sensitive not in snapshot
                print(f"PASS: {count} actual HTTP responses conform to OpenAPI schemas (including polling).")
                print("Verified: catalog/defaults, capability states, atomic provider guards, invalid expectation pairs, accepted-key replay before readiness/expectations, 503 without queueing, task/asset metadata, persistent search and historical results across restarts.")
                print("Verified: real project create/detail/edit, Eino structured mock script, 400/404/409 paths, version/source preservation, failed generation keeps draft, all-write accepted-key replay across restarts, and script execution independent of unavailable image configuration.")
                print("Verified: storyboard mock generation/editing, 400/404/409 guards, source script revision/staleness, failed generation keeps old plan, persisted receipts across restart and successful regeneration clears staleness.")
                print("Verified: immutable script history restoration, monotonic versions, replay/conflict/cross-space/restart guards; asynchronous brief analysis, provider registry and creative job persistence.")
                print("Verified: episode migration preview, metadata-only progress lists, hidden preparations, restart, local episode remapping, unchanged source, accepted receipt replay and cancellation.")
                print("Verified: explicit stable identity upgrade, rename/reorder, old-client write refusal, scope isolation and original receipt replay after restart.")
                print("Verified: library card versions, style/voice intent and >20 constraints without quotas, distinct types and image-not-voice refusal, ordered history, cross-space isolation, pinned project bindings, explicit required authorization revision/CAS/current revocation and original receipts across archive and restart.")
                print("No real credential was read and no external provider was enabled; all runtime data and processes were temporary.")
            finally:
                stop()


if __name__ == "__main__":
    main()
