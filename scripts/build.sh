#!/bin/sh
set -eu
here=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$here"
version=${1:-0.1.0-dev}
case "$version" in ''|*[!0-9A-Za-z._+-]*) echo 'Invalid version' >&2;exit 2;; esac
stage=
trap '[ -z "$stage" ] || rm -rf "$stage"' EXIT HUP INT TERM
mkdir -p dist
sh scripts/install_test.sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
for target in arm64 armv7;do
 case "$target" in arm64) arch=arm64;arm=7;;armv7) arch=arm;arm=7;;esac
 CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$arm" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "dist/station-airplay-linux-$target" ./cmd/station-airplay
 stage=$(mktemp -d)
 cp "dist/station-airplay-linux-$target" "$stage/station-airplay"
 mkdir -p "$stage/scripts" "$stage/docs/licenses"
 cp scripts/install.sh scripts/service.sh "$stage/scripts/"
 cp README.md NOTICE LICENSE AUTHORS "$stage/"
 cp docs/validation.md docs/embedding.md docs/SOURCE.md "$stage/docs/"
 cp docs/licenses/* "$stage/docs/licenses/"
 python3 - "$stage" "dist/station-airplay-$version-linux-$target.tar.gz" <<'PYARCHIVE'
import importlib.util
from pathlib import Path
import sys
import tarfile
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("source_package", "scripts/build-source-package.py")
package = importlib.util.module_from_spec(spec)
spec.loader.exec_module(package)
with tarfile.open(sys.argv[2], "w:gz", format=tarfile.PAX_FORMAT) as archive:
    for path in sorted(Path(sys.argv[1]).iterdir()):
        package.add_portable(archive, path, path.name)
PYARCHIVE
 rm -rf "$stage"
 stage=
done

python3 scripts/build-source-package.py --version "$version"
python3 - "$version" <<'PYHASH'
import hashlib
from pathlib import Path
import sys
version = sys.argv[1]
paths = [Path("dist") / f"station-airplay-{version}-{target}.tar.gz"
         for target in ("linux-arm64", "linux-armv7", "source")]
Path("dist/SHA256SUMS").write_text("".join(
    f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n" for path in paths))
PYHASH
