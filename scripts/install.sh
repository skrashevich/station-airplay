#!/bin/sh
# Install only our writable prefix; never remount or modify vendor services.
set -eu
umask 077
here=$(CDPATH= cd "$(dirname "$0")" && pwd)
prefix=/data/opt/station-airplay
binary="$here/../station-airplay"
name=
iface=
device=
aplay=
aplay_set=0
autostart=0
no_start=0
uninstall=0
purge=0
while [ "$#" -gt 0 ];do
 case "$1" in
  --prefix) prefix=$2;shift 2;;
  --binary) binary=$2;shift 2;;
  --name) name=$2;shift 2;;
  --interface) iface=$2;shift 2;;
  --device) device=$2;shift 2;;
  --aplay) aplay=$2;aplay_set=1;shift 2;;
  --autostart) autostart=1;shift;;
  --no-start) no_start=1;shift;;
  --uninstall) uninstall=1;shift;;
  --purge) purge=1;shift;;
  *) echo "Unknown option: $1" >&2;exit 2;;
 esac
done
case "$prefix" in /*) ;; *) echo '--prefix must be absolute' >&2;exit 2;; esac
case "$prefix" in /|/data|/data/opt|/tmp|/usr|/etc) echo 'Refusing a shared prefix' >&2;exit 2;; esac
marker="$prefix/.station-airplay"
init=/etc/init.d/S98station-airplay
if [ -f "$marker" ] && [ "$(cat "$marker")" != station-airplay-v1 ];then echo 'Unknown installation marker' >&2;exit 1;fi
if [ -d "$prefix" ] && [ ! -f "$marker" ];then
 if [ -n "$(ls -A "$prefix")" ];then echo 'Existing prefix is unmanaged; refusing to adopt it' >&2;exit 1;fi
fi
if [ "$uninstall" -eq 1 ];then
 [ -f "$marker" ] || { echo 'No managed installation' >&2;exit 1; }
 if [ -x "$prefix/service" ];then "$prefix/service" stop;fi
 if [ -L "$init" ] && [ "$(readlink "$init")" = "$prefix/service" ];then rm "$init";fi
 rm -f "$prefix/bin/station-airplay" "$prefix/bin/station-pcm" "$prefix/bin/station-pcm-armv7" "$prefix/bin/station-pcm-arm64" "$prefix/service" "$prefix/receiver.pid" "$prefix/receiver.log"
 rmdir "$prefix/bin" 2>/dev/null || true
 if [ "$purge" -eq 1 ];then
  rm -rf "$prefix/state"
  rm -f "$prefix/name" "$prefix/interface" "$prefix/device" "$prefix/aplay" "$marker"
  rmdir "$prefix" 2>/dev/null || true
 fi
 echo 'Station AirPlay uninstalled; pairing state retained unless --purge was requested'
 exit 0
fi
[ -x "$binary" ] || { echo "Executable not found: $binary" >&2;exit 1; }
"$binary" -version
effective_aplay=$aplay
if [ "$aplay_set" -eq 0 ] && [ -f "$prefix/aplay" ];then effective_aplay=$(cat "$prefix/aplay");fi
# Older installers wrote their implicit system-aplay default to this file.
# Migrate it to the executable's embedded player. Explicit overrides
# (including a new --aplay aplay) remain the user's choice.
if [ "$aplay_set" -eq 0 ] && [ "$effective_aplay" = aplay ];then effective_aplay=;fi
if [ "$autostart" -eq 1 ];then
 [ -d /etc/init.d ] && [ -w /etc/init.d ] || { echo 'Autostart requires an already writable /etc/init.d; no remount will be attempted' >&2;exit 1; }
 if [ -e "$init" ] || [ -L "$init" ];then
  [ -L "$init" ] && [ "$(readlink "$init")" = "$prefix/service" ] || { echo 'Autostart path belongs to another service' >&2;exit 1; }
 fi
fi
mkdir -p "$prefix/state"
printf '%s\n' station-airplay-v1 >"$marker"
stage=$(mktemp -d "$prefix/.install.XXXXXX")
switched=0
had_bin=0
was_running=0
cleanup() {
 result=$?
 trap - EXIT HUP INT TERM
 if [ "$result" -ne 0 ] && [ "$switched" -eq 1 ]; then
  "$prefix/service" stop || true
  rm -rf "$prefix/bin"
  if [ "$had_bin" -eq 1 ]; then mv "$stage/old-bin" "$prefix/bin"; fi
  for file in service name interface device aplay; do
   if [ -f "$stage/old/$file" ]; then cp -p "$stage/old/$file" "$prefix/$file"; else rm -f "$prefix/$file"; fi
  done
  if [ "$was_running" -eq 1 ]; then "$prefix/service" start || echo 'Previous service could not restart; inspect receiver.log.' >&2; fi
 fi
 rm -rf "$stage"
 exit "$result"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
mkdir "$stage/bin" "$stage/old" "$stage/settings"
if [ -d "$prefix/bin" ]; then cp -R "$prefix/bin/." "$stage/bin/"; fi
for file in service name interface device aplay; do
 if [ -f "$prefix/$file" ]; then cp -p "$prefix/$file" "$stage/old/$file"; cp -p "$prefix/$file" "$stage/settings/$file"; fi
done
cp "$binary" "$stage/bin/station-airplay";chmod 700 "$stage/bin/station-airplay"
"$stage/bin/station-airplay" -version
# Retire sidecars from earlier experimental packages; the new executable owns
# its compressed payload and extracts only a private runtime cache when needed.
rm -f "$stage/bin/station-pcm" "$stage/bin/station-pcm-armv7" "$stage/bin/station-pcm-arm64"
cp "$here/service.sh" "$stage/settings/service";chmod 700 "$stage/settings/service"
if [ -n "$name" ];then printf '%s\n' "$name" >"$stage/settings/name";elif [ ! -f "$stage/settings/name" ];then printf '%s AirPlay\n' "$(hostname)" >"$stage/settings/name";fi
if [ ! -f "$stage/settings/interface" ] || [ -n "$iface" ];then printf '%s\n' "$iface" >"$stage/settings/interface";fi
if [ ! -f "$stage/settings/device" ] || [ -n "$device" ];then printf '%s\n' "${device:-default}" >"$stage/settings/device";fi
printf '%s\n' "$effective_aplay" >"$stage/settings/aplay"
# Check the staged output and configuration before stopping a working receiver.
# Check opens neither audio nor receiver ports and creates no pairing identity.
"$stage/bin/station-airplay" -check -name "$(cat "$stage/settings/name")" \
 -interface "$(cat "$stage/settings/interface")" -device "$(cat "$stage/settings/device")" \
 -aplay "$(cat "$stage/settings/aplay")" -data-dir "$prefix/state"
if [ -x "$prefix/service" ];then
 if "$prefix/service" status >/dev/null 2>&1;then was_running=1;fi
 "$prefix/service" stop
fi
if [ -d "$prefix/bin" ]; then mv "$prefix/bin" "$stage/old-bin";had_bin=1;fi
switched=1
mv "$stage/bin" "$prefix/bin"
for file in service name interface device aplay; do mv "$stage/settings/$file" "$prefix/$file";done
if [ "$no_start" -eq 0 ];then "$prefix/service" start;fi
if [ "$autostart" -eq 1 ] && [ ! -L "$init" ];then ln -s "$prefix/service" "$init";fi
echo "Installed in $prefix"
