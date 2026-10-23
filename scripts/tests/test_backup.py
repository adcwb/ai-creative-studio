import importlib.util
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('backup', Path(__file__).parents[1] / 'backup.py')
backup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backup)

class BackupTests(unittest.TestCase):
    def test_roundtrip_and_no_overwrite(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = root / 'data'
            data.mkdir()
            (data / 'state.json').write_text('{"version":3}')
            (data / 'media').mkdir()
            (data / 'media' / 'test.bin').write_bytes(bytes(range(256)))
            archive = root / 'backup.tar.gz'
            self.assertEqual(backup.backup(data, archive)['files'], 2)
            self.assertTrue(backup.verify(archive)['verified'])
            restored = root / 'restored'
            backup.restore(archive, restored)
            self.assertEqual((restored / 'media' / 'test.bin').read_bytes(), bytes(range(256)))
            self.assertEqual((restored / 'state.json').stat().st_mode & 0o777, 0o600)
            with self.assertRaises(ValueError):
                backup.restore(archive, restored)
            with self.assertRaises(ValueError):
                backup.backup(data, archive)

    def test_running_store_and_symlinks_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            data = root / 'data'
            data.mkdir()
            (data / 'state.json').write_text('{}')
            with backup.locked(data):
                with self.assertRaises(ValueError):
                    backup.backup(data, root / 'a.tar.gz')
            (data / 'link').symlink_to('/etc/passwd')
            with self.assertRaises(ValueError):
                backup.backup(data, root / 'a.tar.gz')

    def test_corrupt_and_traversal_rejected(self):
        for name in ['../escape', '/absolute', 'state.json']:
            with self.subTest(name=name), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                archive = root / 'bad.tar.gz'
                with tarfile.open(archive, 'w:gz') as tar:
                    manifest = json.dumps({'version': 1, 'files': {name: {'size': 2, 'sha256': 'wrong'}}}).encode()
                    for entry, content in [('backup-manifest.json', manifest), (name, b'{}')]:
                        info = tarfile.TarInfo(entry)
                        info.size = len(content)
                        tar.addfile(info, io.BytesIO(content))
                with self.assertRaises(ValueError):
                    backup.restore(archive, root / 'restore')
                self.assertFalse((root / 'restore').exists())

if __name__ == '__main__':
    unittest.main()
