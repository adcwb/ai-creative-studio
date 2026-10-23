"""Container integration must never inherit account credentials or remote daemons."""
import contextlib
import importlib.util
import io
import os
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch

MODULE = Path(__file__).resolve().parents[1] / 's3_integration.py'
spec = importlib.util.spec_from_file_location('s3_fixture', MODULE)
s3_fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(s3_fixture)


class ContainerEnvironmentTest(unittest.TestCase):
    def test_s3_every_docker_operation_uses_empty_local_config(self):
        docker_calls = []
        def run(args, **kwargs):
            if args[0] == 'docker':
                env = kwargs['env']
                self.assertEqual(set(env), {'PATH', 'DOCKER_CONFIG'})
                self.assertEqual(list(Path(env['DOCKER_CONFIG']).iterdir()), [])
                docker_calls.append(tuple(args[1:3]))
                return SimpleNamespace(stdout='127.0.0.1:19999\n')
            self.assertEqual(args[0], 'go')
            self.assertNotIn('OIDC_CLIENT_SECRET', kwargs['env'])
            return SimpleNamespace(stdout='')
        response = contextlib.nullcontext(SimpleNamespace(status=200))
        opener = SimpleNamespace(open=lambda *args, **kwargs: response)
        synthetic_parent = {'PATH': '/usr/bin', 'DOCKER_CONFIG': '/never/read/account',
                            'DOCKER_HOST': 'tcp://never-contact.invalid', 'DOCKER_AUTH_CONFIG': 'synthetic',
                            'OIDC_CLIENT_SECRET': 'synthetic', 'MODEL_CONFIG_KEK_BASE64': 'synthetic'}
        with patch.dict(os.environ, synthetic_parent, clear=True), patch('sys.argv', ['s3', '--image', 'synthetic-local-fixture']), patch.object(s3_fixture.subprocess, 'run', side_effect=run), patch.object(s3_fixture.urllib.request, 'build_opener', return_value=opener), contextlib.redirect_stdout(io.StringIO()):
            s3_fixture.main()
        self.assertEqual([call[0] for call in docker_calls], ['run', 'port', 'rm'])


if __name__ == '__main__':
    unittest.main()
