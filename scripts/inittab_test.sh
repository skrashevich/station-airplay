#!/bin/sh
# Exercise real installation/configuration in a staged filesystem, never PID 1.
set -eu
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT HUP INT TERM
here=$(CDPATH= cd "$(dirname "$0")" && pwd)
mkdir -p "$root/etc/init.d"
inittab="$root/etc/inittab"
printf '%s\n' '# existing configuration' '::sysinit:/etc/init.d/rcS' 'console::respawn:/sbin/getty console' >"$inittab"
cp "$inittab" "$root/original"
chmod 644 "$inittab"
cat >"$root/binary" <<'BIN'
#!/bin/sh
if [ "${1:-}" = -version ]; then echo 'station-airplay test'; exit; fi
printf '%s\n' "$@" >"$(dirname "$0")/../arguments"
BIN
chmod +x "$root/binary"
prefix="$root/receiver"
name="Station's \$(touch $root/injected) music #2"
sh "$here/install.sh" --prefix "$prefix" --inittab "$inittab" --binary "$root/binary" --name "$name" --autostart --no-start
test -x "$prefix/bin/station-airplay"
test ! -e "$root/etc/init.d/S98station-airplay"
test "$(LC_ALL=C ls -l "$inittab" | cut -c1-10)" = '-rw-r--r--'
cmp "$root/original" "$prefix/inittab.before-airplay"
test "$(awk '/::respawn:.*station-airplay/ { n++ } END { print n+0 }' "$inittab")" = 1
# Run the emitted init command: verify literal arguments and no shell injection.
command=$(sed -n 's/^::respawn://p' "$inittab" | tail -1 | sed 's/#.*//')
sh -c "exec $command"
test "$(sed -n '2p' "$prefix/arguments")" = "$name"
test ! -e "$root/injected"
printf '%s' pairing >"$prefix/state/retained"
# An upgrade retains autostart without requiring the option again.
sh "$here/install.sh" --prefix "$prefix" --inittab "$inittab" --binary "$root/binary" --name Updated --no-start
test "$(awk '/::respawn:.*station-airplay/ { n++ } END { print n+0 }' "$inittab")" = 1
test "$(cat "$prefix/state/retained")" = pairing
command=$(sed -n 's/^::respawn://p' "$inittab" | tail -1)
sh -c "exec $command"
test "$(sed -n '2p' "$prefix/arguments")" = Updated
# Stop removes the respawn entry but remembers supervised mode for next start.
"$prefix/service" stop
cmp "$root/original" "$inittab"
test -f "$prefix/autostart"
"$prefix/service" enable --no-start
"$prefix/service" disable
cmp "$root/original" "$inittab"
test ! -e "$prefix/autostart"
# Migrate only a legacy hook owned by this prefix.
ln -s "$prefix/service" "$root/etc/init.d/S98station-airplay"
sh "$here/install.sh" --prefix "$prefix" --inittab "$inittab" --binary "$root/binary" --autostart --no-start
test ! -L "$root/etc/init.d/S98station-airplay"
sh "$here/install.sh" --prefix "$prefix" --inittab "$inittab" --uninstall --no-start
cmp "$root/original" "$inittab"
test -f "$prefix/state/retained"
test ! -e "$prefix/bin/station-airplay"
# Do not adopt or remove somebody else's init hook.
ln -s /foreign/service "$root/etc/init.d/S98station-airplay"
if sh "$here/install.sh" --prefix "$prefix" --inittab "$inittab" --binary "$root/binary" --autostart --no-start 2>"$root/error"; then
 echo 'accepted a foreign init hook' >&2; exit 1
fi
test "$(readlink "$root/etc/init.d/S98station-airplay")" = /foreign/service
cmp "$root/original" "$inittab"
rm "$root/etc/init.d/S98station-airplay"
# Purge removes identity but preserves a reinstallable backup/ownership marker.
sh "$here/install.sh" --prefix "$prefix" --inittab "$inittab" --binary "$root/binary" --autostart --no-start
sh "$here/install.sh" --prefix "$prefix" --inittab "$inittab" --uninstall --purge --no-start
test ! -e "$prefix/state"
cmp "$root/original" "$prefix/inittab.before-airplay"
# Malformed owned blocks must leave the original file intact.
sh "$here/install.sh" --prefix "$prefix" --inittab "$inittab" --binary "$root/binary" --no-start
printf '# BEGIN station-airplay %s\n' "$prefix" >>"$inittab"
cp "$inittab" "$root/malformed"
if "$prefix/service" enable --no-start 2>"$root/error"; then
 echo 'accepted an unterminated inittab block' >&2; exit 1
fi
cmp "$root/malformed" "$inittab"
echo 'inittab installation, command quoting, update, stop, migration and uninstall passed'
