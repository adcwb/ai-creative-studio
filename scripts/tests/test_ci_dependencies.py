import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
STUB = '''#!/usr/bin/python3
import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
if name == 'lsb_release':
    print(os.environ.get('TEST_OS', 'Ubuntu') if sys.argv[-1] == '-is' else '24.04')
elif name == 'dpkg-query':
    if sys.argv[-1] not in os.environ.get('TEST_INSTALLED', '').split(','):
        sys.exit(1)
    print('installed', end='')
elif name == 'sudo':
    with open(os.environ['TEST_APT_LOG'], 'a') as log:
        log.write(json.dumps(sys.argv[1:]) + '\\n')
    if os.environ.get('TEST_APT_FAIL') in sys.argv[1:]:
        sys.exit(37)
elif name == 'fc-match':
    print(os.environ.get('TEST_FONT', 'Noto Sans CJK SC'))
else:
    print(name + ' fixture')
'''


class CIDependenciesTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='studio-ci-dependencies-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        binary = self.root / 'bin'
        binary.mkdir()
        for name in ('lsb_release', 'dpkg-query', 'sudo', 'ffmpeg', 'ffprobe', 'fc-match', 'google-chrome'):
            path = binary / name
            path.write_text(STUB)
            path.chmod(0o700)
        self.log = self.root / 'apt.jsonl'
        self.env = dict(os.environ, PATH=str(binary) + os.pathsep + os.defpath,
                        RUNNER_TEMP=str(self.root), TEST_APT_LOG=str(self.log),
                        TEST_INSTALLED='ffmpeg,fonts-noto-cjk', STUDIO_CI_POSTGRES='false')

    def run_dependencies(self, **values):
        return subprocess.run(['/bin/bash', str(ROOT / 'scripts/ci_dependencies.sh')],
                              env=dict(self.env, **values), capture_output=True, text=True, timeout=10)

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def test_preinstalled_media_never_invokes_apt(self):
        result = self.run_dependencies()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls(), [])

    def test_only_missing_packages_use_signed_https_sources_and_bounded_network(self):
        result = self.run_dependencies(TEST_INSTALLED='ffmpeg')
        self.assertEqual(result.returncode, 0, result.stderr)
        update, install = self.calls()
        self.assertEqual(update[-1], 'update')
        self.assertEqual(install[-4:], ['install', '-y', '--no-install-recommends', 'fonts-noto-cjk'])
        for call in (update, install):
            self.assertIn('Acquire::Retries=2', call)
            self.assertIn('Acquire::http::Timeout=30', call)
            self.assertIn('Acquire::https::Timeout=30', call)
            self.assertIn('APT::Update::Error-Mode=any', call)
        sources = (self.root / 'studio-apt/ubuntu.sources').read_text()
        self.assertIn('https://archive.ubuntu.com/ubuntu/', sources)
        self.assertIn('https://security.ubuntu.com/ubuntu/', sources)
        self.assertEqual(sources.count('Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg'), 2)
        self.assertNotIn('trusted=yes', sources)

    def test_preinstalled_postgres_is_reused(self):
        result = self.run_dependencies(STUDIO_CI_POSTGRES='true',
                                       TEST_INSTALLED='ffmpeg,fonts-noto-cjk,postgresql-16,postgresql-client-16')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls(), [])

    def test_update_failure_stops_before_install(self):
        result = self.run_dependencies(TEST_INSTALLED='', TEST_APT_FAIL='update')
        self.assertEqual(result.returncode, 37)
        self.assertEqual(len(self.calls()), 1)

    def test_install_failure_is_not_success(self):
        result = self.run_dependencies(TEST_INSTALLED='', TEST_APT_FAIL='install')
        self.assertEqual(result.returncode, 37)
        self.assertEqual(len(self.calls()), 2)

    def test_missing_chinese_font_fails_even_if_package_claims_installed(self):
        result = self.run_dependencies(TEST_FONT='DejaVu Sans')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Chinese font unavailable', result.stderr)

    def test_wrong_runner_and_invalid_selection_are_rejected(self):
        for values in ({'TEST_OS': 'Debian'}, {'STUDIO_CI_POSTGRES': 'invalid'}):
            with self.subTest(values=values):
                self.assertNotEqual(self.run_dependencies(**values).returncode, 0)
                self.assertEqual(self.calls(), [])


if __name__ == '__main__':
    unittest.main()
