#!/usr/bin/env python3
"""Check the archive platform and its pinned packages."""
import argparse
import hashlib
import json
from pathlib import Path
import tarfile


def inspect(files, read):
    programs = [name for name in files if name.endswith('/bin/opencode')]
    expected = 'node_modules/@opencode/cli-linux-arm64/bin/opencode'
    if programs != [expected]:
        raise ValueError('The archive must contain one Linux arm64 OpenCode program.')
    header = read(expected)[:20]
    if header[:4] != b'\x7fELF' or header[4:6] != b'\x02\x01' or header[18:20] != b'\xb7\x00':
        raise ValueError('The OpenCode program must use Linux arm64.')
    if any('cli-linux-arm64-musl' in name or 'cli-darwin' in name or 'cli-linux-x64' in name for name in files):
        raise ValueError('The archive contains another OpenCode platform.')
    for package, version in [('@opencode/cli-linux-arm64', '2.2.7'), ('opencode-websearch', '0.7.1')]:
        if json.loads(read(f'node_modules/{package}/package.json'))['version'] != version:
            raise ValueError('The archive package differs from its release pin.')
    for name in ['control/server.mjs', 'control/shell', 'control/package.json']:
        if name not in files:
            raise ValueError('The archive lacks the Culler control plugin.')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('archive', nargs='?')
    parser.add_argument('--folder')
    args = parser.parse_args()
    if args.folder:
        root = Path(args.folder)
        files = [str(path.relative_to(root)) for path in root.rglob('*') if path.is_file()]
        inspect(files, lambda name: (root / name).read_bytes())
    else:
        with tarfile.open(args.archive) as archive:
            members = {member.name.removeprefix('./'): member for member in archive.getmembers() if member.isfile()}
            inspect(list(members), lambda name: archive.extractfile(members[name]).read())
        path = Path(args.archive)
        print(json.dumps({'name': path.name, 'size': path.stat().st_size,
                          'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}))


if __name__ == '__main__':
    main()
