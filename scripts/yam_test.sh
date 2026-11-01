#!/bin/sh
set -eu
here=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
work=$(CDPATH= cd "$work" && pwd -P)
trap 'rm -rf "$work"' EXIT HUP INT TERM
root="$work/yam root"
payload="$root/pkgs/skrashevich/station-airplay/0.1.0/files"
mkdir -p "$root/bin" "$payload/bin" "$payload/share/skrashevich.station-airplay/bin"
cp "$here/packaging/yam/launcher.sh" "$payload/bin/station-airplay"
chmod 755 "$payload/bin/station-airplay"
ln -s "$payload/bin/station-airplay" "$root/bin/station-airplay"
cat > "$payload/share/skrashevich.station-airplay/bin/station-airplay" <<'STUB'
#!/bin/sh
printf '%s\n' "$@"
STUB
chmod 755 "$payload/share/skrashevich.station-airplay/bin/station-airplay"
unset YAM_ROOT YAM_VAR
"$root/bin/station-airplay" -name 'A name with spaces' > "$work/actual"
printf '%s\n' -data-dir "$root/var/skrashevich.station-airplay" -name 'A name with spaces' > "$work/expected"
cmp "$work/expected" "$work/actual"
test -d "$root/var/skrashevich.station-airplay"
YAM_VAR="$work/custom var" "$root/bin/station-airplay" -check > "$work/actual"
printf '%s\n' -data-dir "$work/custom var/skrashevich.station-airplay" -check > "$work/expected"
cmp "$work/expected" "$work/actual"
test -d "$work/custom var/skrashevich.station-airplay"
test "$(python3 "$here/scripts/release-version.py" v0.1)" = 0.1.0
test "$(python3 "$here/scripts/release-version.py" v1.2.3)" = 1.2.3
if python3 "$here/scripts/release-version.py" 'v0.1/unsafe' >/dev/null 2>&1; then exit 1; fi

# Check the download/install boundary without network or a real system YAM.
mkdir -p "$work/fixture" "$work/tools"
export YAM_TEST_FIXTURE="$work/fixture"
artifact=station-airplay-0.1.0-cucumber.yampkg
printf 'package fixture\n' > "$YAM_TEST_FIXTURE/$artifact"
(cd "$YAM_TEST_FIXTURE" && sha256sum "$artifact" > SHA256SUMS)
cat > "$work/tools/curl" <<'CURL'
#!/bin/sh
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o) output=$2; shift 2 ;;
        --retry) shift 2 ;;
        https://*) filename=${1##*/}; shift ;;
        *) shift ;;
    esac
done
cp "$YAM_TEST_FIXTURE/$filename" "$output"
CURL
cat > "$work/tools/yam" <<'YAM'
#!/bin/sh
test "$1" = install && test "$2" = --yes
cmp "$3" "$YAM_TEST_FIXTURE/station-airplay-0.1.0-cucumber.yampkg"
touch "$YAM_TEST_FIXTURE/installed"
YAM
chmod 755 "$work/tools/curl" "$work/tools/yam"
PATH="$work/tools:$PATH" sh "$here/scripts/install-yam.sh" cucumber v0.1
test -f "$YAM_TEST_FIXTURE/installed"
rm "$YAM_TEST_FIXTURE/installed"
printf 'corruption\n' >> "$YAM_TEST_FIXTURE/$artifact"
if PATH="$work/tools:$PATH" sh "$here/scripts/install-yam.sh" cucumber v0.1 > "$work/rejected" 2>&1; then
    echo 'installer accepted a corrupted download' >&2; exit 1
fi
test ! -f "$YAM_TEST_FIXTURE/installed"
echo 'YAM launcher, download integrity and release version checks passed'
