#!/bin/sh
# Writable-prefix installation, optional direct BusyBox inittab supervision.
set -eu
umask 077
here=$(CDPATH= cd "$(dirname "$0")" && pwd)
prefix=/data/opt/station-airplay
binary="$here/../station-airplay"
inittab=
name=
iface=
device=
aplay=
autostart=0
no_start=0
uninstall=0
purge=0
while [ "$#" -gt 0 ]; do
 case "$1" in
  --prefix) prefix=$2; shift 2;;
  --binary) binary=$2; shift 2;;
  --inittab) inittab=$2; shift 2;;
  --name) name=$2; shift 2;;
  --interface) iface=$2; shift 2;;
  --device) device=$2; shift 2;;
  --aplay) aplay=$2; shift 2;;
  --autostart) autostart=1; shift;;
  --no-start) no_start=1; shift;;
  --uninstall) uninstall=1; shift;;
  --purge) purge=1; shift;;
  *) echo "Unknown option: $1" >&2; exit 2;;
 esac
done
die() { echo "$*" >&2; exit 1; }
case "$prefix" in /*) ;; *) die '--prefix must be absolute';; esac
case "$prefix" in /|/data|/data/opt|/tmp|/usr|/etc|*//*|*/./*|*/.|*/|*/../*|*/..|*[!A-Za-z0-9_./-]*) die 'Unsafe installation prefix';; esac
nl='
'
cr=$(printf '\r')
for value in "$name" "$iface" "$device" "$aplay" "$inittab"; do
 case "$value" in *"$nl"*|*"$cr"*) die 'Newlines are not allowed in configuration';; esac
done
marker="$prefix/.station-airplay"
if [ -f "$marker" ] && [ "$(cat "$marker")" != station-airplay-v1 ]; then die 'Unknown installation marker'; fi
if [ -d "$prefix" ] && [ ! -f "$marker" ] && [ -n "$(ls -A "$prefix")" ]; then
 die 'Existing prefix is unmanaged; refusing to adopt it'
fi
if [ -f "$prefix/inittab" ]; then
 previous=$(cat "$prefix/inittab")
 [ -z "$inittab" ] || [ "$inittab" = "$previous" ] || die 'Changing the inittab path requires uninstall first'
 inittab=$previous
fi
inittab=${inittab:-/etc/inittab}
case "$inittab" in /*) ;; *) die '--inittab must be absolute';; esac
if [ "$inittab" != /etc/inittab ] && [ "$no_start" -eq 0 ]; then
 die '--inittab is for offline staging and requires --no-start'
fi
legacy="$(dirname "$inittab")/init.d/S98station-airplay"
if [ -e "$legacy" ] || [ -L "$legacy" ]; then
 [ -L "$legacy" ] && [ "$(readlink "$legacy")" = "$prefix/service" ] ||
  die 'Legacy init hook belongs to another installation'
 autostart=1
fi
[ ! -f "$prefix/autostart" ] || autostart=1
if [ "$uninstall" -eq 1 ]; then
 [ -f "$marker" ] || die 'No managed installation'
 cp "$here/service.sh" "$prefix/.service.new"; chmod 700 "$prefix/.service.new"
 mv "$prefix/.service.new" "$prefix/service"
 printf '%s\n' "$inittab" >"$prefix/inittab"
 "$prefix/service" disable
 rm -f "$prefix/bin/station-airplay" "$prefix/service" "$prefix/receiver.pid" "$prefix/receiver.log"
 rmdir "$prefix/bin" 2>/dev/null || true
 if [ "$purge" -eq 1 ]; then
  rm -rf "$prefix/state"
  rm -f "$prefix/name" "$prefix/interface" "$prefix/device" "$prefix/aplay"
  # Keep the backup and installation marker so reinstall remains possible.
  rmdir "$prefix" 2>/dev/null || true
 fi
 echo 'Station AirPlay uninstalled; original inittab backup retained'
 exit 0
fi
[ -x "$binary" ] || die "Executable not found: $binary"
"$binary" -version
mkdir -p "$prefix/bin" "$prefix/state"
printf '%s\n' station-airplay-v1 >"$marker"
new="$prefix/bin/.station-airplay.new.$$"
trap 'rm -f "$new" "$prefix/.service.new"' EXIT HUP INT TERM
cp "$binary" "$new"; chmod 700 "$new"
"$new" -version
cp "$here/service.sh" "$prefix/.service.new"; chmod 700 "$prefix/.service.new"
mv "$prefix/.service.new" "$prefix/service"
printf '%s\n' "$inittab" >"$prefix/inittab"
# Stop and remove respawn before replacing the executable or its arguments.
"$prefix/service" stop
mv "$new" "$prefix/bin/station-airplay"
if [ -n "$name" ]; then printf '%s\n' "$name" >"$prefix/name"
elif [ ! -f "$prefix/name" ]; then printf '%s AirPlay\n' "$(hostname)" >"$prefix/name"; fi
if [ ! -f "$prefix/interface" ] || [ -n "$iface" ]; then printf '%s\n' "$iface" >"$prefix/interface"; fi
if [ ! -f "$prefix/device" ] || [ -n "$device" ]; then printf '%s\n' "${device:-default}" >"$prefix/device"; fi
if [ ! -f "$prefix/aplay" ] || [ -n "$aplay" ]; then printf '%s\n' "${aplay:-aplay}" >"$prefix/aplay"; fi
if [ "$autostart" -eq 1 ]; then
 if [ "$no_start" -eq 1 ]; then "$prefix/service" enable --no-start
 else "$prefix/service" enable; fi
elif [ "$no_start" -eq 0 ]; then "$prefix/service" start; fi
echo "Installed in $prefix"
