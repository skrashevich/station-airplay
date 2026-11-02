#!/bin/sh
# Host-only fixtures: no real /etc changes, station connections or ARM execution.
set -eu
repo=$(CDPATH= cd "$(dirname "$0")/.." && pwd -P)
fixture=$(mktemp -d)
root="$fixture/yam quote's root"
payload=$root/pkgs/skrashevich/station-airplay/1.0
cleanup() {
    "$root/bin/station-airplay-service" stop >/dev/null 2>&1 || true
    rm -rf "$fixture"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
mkdir -p "$payload/bin" "$root/bin"
cp "$repo/packaging/yam/service.sh" "$payload/bin/station-airplay-service"
cp "$repo/packaging/yam/postinstall.sh" "$payload/bin/station-airplay-postinstall"
chmod 755 "$payload/bin/"*
ln -s "$payload/bin/station-airplay-service" "$root/bin/station-airplay-service"
ln -s "$payload/bin/station-airplay-postinstall" "$root/bin/station-airplay-postinstall"
cat > "$root/bin/station-airplay" <<'RECEIVER'
#!/bin/sh
set -eu
root=$(CDPATH= cd "$(dirname "$0")/.." && pwd -P)
printf 'start:%s:YAM_ROOT=%s:YAM_VAR=%s\n' "$$" "${YAM_ROOT-unset}" "${YAM_VAR-unset}" >> "$root/events"
trap 'echo term:$$ >> "$root/events"; exit 0' TERM INT
while :; do sleep 1; done
RECEIVER
chmod 755 "$root/bin/station-airplay"
cp "$root/bin/station-airplay" "$fixture/receiver-fixture"
# Stub the native setsid entry point so macOS also checks the detach branch.
mkdir "$fixture/native-bin"
cat > "$fixture/native-bin/setsid" <<'SETSID'
#!/bin/sh
printf 'native\n' >> "$(dirname "$0")/../detach.log"
exec "$@"
SETSID
chmod 755 "$fixture/native-bin/setsid"
# Supply a deterministic Linux boot ID on macOS too; all other cat calls pass through.
cat_command=$(command -v cat)
{
    printf '#!/bin/sh\n'
    printf 'if [ "$1" = /proc/sys/kernel/random/boot_id ]; then\n'
    printf '  echo fixture-current-boot\n'
    printf 'else\n'
    printf '  exec "%s" "$@"\n' "$cat_command"
    printf 'fi\n'
} > "$fixture/native-bin/cat"
chmod 755 "$fixture/native-bin/cat"
PATH=$fixture/native-bin:$PATH
export PATH
service=$root/bin/station-airplay-service
hook=$fixture/etc/init.d/S99station-airplay-yam
assert_stopped() { if "$service" status >/dev/null 2>&1; then echo 'expected stopped' >&2; exit 1; fi; }
wait_event() {
    n=0
    until [ -f "$root/events" ] && [ "$(wc -l < "$root/events" | tr -d ' ')" -ge "$1" ]; do
        n=$((n + 1)); [ "$n" -lt 10 ] || { cat "$root/events"; exit 1; }; sleep 1
    done
}
assert_stopped
# A stale PID cannot cause stop to signal another process.
mkdir -p "$root/run/skrashevich.station-airplay/service"
printf '%s\n' "$$" > "$root/run/skrashevich.station-airplay/service/pid"
"$service" stop
"$service" start &
first=$!
"$service" start &
second=$!
wait "$first"
wait "$second"
wait_event 1
# A live supervisor from an older package may not have a boot stamp yet.
rm "$root/run/skrashevich.station-airplay/service/boot_id"
"$service" status
"$service" start
[ "$(wc -l < "$root/events" | tr -d ' ')" -eq 1 ]
"$service" status
"$service" restart
wait_event 3
"$service" stop
wait_event 4
assert_stopped
# Off-station install must skip activation even with valid YAM paths.
YAM_ROOT=$root "$payload/bin/station-airplay-postinstall" --test-root "$fixture"
[ ! -e "$hook" ]
mkdir -p "$fixture/system/vendor/quasar" "$fixture/etc/init.d"
: > "$fixture/system/vendor/quasar/quasar.cfg"
# Test the same installation branch and generated hook against an isolated /etc.
YAM_ROOT=$root YAM_VAR=$root/var "$payload/bin/station-airplay-postinstall" --test-root "$fixture"
wait_event 5
sh -n "$hook"
[ -x "$hook" ]
# The receiver must inherit no YAM_ROOT/YAM_VAR from the postinstall environment.
sed -n '5p' "$root/events" | rg -q 'YAM_ROOT=unset:YAM_VAR=unset'
"$service" disable
assert_stopped
"$hook" start
assert_stopped
# Reinstall preserves the user's disabled setting.
YAM_ROOT=$root "$payload/bin/station-airplay-postinstall" --test-root "$fixture"
assert_stopped
# A manually started disabled service must upgrade its live receiver too.
"$service" start
wait_event 7
previous=$(cat "$root/run/skrashevich.station-airplay/service/pid")
# Replace the public receiver atomically, as an installed new payload would be.
awk '/^root=/{ print; print ": > \"$root/public-version2\""; next } { print }' \
    "$root/bin/station-airplay" > "$root/bin/receiver-next"
chmod 755 "$root/bin/receiver-next"
mv "$root/bin/receiver-next" "$root/bin/station-airplay"
YAM_ROOT=$root "$payload/bin/station-airplay-postinstall" --test-root "$fixture"
wait_event 9
[ -f "$root/public-version2" ]
[ "$(cat "$root/run/skrashevich.station-airplay/service/pid")" != "$previous" ]
[ -f "$root/var/skrashevich.station-airplay/disabled" ]
"$service" stop
wait_event 10
"$hook" start
assert_stopped
YAM_ROOT=$root "$payload/bin/station-airplay-postinstall" --test-root "$fixture" --enable
wait_event 11
"$hook" stop
assert_stopped
"$hook" start
wait_event 13
# Removal has no YAM hook; the supervisor detects the missing public executable.
rm "$root/bin/station-airplay"
n=0
while "$service" status >/dev/null 2>&1; do
    n=$((n + 1)); [ "$n" -lt 15 ] || exit 1; sleep 1
done
wait_event 14
rg -q '^native$' "$fixture/detach.log"
# A persistent stale owner from another boot may now name an unrelated live PID.
# start must recover, and the unrelated shell running this test must survive.
cp "$fixture/receiver-fixture" "$root/bin/station-airplay"
mkdir -p "$root/run/skrashevich.station-airplay/service"
printf '%s\n' "$$" > "$root/run/skrashevich.station-airplay/service/owner"
printf '%s\n' "$$" > "$root/run/skrashevich.station-airplay/service/pid"
"$service" start
wait_event 15
kill -0 "$$"
"$service" stop
wait_event 16
assert_stopped
# Simulate a reboot reusing EXACTLY the PID of the current launching CLI.
# exec keeps the wrapper's PID; its cmdline then identifies the actual service.
cat > "$root/bin/reused-pid-start" <<'REUSED'
#!/bin/sh
set -eu
root=$(CDPATH= cd "$(dirname "$0")/.." && pwd -P)
lock=$root/run/skrashevich.station-airplay/service
mkdir "$lock"
printf '%s\n' "$$" > "$lock/owner"
printf '%s\n' "$$" > "$lock/pid"
if [ "${1:-}" != --unstamped ]; then
    printf 'fixture-previous-boot\n' > "$lock/boot_id"
fi
exec "$root/bin/station-airplay-service" start
REUSED
chmod 755 "$root/bin/reused-pid-start"
"$root/bin/reused-pid-start"
wait_event 17
[ "$(cat "$root/run/skrashevich.station-airplay/service/boot_id")" = fixture-current-boot ]
"$service" stop
wait_event 18
assert_stopped
# The same PID reuse must also recover an unstamped pre-upgrade lock.
"$root/bin/reused-pid-start" --unstamped
wait_event 19
[ "$(cat "$root/run/skrashevich.station-airplay/service/boot_id")" = fixture-current-boot ]
"$service" stop
wait_event 20
assert_stopped
# Exercise BusyBox's setsid applet with a PATH containing no native setsid.
mkdir "$fixture/busy-bin"
for command in mkdir dirname readlink ps tr rm sleep nohup cat grep; do
    ln -s "$(command -v "$command")" "$fixture/busy-bin/$command"
done
cat > "$fixture/busy-bin/busybox" <<'BUSYBOX'
#!/bin/sh
case "$1" in
    --list) echo setsid ;;
    setsid)
        printf 'busybox\n' >> "$(dirname "$0")/../detach.log"
        shift
        exec "$@"
        ;;
    *) exit 2 ;;
esac
BUSYBOX
chmod 755 "$fixture/busy-bin/busybox"
PATH=$fixture/busy-bin "$service" start
wait_event 21
rg -q '^busybox$' "$fixture/detach.log"
PATH=$fixture/busy-bin "$service" stop
wait_event 22
assert_stopped
rm "$root/bin/station-airplay"
# Removed public service makes a retained init hook inert.
rm "$root/bin/station-airplay-service"
"$hook" start
# The same marker from a different YAM installation is also unowned.
ln -s "$payload/bin/station-airplay-service" "$root/bin/station-airplay-service"
{
    printf '#!/bin/sh\n# station-airplay YAM boot hook v1\n'
    printf 'unset YAM_ROOT YAM_VAR YAM_RUN YAM_PKG YAM_ETC YAM_PLATFORM\n'
    printf "service='/another/yam/bin/station-airplay-service'\n"
} > "$hook"
cp "$hook" "$fixture/foreign-hook"
if YAM_ROOT=$root "$payload/bin/station-airplay-postinstall" --test-root "$fixture"; then exit 1; fi
cmp "$hook" "$fixture/foreign-hook"
# Refuse to overwrite an init script with an unrelated marker too.
printf '#!/bin/sh\n# someone else\n' > "$hook"
if YAM_ROOT=$root "$payload/bin/station-airplay-postinstall" --test-root "$fixture"; then exit 1; fi
rg -q 'someone else' "$hook"
echo 'YAM service fixtures passed'
