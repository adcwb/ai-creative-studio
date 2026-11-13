import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('native_backend', Path(__file__).resolve().parents[1] / 'native_backend_check.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class NativeBackendGuardTest(unittest.TestCase):
    def test_loopback_dedicated_database(self):
        value = 'postgresql://synthetic@127.0.0.1/studio_native_test?sslmode=disable'
        self.assertEqual(module.disposable_dsn(value), value)

    def test_remote_or_production_refused(self):
        for value in ('postgresql://user@db.example/studio_native_test', 'postgresql://user@localhost/production'):
            with self.assertRaises(ValueError):
                module.disposable_dsn(value)

    def test_target_override_refused(self):
        for key in ('host', 'hostaddr', 'service', 'dbname'):
            with self.assertRaises(ValueError):
                module.disposable_dsn('postgresql://user@localhost/studio_native_test?' + key + '=elsewhere')
