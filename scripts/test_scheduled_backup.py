import argparse
import fcntl
import os
import importlib.util
from pathlib import Path
import tempfile
import unittest
import json
import subprocess
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("scheduler", Path(__file__).with_name("scheduled_backup.py"))
scheduler = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scheduler)


class SchedulerTest(unittest.TestCase):
    def exercise(self, pre_code, backup_code):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        log = root / "steps"
        def executable(name, code):
            p = root / name
            p.write_text("#!/bin/sh\nprintf '%s\\n' " + name + " >> '" + str(log) + "'\nexit " + str(code) + "\n")
            p.chmod(0o700)
            return str(p)
        args = argparse.Namespace(pre_hook=executable("pre", pre_code), post_hook=executable("post", 0), binary=executable("backup", backup_code), destination_root=str(root), pg_dump="pg_dump")
        try:
            scheduler.run(args)
        except Exception:
            pass
        return log.read_text().splitlines()

    def test_success(self):
        self.assertEqual(self.exercise(0, 0), ["pre", "backup", "post"])

    def test_backup_failure_resumes(self):
        self.assertEqual(self.exercise(0, 1), ["pre", "backup", "post"])

    def test_failed_stop_never_backs_up(self):
        self.assertEqual(self.exercise(1, 0), ["pre"])

    def test_concurrent_runner_rejected_before_hook(self):
        with tempfile.TemporaryDirectory() as directory:
            lock = os.open(Path(directory) / ".scheduler.lock", os.O_CREAT | os.O_RDWR, 0o600)
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                args = argparse.Namespace(pre_hook="/bin/true", post_hook="/bin/true", binary="/bin/true", destination_root=directory, pg_dump="pg_dump")
                with self.assertRaises(BlockingIOError):
                    scheduler.run(args)
            finally:
                os.close(lock)

    def test_encrypted_success_removes_only_created_plaintext(self):
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as encrypted:
            root = Path(directory)
            unrelated = root / "keep"
            unrelated.mkdir()
            archive_name = "studio-backup-" + "a" * 32 + ".sbk"
            actions = []
            def execute(argv, **kwargs):
                actions.append(argv[0] if len(argv) == 1 else argv[1])
                if "-mode=backup" in argv:
                    destination = Path(next(v.split("=", 1)[1] for v in argv if v.startswith("-destination=")))
                    destination.mkdir()
                if "-mode=seal" in argv:
                    (Path(encrypted) / archive_name).write_bytes(b"synthetic envelope")
                    return subprocess.CompletedProcess(argv, 0, json.dumps({"verified": True, "file": archive_name}).encode())
                return subprocess.CompletedProcess(argv, 0)
            args = argparse.Namespace(pre_hook="/bin/true", post_hook="/bin/true", binary="/bin/true", destination_root=directory, encrypted_root=encrypted, pg_dump="pg_dump")
            with patch.object(scheduler.subprocess, "run", side_effect=execute):
                result = scheduler.run(args)
            self.assertEqual(result, Path(encrypted) / archive_name)
            self.assertEqual(actions, ["/bin/true", "-mode=backup", "-mode=seal", "/bin/true"])
            self.assertTrue(unrelated.is_dir())
            self.assertEqual(sorted(p.name for p in root.iterdir()), [".scheduler.lock", "keep"])

    def test_encryption_failure_preserves_plaintext_and_resumes(self):
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as encrypted:
            actions = []
            def execute(argv, **kwargs):
                actions.append(argv[0] if len(argv) == 1 else argv[1])
                if "-mode=backup" in argv:
                    Path(next(v.split("=", 1)[1] for v in argv if v.startswith("-destination="))).mkdir()
                if "-mode=seal" in argv:
                    raise subprocess.CalledProcessError(1, argv)
                return subprocess.CompletedProcess(argv, 0)
            args = argparse.Namespace(pre_hook="/bin/true", post_hook="/bin/true", binary="/bin/true", destination_root=directory, encrypted_root=encrypted, pg_dump="pg_dump")
            with patch.object(scheduler.subprocess, "run", side_effect=execute), self.assertRaises(subprocess.CalledProcessError):
                scheduler.run(args)
            self.assertEqual(actions[-1], "/bin/true")
            self.assertEqual(len([p for p in Path(directory).iterdir() if p.is_dir()]), 1)

    def test_symlink_lock_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            (Path(directory) / ".scheduler.lock").symlink_to("/dev/null")
            args = argparse.Namespace(pre_hook="/bin/true", post_hook="/bin/true", binary="/bin/true", destination_root=directory, pg_dump="pg_dump")
            with self.assertRaises(OSError):
                scheduler.run(args)


if __name__ == "__main__":
    unittest.main()
