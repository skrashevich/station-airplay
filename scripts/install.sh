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
  --aplay) aplay=$2;shift 2;;
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
 rm -f "$prefix/bin/station-airplay" "$prefix/service" "$prefix/receiver.pid" "$prefix/receiver.log"
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
if [ "$autostart" -eq 1 ];then
 [ -d /etc/init.d ] && [ -w /etc/init.d ] || { echo 'Autostart requires an already writable /etc/init.d; no remount will be attempted' >&2;exit 1; }
 if [ -e "$init" ] || [ -L "$init" ];then
  [ -L "$init" ] && [ "$(readlink "$init")" = "$prefix/service" ] || { echo 'Autostart path belongs to another service' >&2;exit 1; }
 fi
fi
mkdir -p "$prefix/bin" "$prefix/state"
printf '%s\n' station-airplay-v1 >"$marker"
new="$prefix/bin/.station-airplay.new.$$"
trap 'rm -f "$new"' EXIT HUP INT TERM
cp "$binary" "$new";chmod 700 "$new"
"$new" -version
if [ -x "$prefix/service" ];then "$prefix/service" stop;fi
mv "$new" "$prefix/bin/station-airplay"
cp "$here/service.sh" "$prefix/service";chmod 700 "$prefix/service"
if [ -n "$name" ];then printf '%s\n' "$name" >"$prefix/name";elif [ ! -f "$prefix/name" ];then printf '%s AirPlay\n' "$(hostname)" >"$prefix/name";fi
if [ ! -f "$prefix/interface" ] || [ -n "$iface" ];then printf '%s\n' "$iface" >"$prefix/interface";fi
if [ ! -f "$prefix/device" ] || [ -n "$device" ];then printf '%s\n' "${device:-default}" >"$prefix/device";fi
if [ ! -f "$prefix/aplay" ] || [ -n "$aplay" ];then printf '%s\n' "${aplay:-aplay}" >"$prefix/aplay";fi
if [ "$autostart" -eq 1 ] && [ ! -L "$init" ];then ln -s "$prefix/service" "$init";fi
if [ "$no_start" -eq 0 ];then "$prefix/service" start;fi
echo "Installed in $prefix"
