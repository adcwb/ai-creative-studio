"""Exercise the actual CI launchers: reject invalid input and retain all groups.

Subprocess fixtures record invocations only; application/Playwright acceptance
still runs against real isolated backends in the corresponding CI matrix jobs.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class CIPartitionsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='studio-ci-partitions-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.log = self.root / 'invocations.jsonl'
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        self.env = dict(os.environ, PATH=str(self.bin) + os.pathsep + os.defpath,
                        CI_INVOCATIONS=str(self.log))
        for command in ('go', 'node'):
            path = self.bin / command
            path.write_text('#!/usr/bin/python3\nimport json,os,sys\n'
                            'with open(os.environ["CI_INVOCATIONS"],"a") as f: f.write(json.dumps(sys.argv[1:])+"\\n")\n')
            path.chmod(0o700)

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def launch(self, name, **values):
        return subprocess.run(['bash', str(ROOT / 'scripts' / name)], env=dict(self.env, **values),
                              capture_output=True, text=True, timeout=10)

    def test_go_groups_preserve_complementary_selection_and_race(self):
        for group in ('a-m', 'other', 'native'):
            result = self.launch('ci_go_tests.sh', STUDIO_CI_TEST_GROUP=group)
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls(), [
            ['test', '-count=1', '-race', './...', '-run', '^Test[A-M]'],
            ['test', '-count=1', '-race', './...', '-skip', '^(TestNative|Test[A-M])'],
            ['test', '-count=1', '-race', './...', '-run', '^TestNative'],
        ])
        self.assertNotEqual(self.launch('ci_go_tests.sh', STUDIO_CI_TEST_GROUP='unknown').returncode, 0)
        self.assertEqual(len(self.calls()), 3)

    def test_all_browser_suites_and_both_devices_keep_one_worker(self):
        for suite in ('e2e', 'routing', 'auth'):
            for device in ('desktop', 'mobile'):
                result = self.launch('ci_browser_tests.sh', STUDIO_CI_BROWSER_SUITE=suite, STUDIO_CI_BROWSER_DEVICE=device)
                self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.calls()
        self.assertEqual(len(calls), 6)
        self.assertEqual({call[3] for call in calls}, {'playwright.config.ts', 'playwright.routing.config.ts', 'playwright.auth.config.ts'})
        self.assertEqual({call[5] for call in calls}, {'desktop', 'mobile'})
        self.assertTrue(all(call[-1] == '--workers=1' for call in calls))
        for suite, device in [('unknown', 'desktop'), ('auth', 'unknown')]:
            self.assertNotEqual(self.launch('ci_browser_tests.sh', STUDIO_CI_BROWSER_SUITE=suite, STUDIO_CI_BROWSER_DEVICE=device).returncode, 0)
        self.assertEqual(len(self.calls()), 6)

    def test_gateway_selection_keeps_every_spec_in_its_own_process(self):
        # Run the real launcher using Node; replace only its child Playwright CLI.
        script = self.root / 'scripts/gateway_browser_check.mjs'
        script.parent.mkdir()
        shutil.copyfile(ROOT / 'scripts/gateway_browser_check.mjs', script)
        frontend = self.root / 'frontend'
        specs = frontend / 'e2e-gateway'
        specs.mkdir(parents=True)
        for name in ('first.spec.ts', 'second.spec.ts'):
            (specs / name).touch()
        cli = frontend / 'node_modules/@playwright/test/cli.js'
        cli.parent.mkdir(parents=True)
        cli.write_text('require("node:fs").appendFileSync(process.env.CI_INVOCATIONS, JSON.stringify(process.argv.slice(2))+"\\n")')
        env = dict(os.environ, CI_INVOCATIONS=str(self.log))
        env.pop('STUDIO_GATEWAY_DEVICE', None)
        node = shutil.which('node')
        result = subprocess.run([node, str(script)], env=env, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.calls()), 4)

        self.log.unlink()
        for device in ('desktop', 'mobile'):
            result = subprocess.run([node, str(script)], env=dict(env, STUDIO_GATEWAY_DEVICE=device), capture_output=True, text=True, timeout=10)
            self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.calls()), 4)
        self.assertEqual({tuple(call) for call in self.calls()}, {
            ('test', '--config', 'playwright.gateway.config.ts', '--project=' + device, spec)
            for device in ('desktop', 'mobile') for spec in ('first.spec.ts', 'second.spec.ts')
        })
        result = subprocess.run([node, str(script)], env=dict(env, STUDIO_GATEWAY_DEVICE='invalid'), capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 2)
        self.assertEqual(len(self.calls()), 4)

    def test_publish_gate_rejects_missing_acceptance(self):
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        gate = workflow.split('  verify:\n', 1)[1].split('  docker:\n', 1)[0]
        code = gate.split("python3 - <<'PYCODE'\n", 1)[1].split('          PYCODE', 1)[0]
        code = '\n'.join(line[10:] for line in code.splitlines())
        required = ('quality', 'go-race', 'integrations', 'browser-smoke', 'gateway-smoke', 'container-smoke', 'durable-smoke')
        results = {name: {'result': 'success'} for name in required}
        def check(values):
            return subprocess.run(['python3', '-c', code], env=dict(self.env, VALIDATION_RESULTS=json.dumps(values)), capture_output=True, text=True, timeout=10)
        self.assertEqual(check(results).returncode, 0)
        self.assertNotEqual(check({}).returncode, 0)
        for name in required:
            self.assertIn(name, gate)
            for outcome in ('failure', 'cancelled', 'skipped'):
                self.assertNotEqual(check(dict(results, **{name: {'result': outcome}})).returncode, 0)


if __name__ == '__main__':
    unittest.main()
