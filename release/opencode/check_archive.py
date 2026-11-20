#!/usr/bin/env python3
"""Check the archive platform and its pinned packages."""
import argparse
import hashlib
import json
from pathlib import Path
import tarfile

# The ELF e_machine value of each supported Linux target, and the package-name
# fragments that must never appear in that platform's archive.
PLATFORMS = {
    'linux-arm64': {'machine': b'\xb7\x00', 'foreign': ('cli-linux-arm64-musl', 'cli-darwin', 'cli-linux-x64')},
    'linux-x64': {'machine': b'\x3e\x00', 'foreign': ('cli-linux-arm64', 'cli-darwin', 'cli-linux-x64-musl')},
}


def inspect(platform, pins, files, read):
    package = pins['platforms'][platform]['package']
    expected = f'node_modules/@opencode/{package}/bin/opencode'
    programs = [name for name in files if name.endswith('/bin/opencode')]
    if programs != [expected]:
        raise ValueError(f'The archive must contain one {platform} OpenCode program.')
    header = read(expected)[:20]
    if header[:4] != b'\x7fELF' or header[4:6] != b'\x02\x01' or header[18:20] != PLATFORMS[platform]['machine']:
        raise ValueError(f'The OpenCode program requires {platform} (glibc).')
    if any(fragment in name for fragment in PLATFORMS[platform]['foreign'] for name in files):
        raise ValueError('The archive contains another OpenCode platform.')
    for name, version in [('@opencode/' + package, pins['opencode']), ('opencode-websearch', pins['websearch'])]:
        if json.loads(read(f'node_modules/{name}/package.json'))['version'] != version:
            raise ValueError('The archive package differs from its release pin.')
    for name in ['control/server.mjs', 'control/shell', 'control/package.json']:
        if name not in files:
            raise ValueError('The archive lacks the Culler control plugin.')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('archive', nargs='?')
    parser.add_argument('--folder')
    parser.add_argument('--platform', default='linux-arm64')
    parser.add_argument('--pins', default=str(Path(__file__).with_name('pins.json')))
    args = parser.parse_args()
    if args.platform not in PLATFORMS:
        raise ValueError('The OpenCode platform is unsupported.')
    pins = json.loads(Path(args.pins).read_text())
    if args.folder:
        root = Path(args.folder)
        files = [str(path.relative_to(root)) for path in root.rglob('*') if path.is_file()]
        inspect(args.platform, pins, files, lambda name: (root / name).read_bytes())
    else:
        with tarfile.open(args.archive) as archive:
            members = {member.name.removeprefix('./'): member for member in archive.getmembers() if member.isfile()}
            inspect(args.platform, pins, list(members), lambda name: archive.extractfile(members[name]).read())
        path = Path(args.archive)
        print(json.dumps({'name': path.name, 'size': path.stat().st_size,
                          'sha256': hashlib.sha256(path.read_bytes()).hexdigest()}))


if __name__ == '__main__':
    main()
