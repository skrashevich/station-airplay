#!/usr/bin/env python3
"""Guard release coverage and ABI mappings against the pinned YAM catalog."""
import argparse
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--yam-source', type=Path, required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
targets = {}
for line in (root / 'packaging/yam/targets.tsv').read_text().splitlines():
    if not line.strip() or line.startswith('#'):
        continue
    profile, arch = line.split()
    if profile in targets:
        raise SystemExit(f'duplicate profile: {profile}')
    targets[profile] = arch
for profile in ('yandexstation', 'yandexstation_2'):
    if targets.get(profile) != 'arm64':
        raise SystemExit(f'missing Android ARM64 target: {profile}')
for profile, arch in targets.items():
    # Pinned upstream profiles are flat YAML mappings; read only the ABI field.
    fields = dict(line.split(':', 1) for line in
                  (args.yam_source / 'platforms' / f'{profile}.yaml').read_text().splitlines()
                  if line.startswith('arch:'))
    expected = {'aarch64': 'arm64', 'armv7': 'armv7'}.get(fields.get('arch', '').strip())
    if expected != arch:
        raise SystemExit(f'ABI mismatch for {profile}: {arch} != {expected}')
print(f'Validated {len(targets)} YAM targets, including Station 1 and Max Android profiles')
