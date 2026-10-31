#!/bin/sh
set -eu
here=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$here"
version=${1:-0.1.0-dev}
case "$version" in ''|*[!0-9A-Za-z._+-]*) echo 'Invalid version' >&2;exit 2;; esac
mkdir -p dist
sh scripts/install_test.sh
sh scripts/inittab_test.sh
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
for target in arm64 armv7;do
 case "$target" in arm64) arch=arm64;arm=7;;armv7) arch=arm;arm=7;;esac
 CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOARM="$arm" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "dist/station-airplay-linux-$target" .
 stage=$(mktemp -d)
 cp "dist/station-airplay-linux-$target" "$stage/station-airplay"
 mkdir -p "$stage/scripts" "$stage/docs/licenses"
 cp scripts/install.sh scripts/service.sh "$stage/scripts/"
 cp README.md NOTICE LICENSE AUTHORS "$stage/"
 cp docs/validation.md "$stage/docs/"
 cp docs/licenses/* "$stage/docs/licenses/"
 COPYFILE_DISABLE=1 tar -czf "dist/station-airplay-$version-linux-$target.tar.gz" -C "$stage" .
 rm -rf "$stage"
done
