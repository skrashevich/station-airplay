#!/bin/sh
# Run on the station with an existing YAM v2 installation.
set -eu
platform=${1:?Usage: sh install-yam.sh PLATFORM [TAG, default v0.1.1]}
tag=${2:-v0.1.1}
case "$platform" in ''|*[!a-z0-9_]*) echo 'Invalid YAM platform' >&2; exit 2 ;; esac
case "$tag" in v*) version=${tag#v} ;; *) echo 'Tag must start with v' >&2; exit 2 ;; esac
case "$version" in ''|*[!0-9.]*|.*|*.) echo 'Invalid release tag' >&2; exit 2 ;; esac
case "$version" in *.*.*) ;; *.*) version=$version.0 ;; *) exit 2 ;; esac
command -v yam >/dev/null
command -v sha256sum >/dev/null
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
download() {
    if command -v curl >/dev/null 2>&1; then curl -fL --retry 3 "$1" -o "$2"
    else wget -O "$2" "$1"; fi
}
base=https://github.com/skrashevich/station-airplay/releases/download/$tag
artifact=station-airplay-$version-$platform.yampkg
download "$base/SHA256SUMS" "$work/SHA256SUMS"
download "$base/$artifact" "$work/$artifact"
awk -v file="$artifact" '$2 == file {print; found=1} END {if (!found) exit 1}' \
    "$work/SHA256SUMS" > "$work/selected.sha256"
(cd "$work" && sha256sum -c selected.sha256)
yam install --yes "$work/$artifact"
echo 'Installed. Run station-airplay -check, then station-airplay to start the receiver.'
