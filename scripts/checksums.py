#!/usr/bin/env python3
"""Write checksums for every distributable release artifact (not raw build intermediates)."""
import hashlib
from pathlib import Path
import sys

dist = Path(__file__).resolve().parents[1] / "dist"
version = sys.argv[1] if len(sys.argv) > 1 else (dist.parent / "VERSION").read_text().strip()
paths = [dist / f"station-airplay-{version}-{target}.tar.gz"
         for target in ("linux-arm64", "linux-armv7", "source")]
for line in (dist.parent / "packaging/yam/targets.tsv").read_text().splitlines():
    if line.strip() and not line.startswith("#"):
        paths.append(dist / f"station-airplay-{version}-{line.split()[0]}.yampkg")
paths = sorted(path for path in [*paths, dist / "install-yam.sh"] if path.is_file())
if not paths:
    raise SystemExit("no release artifacts in dist/")
(dist / "SHA256SUMS").write_text("".join(
    f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n" for path in paths))
