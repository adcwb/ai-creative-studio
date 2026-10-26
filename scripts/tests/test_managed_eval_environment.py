"""Regression for CI without exported Go cache paths; no credential inheritance."""
import importlib.util
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPTS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS))
spec = importlib.util.spec_from_file_location('managed_eval_environment_fixture', SCRIPTS / 'managed_eval_check.py')
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
sys.path.pop(0)


class ManagedEvalBuildEnvironmentTest(unittest.TestCase):
    def test_absent_or_empty_go_paths_have_private_fallbacks(self):
        for overrides in ({}, {'GOPATH': '', 'GOCACHE': ''}):
            with self.subTest(overrides=overrides), tempfile.TemporaryDirectory() as folder:
                parent = {'HOME': '/must-not-forward', 'OPENAI_API_KEY': 'synthetic',
                          'GOENV': '/must-not-read', 'GOPROXY': 'https://synthetic:credential@example.invalid',
                          **overrides}
                base = {'PATH': '/usr/bin', 'PGPASSFILE': '/dev/null', 'PGSERVICEFILE': '/dev/null'}
                with patch.dict(os.environ, parent, clear=True):
                    result = fixture.build_environment(base, Path(folder))
                self.assertEqual(result, {**base, 'GOENV': 'off', 'GOTOOLCHAIN': 'local',
                                         'GOPATH': folder + '/go-path', 'GOCACHE': folder + '/go-build'})
                self.assertEqual(base.keys(), {'PATH', 'PGPASSFILE', 'PGSERVICEFILE'})

    def test_explicit_cache_paths_remain_available_without_other_parent_values(self):
        paths = {'GOPATH': '/tmp/synthetic-go', 'GOCACHE': '/tmp/synthetic-cache',
                 'GOMODCACHE': '/tmp/synthetic-modules'}
        with patch.dict(os.environ, {**paths, 'MODEL_CONFIG_KEK_BASE64': 'synthetic',
                                     'GOTOOLCHAIN': 'auto'}, clear=True):
            result = fixture.build_environment({'PATH': '/usr/bin'}, Path('/tmp/evidence'))
        self.assertEqual(result, {'PATH': '/usr/bin', 'GOENV': 'off', 'GOTOOLCHAIN': 'local', **paths})


if __name__ == '__main__':
    unittest.main()
