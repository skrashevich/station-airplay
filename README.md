# Station AirPlay

A standalone AirPlay 2 buffered-audio receiver in Go for rooted Linux Yandex Stations.
Protocol basis: [ibiscum/goplay2](https://github.com/ibiscum/goplay2), commit `91a0a06`.
AAC decoding: [skrashevich/go-aac](https://github.com/skrashevich/go-aac).

The receiver and decoder build with **CGO_ENABLED=0**. Output uses the existing
firmware `aplay -D default`, which preserves the device's ALSA plugin/mixer path.
No FDK-AAC, PortAudio, PulseAudio, FFmpeg, shared-library loading, or Go runtime
installation is required on the speaker. FFmpeg was used only to generate the
synthetic test fixtures on the development host.

## Requirements and compatibility

- Rooted Linux with the existing `aplay` and a working ALSA `default` playback PCM.
- Linux ARM64 or ARMv7; choose the architecture, not the Station model.
- iPhone and receiver on a network allowing mDNS multicast and direct TCP/UDP.
- TCP 7000 by default, dynamically allocated event/media TCP ports, UDP 319/320
  for PTP, UDP 5353 for mDNS. Existing port conflicts fail without modifying services.
- AAC-LC stereo, 44100/48000 Hz, AirPlay 2 buffered TCP audio (stream type 103).
  The speaker's ALSA configuration performs hardware rate/channel conversion.
- Software volume affects only this receiver's samples. Global mixer controls,
  audio configuration, OTA settings and Quasar processes are not modified.
  Optional boot installation changes only the receiver's block in `/etc/inittab`.

AirPlay 1, ALAC, realtime UDP audio, video/screen mirroring and precision multiroom
synchronization are not claimed. Basic group playback was confirmed by the user on Midi and Station 2 from one
iPhone. Sample-accurate synchronization, long-term drift and relayed HomePod timing
have not been validated. Relayed grandmaster timing and silent-clock wakeup
are not implemented/validated for mixed HomePod groups.
Applications requesting unsupported codecs/transports receive an explicit error.
Opus is not used by this AAC transport and is not linked merely because a decoder is available.

Live iPhone discovery and individual playback have been observed on Midi and
Station 2. See [validation notes](docs/validation.md) for limitations and current results.

## Install on a speaker

Extract the matching archive into a temporary directory on the speaker, then run:

```sh
sh scripts/install.sh --name 'My Station AirPlay'
```

The default prefix is `/data/opt/station-airplay`. It contains the binary, an
administrative `service` helper, configuration text files, private pairing state
and `receiver.log`. A manually launched instance also has a PID file.
Installation starts the receiver but does **not** change boot configuration.

```sh
/data/opt/station-airplay/service status
/data/opt/station-airplay/service stop
/data/opt/station-airplay/service start
```

To install with boot autostart:

```sh
sh scripts/install.sh --name 'My Station AirPlay' --autostart
```

This uses the existing **BusyBox init**, without adding an init script or changing
Quasar's launcher. The installer saves `inittab.before-airplay` in the prefix,
temporarily remounts `/` writable when needed, atomically updates `/etc/inittab`,
restores its original read-only mount state, then sends SIGHUP to PID 1. Root must
permit remounting the filesystem. An existing writable root remains writable.

The managed block has one `::respawn:` command that directly executes the binary,
with literal quoted arguments and log redirection. BusyBox uses `sh -c 'exec …'`
for the redirection; the shell is replaced by the receiver, whose parent is PID 1.
The `service` helper is **not** invoked at boot. Init starts the receiver after
`rcS` completes and restarts it when it exits. If DHCP is not ready, the receiver
can exit and init retries. This does not guarantee later recovery from an IP
address change while the receiver is already running.

On an existing installation, autostart can be enabled or removed with:

```sh
/data/opt/station-airplay/service enable
/data/opt/station-airplay/service disable
```

In supervised mode, `stop` removes the respawn entry and stops the receiver; it
therefore also prevents startup on the next boot. `start` restores the entry and
starts the receiver again. `disable` additionally forgets supervised mode, so a
later `start` uses manual mode. `restart` stops and starts using the selected mode.
Do not use `kill -9`: init would restart the receiver and ALSA cleanup could be
interrupted. No global init pause or vendor-service restart is used.

Installation/update preserves existing autostart. `--no-start` stops an existing
instance and stages the new files; with autostart selected, it writes the boot
entry without reloading init, for activation on the next boot. A legacy
`S98station-airplay` symlink is migrated only when it belongs to this installation.
Foreign hooks, conflicting launches and malformed managed blocks are rejected.
Firmware OTA can replace `/etc/inittab`; rerun the installer with `--autostart`
after such an update.

Update by running the installer with the new archive. Pairing identity survives
updates and name changes. Uninstall first removes only the managed boot block,
reloads init and waits for graceful shutdown, then removes the program. It retains
pairing state unless explicitly purged:

```sh
sh scripts/install.sh --uninstall
sh scripts/install.sh --uninstall --purge
```

The original inittab backup and installation metadata remain even with `--purge`;
the saved file is not copied wholesale over the live configuration, since other
services may have changed it since installation. Custom locations use `--prefix`
(an absolute path without whitespace, shell characters or dot segments), and local
builds can pass `--binary /path/to/station-airplay`. A nonempty unmanaged prefix is
rejected. `--inittab /absolute/staging/etc/inittab --no-start` supports offline
image staging and tests; it never signals the development host's init.

## Run manually

```sh
./station-airplay -name 'My Station AirPlay' -data-dir /data/opt/station-airplay/state
./station-airplay -check
./station-airplay -help
```

Options include `-interface`, `-listen`, `-aplay`, `-device`, `-volume` (dB) and
`-output-delay` (duration, default 160ms). Interface selection is automatic unless
specified. The common device is `default`; selecting `hw:*` bypasses vendor mixing.
Never use a display-name change as a reason to delete the persistent data directory.

## Build and test

Go 1.26 or later:

```sh
CGO_ENABLED=0 go test ./...
go test -race ./...
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '-s -w' -o station-airplay .
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags '-s -w' -o station-airplay .
sh scripts/install_test.sh
sh scripts/inittab_test.sh
```

`go test -race` uses the host race instrumentation; distributed receiver binaries
are always built with `CGO_ENABLED=0`. Audit the compiled dependency graph:

```sh
CGO_ENABLED=0 go list -deps -f '{{if .CgoFiles}}{{.ImportPath}} {{.CgoFiles}}{{end}}' ./...
```

The output must be empty. The synthetic sender exercises real SRP, encrypted RTSP,
authenticated buffered AAC, and output delivery, but is not a substitute for an iPhone:

```sh
CGO_ENABLED=0 go run ./cmd/airplay-probe -address RECEIVER_IP:7000 \
  -aac codec/testdata/tone.aac -volume -35
```

## Troubleshooting

Logs are in the installation prefix's `receiver.log`. `-check` verifies executable
and interface prerequisites without opening audio. If discovery works but playback
does not, inspect codec/setup errors, PTP timing-wait diagnostics and whether aplay
is running. Do not change global mixer controls to repair a timing problem.

The receiver stops aplay with SIGTERM and waits for ALSA cleanup, with a bounded
kill fallback. Abruptly killing an ALSA dmix owner can strand IPC state; normal
pause/teardown must use the managed lifecycle rather than `kill -9`.

## Attribution

Derived protocol files retain the goplay2 Apache-2.0 license and modification
notices. AAC is the pinned LGPL-3.0 Go decoder; its license is included under
`docs/licenses`. Module source revisions and checksums are in `go.mod`/`go.sum`.
The PTP FollowUp/profile and timeline handling was cross-checked against the public
Shairport Sync and NQPTP implementations. See `NOTICE` and `LICENSE`.
