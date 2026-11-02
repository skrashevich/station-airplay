#!/bin/sh
set -eu
here=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$here"
version=${1:-$(cat VERSION)}
case "$version" in ''|*[!0-9A-Za-z._+-]*) echo 'Invalid version' >&2;exit 2;; esac
case "${COMPRESS_UPX:-0}" in
 0) upx=false;;
 1) command -v upx >/dev/null; upx=true;;
 *) echo 'COMPRESS_UPX must be 0 or 1' >&2;exit 2;;
esac
stage=
trap '[ -z "$stage" ] || rm -rf "$stage"' EXIT HUP INT TERM
mkdir -p dist
sh scripts/install_test.sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
for target in arm64 armv7;do
 case "$target" in arm64) arch=arm64;arm=7;;armv7) arch=arm;arm=7;;esac
 CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$arm" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "dist/station-airplay-linux-$target" ./cmd/station-airplay
 CGO_ENABLED=0 go run ./cmd/station-pcm-package -arch "$target" -binary "dist/station-airplay-linux-$target" -upx="$upx"
 stage=$(mktemp -d)
 cp "dist/station-airplay-linux-$target" "$stage/station-airplay"
 mkdir -p "$stage/scripts" "$stage/docs/licenses"
 cp scripts/install.sh scripts/service.sh "$stage/scripts/"
 cp README.md NOTICE LICENSE AUTHORS "$stage/"
 cp docs/validation.md docs/kuyase-validation.md docs/kuyase-validation.json docs/embedding.md docs/SOURCE.md docs/yam.md docs/releases.md "$stage/docs/"
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
python3 scripts/checksums.py "$version"
