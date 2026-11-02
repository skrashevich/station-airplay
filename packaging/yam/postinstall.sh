#!/bin/sh
# YAM invokes hooks with a clean environment and paths to the installed package.
set -eu
umask 077
prefix=
mode=${1:-install}
# Isolated filesystem fixture, used by the shell tests; never remount its host.
if [ "$mode" = --test-root ]; then
    prefix=${2:?fixture root required}
    case "$prefix" in /*) ;; *) echo 'fixture root must be absolute' >&2; exit 2 ;; esac
    [ "$prefix" != / ] || exit 2
    prefix=$(CDPATH= cd "$prefix" && pwd -P)
    mode=${3:-install}
fi
case "$mode" in install|--enable) ;; *) echo 'invalid postinstall mode' >&2; exit 2 ;; esac
# Installing an ARM package with yam --root on a PC must not alter the host.
if [ ! -f "$prefix/system/vendor/quasar/quasar.cfg" ]; then
    echo 'station-airplay: off-station install; autostart activation skipped'
    exit 0
fi
root=${YAM_ROOT:?YAM_ROOT is required}
root=$(CDPATH= cd "$root" && pwd -P)
service=$root/bin/station-airplay-service
[ -x "$service" ] || { echo 'station-airplay: public service executable missing' >&2; exit 1; }
hook=$prefix/etc/init.d/S99station-airplay-yam
marker='# station-airplay YAM boot hook v1'
# Single-quote the public path when emitting shell code, including literal quotes.
escaped=$(printf '%s' "$service" | sed "s/'/'\\\\''/g")
if [ -e "$hook" ] || [ -L "$hook" ]; then
    [ -f "$hook" ] && [ ! -L "$hook" ] && [ "$(sed -n '2p' "$hook")" = "$marker" ] &&
        [ "$(sed -n '4p' "$hook")" = "service='$escaped'" ] || {
        echo "station-airplay: refusing to replace unowned $hook" >&2; exit 1;
    }
fi
restore_ro=no
tmp=$hook.tmp.$$
cleanup() {
    rm -f "$tmp"
    if [ "$restore_ro" = yes ]; then mount -o remount,ro /; fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
if [ -z "$prefix" ] && awk '$2 == "/" && $4 ~ /(^|,)ro(,|$)/ { found=1 } END { exit !found }' /proc/mounts; then
    mount -o remount,rw /
    restore_ro=yes
fi
{
    printf '#!/bin/sh\n%s\n' "$marker"
    printf 'unset YAM_ROOT YAM_VAR YAM_RUN YAM_PKG YAM_ETC YAM_PLATFORM\n'
    printf "service='%s'\n" "$escaped"
    printf '[ -x "$service" ] || exit 0\n'
    printf 'case "${1:-start}" in\n'
    printf '  start) exec "$service" boot ;;\n'
    printf '  stop|restart|status) exec "$service" "$1" ;;\n'
    printf 'esac\n'
} > "$tmp"
chmod 755 "$tmp"
mv -f "$tmp" "$hook"
if [ "$restore_ro" = yes ]; then
    mount -o remount,ro /
    restore_ro=no
fi
mkdir -p "$root/var/skrashevich.station-airplay"
if [ "$mode" = --enable ]; then rm -f "$root/var/skrashevich.station-airplay/disabled"; fi
# The launcher infers its root, including compatibility with legacy identities.
unset YAM_ROOT YAM_VAR YAM_RUN YAM_PKG YAM_ETC YAM_PLATFORM
# Stop the old version before starting the current public executable on upgrade.
if [ ! -f "$root/var/skrashevich.station-airplay/disabled" ] || "$service" status >/dev/null 2>&1; then
    "$service" restart
fi
