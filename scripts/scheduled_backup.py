#!/usr/bin/env python3
"""Offline backup runner. Operator hooks must stop/verify ALL API and workers.
No hooks are supplied or installed automatically. Never invokes a shell.
"""
import argparse
import datetime
import fcntl
import stat
import os
from pathlib import Path
import subprocess
import sys
import uuid


def run(args):
    if getattr(args, "hook_timeout", 300) <= 0 or getattr(args, "backup_timeout", 3600) <= 0:
        raise ValueError("positive timeouts required")
    for executable in (args.pre_hook, args.post_hook, args.binary):
        if not Path(executable).is_absolute() or not os.access(executable, os.X_OK):
            raise ValueError("explicit absolute executable paths required")
    root = Path(args.destination_root)
    if not root.is_dir() or root.is_symlink():
        raise ValueError("existing private backup destination root required")
    if root.stat().st_mode & 0o077:
        raise ValueError("backup root must be private (0700)")
    descriptor = os.open(root / ".scheduler.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    try:
        if not stat.S_ISREG(os.fstat(descriptor).st_mode):
            raise ValueError("unsafe scheduler lock")
        fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
        return locked_run(args, root)
    finally:
        os.close(descriptor)


def locked_run(args, root):
    destination = root / (datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ-") + uuid.uuid4().hex)
    # Hooks receive no database/provider credentials. The operator defines the
    # exact local service units and must make a failed pre-hook leave safe state.
    hook_env = {"PATH": "/usr/bin:/bin"}
    subprocess.run([args.pre_hook], env=hook_env, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=getattr(args, "hook_timeout", 300))
    try:
        subprocess.run([args.binary, "-mode=backup", "-confirm-stopped", "-destination=" + str(destination), "-pg-dump=" + args.pg_dump], check=True, timeout=getattr(args, "backup_timeout", 3600))
    finally:
        subprocess.run([args.post_hook], env=hook_env, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=getattr(args, "hook_timeout", 300))
    return destination


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("pre-hook", "post-hook", "binary", "destination-root"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--pg-dump", default="pg_dump")
    parser.add_argument("--hook-timeout", type=float, default=300)
    parser.add_argument("--backup-timeout", type=float, default=3600)
    try:
        print(run(parser.parse_args()))
    except Exception:
        print("scheduled backup failed; inspect hook/service state and staging; no automatic retry", file=sys.stderr)
        sys.exit(1)
