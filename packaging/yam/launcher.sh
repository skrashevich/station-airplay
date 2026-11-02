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
# YAM layout: ROOT/pkgs/namespace/name/version/bin/launcher.
root=${YAM_ROOT:-$(CDPATH= cd "$payload/../../../.." && pwd -P)}
state=${YAM_VAR:-$root/var}/skrashevich.station-airplay
# Older launchers inferred the parent of YAM_ROOT. Keep an existing pairing
# there until it is migrated explicitly; never move a live identity at launch.
if [ -z "${YAM_ROOT+x}" ] && [ -z "${YAM_VAR+x}" ]; then
    legacy=$(CDPATH= cd "$root/.." && pwd -P)/var/skrashevich.station-airplay
    if [ ! -f "$state/identity.json" ] && [ -f "$legacy/identity.json" ]; then
        state=$legacy
    fi
fi
mkdir -p "$state"
exec "$payload/share/skrashevich.station-airplay/bin/station-airplay" \
    -data-dir "$state" "$@"
