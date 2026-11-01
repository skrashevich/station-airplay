#!/bin/sh
# Resolve YAM's public symlink so a running version uses its own payload.
set -eu
umask 077
self=$0
while [ -L "$self" ]; do
    link=$(readlink "$self")
    case "$link" in /*) self=$link ;; *) self=$(dirname "$self")/$link ;; esac
done
payload=$(CDPATH= cd "$(dirname "$self")/.." && pwd -P)
# YAM layout: ROOT/pkgs/namespace/name/version/files/bin/launcher.
root=${YAM_ROOT:-$(CDPATH= cd "$payload/../../../../.." && pwd -P)}
state=${YAM_VAR:-$root/var}/skrashevich.station-airplay
mkdir -p "$state"
exec "$payload/share/skrashevich.station-airplay/bin/station-airplay" \
    -data-dir "$state" "$@"
