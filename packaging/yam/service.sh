#!/bin/sh
# Small supervisor for YAM versions that only store service declarations.
set -eu
umask 077
self=$0
while [ -L "$self" ]; do
    link=$(readlink "$self")
    case "$link" in /*) self=$link ;; *) self=$(dirname "$self")/$link ;; esac
done
payload=$(CDPATH= cd "$(dirname "$self")/.." && pwd -P)
root=$(CDPATH= cd "$payload/../../../.." && pwd -P)
public=$root/bin/station-airplay
service=$root/bin/station-airplay-service
state=$root/var/skrashevich.station-airplay
run=$root/run/skrashevich.station-airplay
lock=$run/service
boot_id=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null || true)
mkdir -p "$state" "$run"

process_command() {
    case "$1" in ''|*[!0-9]*) return 1 ;; esac
    if [ -r "/proc/$1/cmdline" ]; then
        tr '\000' ' ' < "/proc/$1/cmdline"
    else
        ps -p "$1" -o args= 2>/dev/null
    fi
}
same_boot() {
    # Old service versions did not stamp locks; retain upgrade compatibility.
    [ -z "$boot_id" ] || [ ! -f "$lock/boot_id" ] ||
        [ "$(cat "$lock/boot_id" 2>/dev/null)" = "$boot_id" ]
}
running() {
    same_boot || return 1
    [ -f "$lock/pid" ] || return 1
    read -r supervisor < "$lock/pid" || return 1
    # A stale PID must never identify an unrelated process as our supervisor.
    command=$(process_command "$supervisor") || return 1
    case "$command" in *"$service __run"*) return 0 ;; *) return 1 ;; esac
}
starting() {
    same_boot || return 1
    [ -f "$lock/owner" ] || return 1
    read -r owner < "$lock/owner" || return 1
    # This invocation cannot own a lock it has not acquired yet. This also
    # recovers old unstamped locks if a reboot reuses the current CLI PID.
    [ "$owner" != "$$" ] || return 1
    command=$(process_command "$owner") || return 1
    # The run directory can survive reboot; an unrelated reused PID is stale.
    case "$command" in
        *"$service __run"*) return 1 ;;
        *"$service"*) return 0 ;;
        *) return 1 ;;
    esac
}
stop() {
    if ! running; then
        rm -rf "$lock"
        return 0
    fi
    : > "$lock/stop"
    n=0
    while running; do
        n=$((n + 1))
        if [ "$n" -ge 20 ]; then
            echo 'station-airplay: supervisor did not stop within 20 seconds' >&2
            return 1
        fi
        sleep 1
    done
}
start() {
    [ -x "$public" ] || { echo 'station-airplay: package executable is missing' >&2; return 1; }
    if running; then echo 'station-airplay: already running'; return 0; fi
    # mkdir serializes simultaneous starts; never replace a live lock.
    if ! mkdir "$lock" 2>/dev/null; then
        n=0
        while [ "$n" -lt 5 ]; do
            sleep 1
            if running; then return 0; fi
            n=$((n + 1))
        done
        # Never steal a lock from a CLI still launching its supervisor.
        if starting; then return 1; fi
        rm -rf "$lock"
        mkdir "$lock" 2>/dev/null || return 1
    fi
    printf '%s\n' "$boot_id" > "$lock/boot_id"
    printf '%s\n' "$$" > "$lock/owner"
    rm -f "$lock/stop"
    # Old station adbd tears down the calling shell's session on disconnect.
    # A new session keeps the receiver independent of that transport.
    if command -v setsid >/dev/null 2>&1; then
        nohup setsid "$service" __run "$$" >> "$state/service.log" 2>&1 < /dev/null &
    elif command -v busybox >/dev/null 2>&1 && busybox --list | grep -qx setsid; then
        nohup busybox setsid "$service" __run "$$" >> "$state/service.log" 2>&1 < /dev/null &
    else
        # Non-station hosts such as macOS may not provide setsid.
        nohup "$service" __run "$$" >> "$state/service.log" 2>&1 < /dev/null &
    fi
    n=0
    while ! running; do
        n=$((n + 1))
        [ "$n" -lt 5 ] || { echo 'station-airplay: supervisor failed to start; see service.log' >&2; return 1; }
        sleep 1
    done
    echo 'station-airplay: started'
}

case "${1:-start}" in
    start) start ;;
    stop) stop ;;
    restart) stop; start ;;
    status)
        if running; then echo "station-airplay: running (supervisor $supervisor)"; else echo 'station-airplay: stopped'; exit 1; fi ;;
    boot) [ -f "$state/disabled" ] || start ;;
    enable) YAM_ROOT=$root exec "$root/bin/station-airplay-postinstall" --enable ;;
    disable) : > "$state/disabled"; stop; echo 'station-airplay: autostart disabled' ;;
    __run)
        # Only start() creates this directory. Do not expose an unlocked worker.
        [ -d "$lock" ] && [ ! -f "$lock/pid" ] || exit 1
        [ "$(cat "$lock/owner")" = "${2:-}" ] || exit 1
        printf '%s\n' "$$" > "$lock/pid"
        child=
        cleanup() {
            trap '' TERM INT HUP
            if [ -n "$child" ] && kill -0 "$child" 2>/dev/null; then
                kill -TERM "$child" 2>/dev/null || true
                n=0
                while kill -0 "$child" 2>/dev/null && [ "$n" -lt 10 ]; do
                    sleep 1
                    n=$((n + 1))
                done
                if kill -0 "$child" 2>/dev/null; then kill -KILL "$child" 2>/dev/null || true; fi
                wait "$child" 2>/dev/null || true
            fi
            rm -rf "$lock"
        }
        trap 'exit 0' TERM INT HUP
        trap cleanup EXIT
        while [ -x "$public" ] && [ ! -f "$lock/stop" ]; do
            "$public" >> "$state/receiver.log" 2>&1 &
            child=$!
            while kill -0 "$child" 2>/dev/null; do
                [ -x "$public" ] && [ ! -f "$lock/stop" ] || exit 0
                sleep 1
            done
            result=0
            wait "$child" || result=$?
            child=
            [ "$result" -ne 0 ] || exit 0
            # Bound restart frequency while the network/ALSA stack is starting.
            sleep 2
        done
        ;;
    *) echo 'usage: station-airplay-service {start|stop|restart|status|enable|disable}' >&2; exit 2 ;;
esac
