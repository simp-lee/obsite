#!/bin/sh
set -eu

[ "$#" -eq 1 ] || { echo "usage: $0 DIST_DIR" >&2; exit 2; }
DIST=$1
[ -d "$DIST" ] || { echo "dist directory not found: $DIST" >&2; exit 1; }
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
METADATA=$DIST/metadata.json
[ -f "$METADATA" ] || { echo "release metadata missing: $METADATA" >&2; exit 1; }

EXPECTED_VERSION=$(python3 - "$METADATA" <<'PY'
import json, sys
print(json.load(open(sys.argv[1], encoding='utf-8'))['version'])
PY
)
EXPECTED_COMMIT=$(python3 - "$METADATA" <<'PY'
import json, sys
print(json.load(open(sys.argv[1], encoding='utf-8'))['commit'])
PY
)
case "$EXPECTED_VERSION" in
  *snapshot*) EXPECTED_TYPE=snapshot ;;
  *) EXPECTED_TYPE=release ;;
esac
COMMIT_DATE=$(git -C "$ROOT" show -s --format=%cI "$EXPECTED_COMMIT")
EXPECTED_DATE=$(python3 - "$COMMIT_DATE" <<'PY'
from datetime import datetime, timezone
import sys
value = datetime.fromisoformat(sys.argv[1]).astimezone(timezone.utc)
print(value.strftime('%Y-%m-%dT%H:%M:%SZ'))
PY
)
EXPECTED_OUTPUT="obsite version=$EXPECTED_VERSION commit=$EXPECTED_COMMIT date=$EXPECTED_DATE type=$EXPECTED_TYPE"

python3 - "$ROOT" "$DIST" <<'PY'
from pathlib import Path
import hashlib, sys, tarfile, zipfile

root, dist = map(Path, sys.argv[1:])
archives = sorted(list(dist.glob('*.tar.gz')) + list(dist.glob('*.zip')))
if len(archives) != 6:
    raise SystemExit(f'archive count = {len(archives)}, want 6: {[p.name for p in archives]}')

notice_paths = {
    'LICENSE',
    'THIRD_PARTY.md',
    'internal/markdown/PASSTHROUGH_LICENSE',
    'internal/recommend/chinese/LICENSE-APACHE-2.0',
    'internal/recommend/chinese/LICENSE-CEDAR-BSD-2-CLAUSE',
    'internal/recommend/chinese/LICENSE-JIEBA-MIT',
    'internal/recommend/chinese/THIRD_PARTY.md',
    'internal/render/vendor/katex/LICENSE',
    'internal/render/vendor/mermaid/LICENSE',
    'internal/social/assets/DroidSansFallbackFull.LICENSE',
}
expected_matrix = {(os_name, arch) for os_name in ('linux', 'darwin', 'windows') for arch in ('amd64', 'arm64')}

def matrix_key(name):
    lowered = name.lower()
    os_name = next((item for item in ('linux', 'darwin', 'windows') if item in lowered), None)
    arch = next((item for item in ('amd64', 'arm64') if item in lowered), None)
    if not os_name or not arch:
        raise SystemExit(f'artifact matrix name is not auditable: {name}')
    return os_name, arch

raw_binaries = {}
for path in dist.rglob('*'):
    if path.is_file() and path.name in ('obsite', 'obsite.exe'):
        key = matrix_key(path.parent.name)
        if key in raw_binaries:
            raise SystemExit(f'duplicate raw binary for {key}: {path}, {raw_binaries[key]}')
        raw_binaries[key] = path
if set(raw_binaries) != expected_matrix:
    raise SystemExit(f'binary matrix = {sorted(raw_binaries)}, want {sorted(expected_matrix)}')

seen = set()
for archive in archives:
    key = matrix_key(archive.name)
    seen.add(key)
    expected_binary = 'obsite.exe' if key[0] == 'windows' else 'obsite'
    if key[0] == 'windows':
        if archive.suffix != '.zip':
            raise SystemExit(f'{archive.name}: Windows archive must be zip')
        with zipfile.ZipFile(archive) as bundle:
            infos = {info.filename.rstrip('/'): info for info in bundle.infolist() if not info.is_dir()}
            members = {name: bundle.read(info) for name, info in infos.items()}
            modes = {name: (info.external_attr >> 16) & 0o777 for name, info in infos.items()}
    else:
        if not archive.name.endswith('.tar.gz'):
            raise SystemExit(f'{archive.name}: {key[0]} archive must be tar.gz')
        with tarfile.open(archive, 'r:gz') as bundle:
            infos = {member.name.rstrip('/'): member for member in bundle.getmembers() if member.isfile()}
            members = {name: bundle.extractfile(info).read() for name, info in infos.items()}
            modes = {name: info.mode & 0o777 for name, info in infos.items()}
            for name, info in infos.items():
                if (info.uid, info.gid, info.uname, info.gname) != (0, 0, 'root', 'root'):
                    raise SystemExit(f'{archive.name}: noncanonical ownership for {name}')
    expected_members = notice_paths | {expected_binary}
    if set(members) != expected_members:
        raise SystemExit(f'{archive.name} members = {sorted(members)}, want {sorted(expected_members)}')
    for name, mode in modes.items():
        expected_mode = 0o755 if name == expected_binary else 0o644
        if mode != expected_mode:
            raise SystemExit(f'{archive.name}: mode for {name} = {mode:o}, want {expected_mode:o}')
    if members[expected_binary] != raw_binaries[key].read_bytes():
        raise SystemExit(f'{archive.name} executable differs from the audited raw binary')
if seen != expected_matrix:
    raise SystemExit(f'archive matrix = {sorted(seen)}, want {sorted(expected_matrix)}')

checksum = dist / 'checksums.txt'
if not checksum.is_file():
    raise SystemExit('checksums.txt missing')
listed = {}
for line in checksum.read_text(encoding='utf-8').splitlines():
    digest, name = line.split(None, 1)
    listed[name.strip()] = digest
for archive in archives:
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    if listed.get(archive.name) != digest:
        raise SystemExit(f'checksum mismatch for {archive.name}')
if set(listed) != {archive.name for archive in archives}:
    raise SystemExit(f'checksum members = {sorted(listed)}')

for key, binary in sorted(raw_binaries.items()):
    data = binary.read_bytes()
    for marker in (b'testdata/quality', b'Rebuilding State from an Event Log', b'cal-zh-hans-00', b'_pagefind'):
        if marker in data:
            raise SystemExit(f'{binary}: forbidden marker {marker!r}')
    for name in ('s_1.txt', 't_1.txt', 'stop_tokens.txt'):
        window = (root / 'internal/recommend/chinese/data' / name).read_bytes()[:64]
        if data.count(window) != 1:
            raise SystemExit(f'{binary}: {name} resource count != 1')
    for marker in (b'globalThis["mermaid"]', b'renderMathInElement', b'obsite.theme.v1:'):
        if marker not in data:
            raise SystemExit(f'{binary}: embedded runtime marker missing: {marker!r}')
    print(f'{binary.relative_to(dist)} {len(data)} bytes')
PY

linux_amd64=$(find "$DIST" -type f -path '*linux*amd64*/obsite' | head -n 1)
[ -n "$linux_amd64" ] || { echo "linux amd64 binary missing" >&2; exit 1; }
file "$linux_amd64" | grep -E 'statically linked|not a dynamic executable' >/dev/null || {
  echo "linux amd64 binary is not statically linked" >&2
  file "$linux_amd64" >&2
  exit 1
}
VERSION_OUTPUT=$($linux_amd64 version)
VERSION_FLAG_OUTPUT=$($linux_amd64 --version)
[ "$VERSION_OUTPUT" = "$EXPECTED_OUTPUT" ] || { echo "version metadata mismatch: $VERSION_OUTPUT" >&2; exit 1; }
[ "$VERSION_FLAG_OUTPUT" = "$EXPECTED_OUTPUT" ] || { echo "--version metadata mismatch: $VERSION_FLAG_OUTPUT" >&2; exit 1; }

printf 'release metadata verified: %s\n' "$EXPECTED_OUTPUT"
echo "release artifacts verified"
