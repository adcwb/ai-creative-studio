#!/usr/bin/env python3
"""Offline, checksummed whole-DATA_DIR backup/restore. Never overwrite a restore target."""
import argparse
import contextlib
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import tarfile
import tempfile

MAX_FILES = 100000
MAX_BYTES = 100 * 1024**3

@contextlib.contextmanager
def locked(directory):
    flags = os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW
    fd = os.open(directory / 'store.lock', flags, 0o600)
    try:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise ValueError('data directory is in use; stop every server/worker first') from exc
        yield
    finally:
        os.close(fd)

def digest_file(path):
    digest = hashlib.sha256()
    with path.open('rb') as file:
        for chunk in iter(lambda: file.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()

def backup(source, destination):
    source, destination = source.resolve(strict=True), destination.absolute()
    if not source.is_dir() or destination.exists() or source == destination or source in destination.parents:
        raise ValueError('source must be a directory; archive must be new and outside DATA_DIR')
    if not (source / 'state.json').is_file():
        raise ValueError('DATA_DIR has no state.json')
    with locked(source):
        records = {}
        total = 0
        for path in sorted(source.rglob('*')):
            if path.is_symlink() or (not path.is_dir() and not path.is_file()):
                raise ValueError('symlinks and special files are forbidden')
            if path.is_dir() or path.name == 'store.lock':
                continue
            name = path.relative_to(source).as_posix()
            if name == 'backup-manifest.json':
                raise ValueError('reserved manifest filename in data directory')
            size = path.stat().st_size
            total += size
            if total > MAX_BYTES or len(records) >= MAX_FILES:
                raise ValueError('backup exceeds resource bounds')
            records[name] = {'size': size, 'sha256': digest_file(path)}
        fd, temporary = tempfile.mkstemp(prefix='.backup-', dir=destination.parent)
        try:
            with os.fdopen(fd, 'wb') as output:
                with tarfile.open(fileobj=output, mode='w:gz') as archive:
                    raw = json.dumps({'version': 1, 'files': records}, sort_keys=True).encode()
                    info = tarfile.TarInfo('backup-manifest.json')
                    info.size, info.mode = len(raw), 0o600
                    archive.addfile(info, io.BytesIO(raw))
                    for name in records:
                        archive.add(source / name, arcname=name, recursive=False)
                output.flush()
                os.fsync(output.fileno())
            # Hard link is atomic and refuses replacement, even after a race.
            os.link(temporary, destination)
        finally:
            os.unlink(temporary)
    return {'files': len(records), 'bytes': total}

def inspect(archive):
    members = archive.getmembers()
    if len(members) > MAX_FILES + 1:
        raise ValueError('too many archive entries')
    names = set()
    total = 0
    for member in members:
        path = PurePosixPath(member.name)
        if (not member.isfile() or path.is_absolute() or '..' in path.parts or
                str(path) != member.name or member.name in names or '\\' in member.name):
            raise ValueError('unsafe or duplicate archive entry')
        total += member.size
        if total > MAX_BYTES:
            raise ValueError('archive exceeds resource bounds')
        names.add(member.name)
    manifest_member = archive.getmember('backup-manifest.json')
    if manifest_member.size > 32 * 1024 * 1024:
        raise ValueError('manifest too large')
    manifest = json.load(archive.extractfile(manifest_member))
    records = manifest.get('files')
    if manifest.get('version') != 1 or not isinstance(records, dict) or names != set(records) | {'backup-manifest.json'} or 'state.json' not in records:
        raise ValueError('invalid or incomplete manifest')
    for name, record in records.items():
        member = archive.getmember(name)
        digest = hashlib.sha256()
        with archive.extractfile(member) as stream:
            for chunk in iter(lambda: stream.read(1024 * 1024), b''):
                digest.update(chunk)
        if record != {'size': member.size, 'sha256': digest.hexdigest()}:
            raise ValueError('checksum/size mismatch: ' + name)
    return records

def verify(source):
    with tarfile.open(source, 'r:gz') as archive:
        records = inspect(archive)
    return {'files': len(records), 'verified': True}

def restore(source, destination):
    destination = destination.absolute()
    if destination.exists() or destination.is_symlink():
        raise ValueError('restore target already exists; use a new DATA_DIR')
    temporary = Path(tempfile.mkdtemp(prefix='.restore-', dir=destination.parent))
    try:
        with tarfile.open(source, 'r:gz') as archive:
            records = inspect(archive)
            for name in records:
                path = temporary / name
                path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
                with path.open('xb') as output, archive.extractfile(name) as stream:
                    os.chmod(path, 0o600)
                    shutil.copyfileobj(stream, output)
                    output.flush()
                    os.fsync(output.fileno())
        # mkdir is the no-clobber claim; then move entries into our private directory.
        destination.mkdir(mode=0o700)
        for path in temporary.iterdir():
            path.rename(destination / path.name)
    finally:
        shutil.rmtree(temporary)
    return {'files': len(records), 'restored': True}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['backup', 'verify', 'restore'])
    parser.add_argument('source', type=Path)
    parser.add_argument('destination', nargs='?', type=Path)
    args = parser.parse_args()
    try:
        if args.action == 'verify':
            result = verify(args.source)
        elif args.destination is None:
            parser.error('destination is required')
        else:
            result = globals()[args.action](args.source, args.destination)
        print(json.dumps(result))
    except (OSError, ValueError, KeyError, tarfile.TarError) as error:
        parser.exit(1, 'backup operation failed: ' + str(error) + '\n')

if __name__ == '__main__':
    main()
