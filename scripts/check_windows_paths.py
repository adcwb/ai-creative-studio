#!/usr/bin/env python3
"""Read-only portability preflight; filenames are fingerprinted by default."""
import argparse
import hashlib
import os
from pathlib import Path
import re
import stat

RESERVED = re.compile(r'^(CON|PRN|AUX|NUL|COM[1-9¹²³]|LPT[1-9¹²³])(?:\..*)?$', re.I)


def inspect(root):
    root = Path(root)
    issues = []
    def add(path, reason):
        issues.append({'path': str(path), 'reason': reason})
    def walk(directory):
        try:
            entries = sorted(os.scandir(directory), key=lambda e: e.name)
        except OSError:
            add(directory, 'directory_unavailable')
            return
        seen = set()
        for entry in entries:
            name = entry.name
            if name.casefold() in seen:
                add(entry.path, 'case_collision')
            seen.add(name.casefold())
            if RESERVED.match(name) or name.endswith((' ', '.')) or re.search(r'[<>:"/\\|?*\x00-\x1f]', name):
                add(entry.path, 'unrepresentable_name')
            if len(name.encode('utf-16-le', errors='surrogatepass')) // 2 > 255:
                add(entry.path, 'component_too_long')
            try:
                info = entry.stat(follow_symlinks=False)
            except OSError:
                add(entry.path, 'entry_unavailable')
                continue
            if entry.is_symlink() or getattr(info, 'st_file_attributes', 0) & 1024:
                add(entry.path, 'link_or_reparse')
            elif stat.S_ISDIR(info.st_mode):
                walk(entry.path)
            elif not stat.S_ISREG(info.st_mode):
                add(entry.path, 'unsupported_file_type')
    try:
        info = root.lstat()
        if not stat.S_ISDIR(info.st_mode) or stat.S_ISLNK(info.st_mode) or getattr(info, 'st_file_attributes', 0) & 1024:
            add(root, 'unsafe_root')
        else:
            walk(root)
    except OSError:
        add(root, 'directory_unavailable')
    return issues


if __name__ == '__main__':
    import json
    parser = argparse.ArgumentParser()
    parser.add_argument('root')
    parser.add_argument('--show-paths', action='store_true', help='Administrator opts in to displaying filenames')
    args = parser.parse_args()
    issues = inspect(args.root)
    for issue in issues:
        if not args.show_paths:
            issue['path'] = hashlib.sha256(issue['path'].encode('utf-8', errors='surrogatepass')).hexdigest()[:16]
    print(json.dumps({'issues': issues}, ensure_ascii=False))
    raise SystemExit(bool(issues))
