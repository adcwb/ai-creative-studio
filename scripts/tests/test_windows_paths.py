import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('windows_paths', Path(__file__).resolve().parents[1] / 'check_windows_paths.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class WindowsPathsTest(unittest.TestCase):
    def test_portable_unicode_and_spaces(self):
        with tempfile.TemporaryDirectory() as root:
            Path(root, "创作 space's").mkdir()
            Path(root, "创作 space's", 'state.json').write_text('preserve')
            self.assertEqual(module.inspect(root), [])

    def test_names_collisions_and_symlink_are_read_only(self):
        with tempfile.TemporaryDirectory() as root:
            for name in ('CON.txt', 'com¹', 'name.', 'A', 'a'):
                Path(root, name).write_text('preserve')
            Path(root, 'link').symlink_to(Path(root, 'A'))
            reasons = {item['reason'] for item in module.inspect(root)}
            self.assertTrue({'unrepresentable_name', 'case_collision', 'link_or_reparse'} <= reasons)
            self.assertEqual(Path(root, 'CON.txt').read_text(), 'preserve')

    def test_missing_is_safely_classified(self):
        self.assertEqual(module.inspect('/nonexistent-studio-migration-fixture')[0]['reason'], 'directory_unavailable')
