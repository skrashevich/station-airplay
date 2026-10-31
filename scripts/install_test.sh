#!/bin/sh
set -eu
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT HUP INT TERM
here=$(CDPATH= cd "$(dirname "$0")" && pwd)
cat > "$root/binary" <<'BIN'
#!/bin/sh
printf '%s\n' 'station-airplay test'
BIN
chmod +x "$root/binary"
prefix="$root/receiver"
sh "$here/install.sh" --prefix "$prefix" --binary "$root/binary" --name 'A name with spaces' --no-start
test -x "$prefix/bin/station-airplay"
test "$(cat "$prefix/name")" = 'A name with spaces'
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
echo 'installer lifecycle passed'
