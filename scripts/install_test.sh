#!/bin/sh
set -eu
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT HUP INT TERM
here=$(CDPATH= cd "$(dirname "$0")" && pwd)
cat > "$root/binary" <<'BIN'
#!/bin/sh
# Fixture for a self-contained receiver that passes -version and -check.
printf '%s\n' 'station-airplay test'
BIN
chmod +x "$root/binary"
prefix="$root/receiver"
sh "$here/install.sh" --prefix "$prefix" --binary "$root/binary" --aplay custom-aplay --name 'A name with spaces' --no-start
test -x "$prefix/bin/station-airplay"
test "$(cat "$prefix/name")" = 'A name with spaces'
test "$(cat "$prefix/aplay")" = custom-aplay
printf '%s' pairing > "$prefix/state/retained"
sh "$here/install.sh" --prefix "$prefix" --binary "$root/binary" --no-start
test "$(cat "$prefix/state/retained")" = pairing
printf '%s' unrelated > "$prefix/unrelated"
sh "$here/install.sh" --prefix "$prefix" --uninstall --no-start
test -f "$prefix/state/retained"
test -f "$prefix/unrelated"
test ! -e "$prefix/bin/station-airplay"
mkdir -p "$root/foreign"
printf '%s' user-file > "$root/foreign/existing"
if sh "$here/install.sh" --prefix "$root/foreign" --binary "$root/binary" --no-start 2>/dev/null; then
 echo 'installer adopted an unmanaged directory' >&2;exit 1
fi
test -f "$root/foreign/existing"

# A complete --binary is sufficient: no installed launcher or helper files.
owned="$root/owned"
sh "$here/install.sh" --prefix "$owned" --binary "$root/binary" --no-start
test "$(cat "$owned/aplay")" = ''
set -- "$owned/bin/"*
test "$#" -eq 1 && test "$1" = "$owned/bin/station-airplay"
printf retained > "$owned/state/retained"
printf 'aplay\n' > "$owned/aplay" # Previous installer's implicit default.
for file in station-pcm station-pcm-armv7 station-pcm-arm64; do
 printf legacy-sidecar > "$owned/bin/$file"
done
cp "$root/binary" "$root/updated-binary"
printf '# updated embedded payload fixture\n' >> "$root/updated-binary"
sh "$here/install.sh" --prefix "$owned" --binary "$root/updated-binary" --no-start
test "$(cat "$owned/aplay")" = ''
cmp "$root/updated-binary" "$owned/bin/station-airplay"
set -- "$owned/bin/"*
test "$#" -eq 1 && test "$1" = "$owned/bin/station-airplay"
test "$(cat "$owned/state/retained")" = retained
sh "$here/install.sh" --prefix "$owned" --binary "$root/updated-binary" --aplay custom-aplay --no-start
sh "$here/install.sh" --prefix "$owned" --binary "$root/updated-binary" --no-start
test "$(cat "$owned/aplay")" = custom-aplay
sh "$here/install.sh" --prefix "$owned" --binary "$root/updated-binary" --aplay '' --no-start
test "$(cat "$owned/aplay")" = ''

# Failed preflight does not stop the old service or publish the candidate.
cp "$owned/bin/station-airplay" "$root/original-binary"
cat > "$owned/service" <<'SERVICE'
#!/bin/sh
case "$1" in
 status) exit 0 ;;
 start) : > "$(dirname "$0")/restored-running" ;;
 stop) : > "$(dirname "$0")/service-stopped" ;;
esac
SERVICE
chmod 755 "$owned/service"
cat > "$root/slow-check" <<'SLOW'
#!/bin/sh
case "$1" in
 -version) exit 0 ;;
 -check) sleep 2;exit 7 ;;
 *) exit 0 ;;
esac
SLOW
chmod 755 "$root/slow-check"
if sh "$here/install.sh" --prefix "$owned" --binary "$root/slow-check" --name rejected-name >"$root/slow.log" 2>&1; then
 echo 'installer accepted a failing embedded PCM/config preflight' >&2;exit 1
fi
test ! -f "$owned/service-stopped"
cmp "$root/original-binary" "$owned/bin/station-airplay"
test "$(cat "$owned/name")" != rejected-name

# Failed candidate startup restores the entire old executable and settings.
if sh "$here/install.sh" --prefix "$owned" --binary "$root/binary" --name rejected-name >"$root/rejected.log" 2>&1; then
 echo 'installer did not report failed candidate startup' >&2;exit 1
fi
cmp "$root/original-binary" "$owned/bin/station-airplay"
test -f "$owned/restored-running"
test "$(cat "$owned/name")" != rejected-name
sh "$here/install.sh" --prefix "$owned" --uninstall --no-start
test ! -e "$owned/bin/station-airplay"
test -f "$owned/state/retained"
echo 'installer lifecycle passed'
