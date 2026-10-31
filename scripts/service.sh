#!/bin/sh
# Administrative helper only: BusyBox init executes the receiver directly.
set -eu
umask 077
self=$0
while [ -L "$self" ]; do
 link=$(readlink "$self")
 case "$link" in /*) self=$link;; *) self=$(dirname "$self")/$link;; esac
done
base=$(CDPATH= cd "$(dirname "$self")" && pwd -L)
binary="$base/bin/station-airplay"
inittab=/etc/inittab
[ ! -f "$base/inittab" ] || inittab=$(cat "$base/inittab")
legacy="$(dirname "$inittab")/init.d/S98station-airplay"
begin="# BEGIN station-airplay $base"
end="# END station-airplay $base"
tmp=
remounted=0
locked=0
die() { echo "$*" >&2; exit 1; }
cleanup() {
 result=$?
 trap - EXIT
 [ -z "$tmp" ] || rm -f "$tmp"
 if [ "$remounted" -eq 1 ]; then
  mount -o remount,ro / || { echo 'Failed to restore read-only root filesystem' >&2; result=1; }
 fi
 [ "$locked" -eq 0 ] || rmdir "$base/.service-lock"
 exit "$result"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
lock() {
 mkdir "$base/.service-lock" 2>/dev/null || die 'Another service operation is in progress'
 locked=1
}
supported_init() {
 [ "$inittab" = /etc/inittab ] || return 0
 case "$(readlink /proc/1/exe)" in */busybox) ;; *) die 'Autostart requires BusyBox as PID 1';; esac
}
has_entry() { [ -f "$inittab" ] && grep -Fqx "$begin" "$inittab"; }
quote() {
 value=$1
 nl='
'
 cr=$(printf '\r')
 case "$value" in *"$nl"*|*"$cr"*) die 'Newlines are not allowed in init arguments';; esac
 # BusyBox strips # comments before the shell parses quotes. Generate that
 # character at runtime instead, without introducing another executable.
 hash_word="'\"\$(printf '\\\\043')\"'"
 printf "'"
 printf '%s' "$value" | sed "s/'/'\\\\''/g; s/#/$hash_word/g"
 printf "'"
}
command_line() {
 printf '::respawn:'
 quote "$binary"
 for option in name interface device aplay; do
  printf ' -%s ' "$option"
  quote "$(cat "$base/$option")"
 done
 printf ' -data-dir '
 quote "$base/state"
 printf ' >>'
 quote "$base/receiver.log"
 printf ' 2>&1\n'
}
edit_inittab() {
 want=$1
 supported_init
 [ -f "$inittab" ] && [ ! -L "$inittab" ] || die 'inittab must be a regular file'
 if [ -e "$legacy" ] || [ -L "$legacy" ]; then
  [ -L "$legacy" ] && [ "$(readlink "$legacy")" = "$base/service" ] ||
   die 'Legacy init hook belongs to another installation'
 fi
 line=
 [ "$want" = off ] || line=$(command_line)
 if [ "$inittab" = /etc/inittab ] &&
    awk '$2 == "/" && ("," $4 ",") ~ /,ro,/ { found=1 } END { exit !found }' /proc/mounts; then
  mount -o remount,rw / || die 'Cannot remount root writable'
  remounted=1
 fi
 [ -f "$base/inittab.before-airplay" ] || cp -p "$inittab" "$base/inittab.before-airplay"
 tmp=$(mktemp "$(dirname "$inittab")/.station-airplay.XXXXXX")
 cp -p "$inittab" "$tmp"
 # Preserve unrelated entries; reject malformed blocks and duplicate launches.
 awk -v first="$begin" -v last="$end" -v binary="$binary" -v want="$want" '
  $0 == first { if (inside || seen++) { bad=1; exit 1 }; inside=1; next }
  $0 == last { if (!inside) { bad=1; exit 1 }; inside=0; next }
  !inside {
   if (want == "on" && /^# (BEGIN|END) station-airplay /) { bad=1; exit 1 }
   if ($0 !~ /^#/ && index($0, binary)) { bad=1; exit 1 }
   print
  }
  END { if (inside || bad) exit 1 }
 ' "$inittab" >"$tmp" || die 'Conflicting or malformed inittab; original file preserved'
 if [ "$want" = on ]; then printf '%s\n%s\n%s\n' "$begin" "$line" "$end" >>"$tmp"; fi
 if ! cmp -s "$inittab" "$tmp"; then
  mv "$tmp" "$inittab"
  tmp=
 fi
 if [ -L "$legacy" ]; then rm "$legacy"; fi
 sync
 if [ "$remounted" -eq 1 ]; then
  mount -o remount,ro / || die 'Cannot restore read-only root filesystem'
  remounted=0
 fi
}
reload_init() {
 # Alternate paths are for offline staging; never signal the host init.
 [ "$inittab" = /etc/inittab ] || die 'An alternate inittab can only be staged with --no-start'
 supported_init
 kill -HUP 1
}
pids() {
 real=$(readlink -f "$binary" 2>/dev/null || printf '%s' "$binary")
 for exe in /proc/[0-9]*/exe; do
  target=$(readlink "$exe" 2>/dev/null) || continue
  case "$target" in "$real"|"$real (deleted)")
   pid=${exe#/proc/}; printf '%s\n' "${pid%/exe}";;
  esac
 done
}
running() { [ -n "$(pids)" ]; }
terminate() {
 for pid in $(pids); do kill -TERM "$pid" 2>/dev/null || true; done
 count=0
 while running; do
  [ "$count" -lt 10 ] || die 'Receiver did not stop; refusing SIGKILL to protect ALSA cleanup'
  sleep 1; count=$((count+1))
 done
 rm -f "$base/receiver.pid"
}
wait_running() {
 count=0
 while ! running; do
  [ "$count" -lt 10 ] || { tail -12 "$base/receiver.log" >&2; die 'Receiver did not start'; }
  sleep 1; count=$((count+1))
 done
 echo "Station AirPlay running ($(pids | tr '\n' ' '))"
}
stop() {
 if has_entry || [ -L "$legacy" ]; then
  edit_inittab off
  if [ "$inittab" = /etc/inittab ]; then reload_init; fi
 fi
 terminate
}
enable() {
 was_enabled=0
 if has_entry; then was_enabled=1; fi
 edit_inittab on
 # Do not interrupt a manually launched receiver until configuration is saved.
 if [ "$was_enabled" -eq 0 ]; then terminate; fi
 : >"$base/autostart"
 if [ "${1:-}" != --no-start ]; then reload_init; wait_running; fi
}
case "${1:-status}" in
 start)
  lock
  if [ -f "$base/autostart" ]; then enable
  elif running; then echo "Station AirPlay already running ($(pids))"
  else
   name=$(cat "$base/name"); iface=$(cat "$base/interface")
   device=$(cat "$base/device"); aplay=$(cat "$base/aplay")
   nohup "$binary" -name "$name" -interface "$iface" -device "$device" -aplay "$aplay" -data-dir "$base/state" </dev/null >>"$base/receiver.log" 2>&1 &
   printf '%s\n' "$!" >"$base/receiver.pid"
   sleep 1
   wait_running
  fi;;
 stop) lock; stop;;
 restart) "$self" stop; "$self" start;;
 enable) lock; enable "${2:-}";;
 disable) lock; stop; rm -f "$base/autostart";;
 status)
  if has_entry; then boot=enabled; else boot=disabled; fi
  if running; then echo "Station AirPlay running ($(pids)); autostart $boot"
  else echo "Station AirPlay stopped; autostart $boot"; exit 1; fi;;
 *) die 'Usage: service {start|stop|restart|enable [--no-start]|disable|status}';;
esac
