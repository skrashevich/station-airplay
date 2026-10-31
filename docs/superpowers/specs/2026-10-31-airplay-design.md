# Station AirPlay: implementation design

Build a standalone AirPlay audio receiver for rooted Yandex Stations. The user specified Go only, no cgo, goplay2 as the protocol reference, a common audio path without per-model branches, and minimal changes to the device. The user explicitly approved using the existing `aplay -D default` executable. Use their go-aac decoder; add go-opus only if a negotiated protocol mode actually requires it.

## Decisions

- A static `CGO_ENABLED=0` Go server; no FDK, PortAudio, PulseAudio, FFmpeg, dynamic-library trampolines, or native codec dependencies. Existing firmware aplay/libasound handles ALSA plugins.
- Protocol baseline is ibiscum/goplay2 commit `91a0a06b56a5b7ddbb573fe007f36d913d39cb02`: Bonjour discovery, HomeKit pairing/control encryption, RTSP, AirPlay 2 buffered TCP audio, AAC-LC and PTP timing. Retain upstream attribution and license; changed upstream files carry modification notices.
- AAC decoding uses github.com/skrashevich/go-aac at a pinned revision. PCM is stereo S16_LE at the negotiated supported rate (44100 or 48000); ALSA default converts to hardware format. No model names, hardcoded IPs, register writes or replacement asound.conf.
- Common audio backend: start aplay only during playback, feed PCM to stdin, manage pause/flush/teardown and child exit, and terminate/reap on receiver shutdown. Apply AirPlay volume to our samples, never global mixer controls. Cap gain, handle mute and nonfinite values. PCM metadata must match the aplay arguments.
- One active audio session per receiver; independent control connections and pairing exchanges must not corrupt each other. Reject unsupported streams/formats instead of advertising fabricated capabilities. Only claim compatibility actually tested; AirPlay 1/ALAC/UDP media and sample-accurate multiroom are outside this initial goplay2-derived mode.
- Bounded input sizes, queues, TCP packet lengths and read deadlines. Invalid/unauthenticated media cannot panic or reach the speaker. Flush/reconnect discard stale audio and reset decoder state where needed.
- Stable persistent accessory identity and pairing keys live in a caller-selected writable data directory, independent of advertised name. Network interface can be selected explicitly or discovered from usable non-loopback interfaces. Port conflicts fail clearly without changing firmware services. PTP offsets are virtual; never set the system clock.
- Package static Linux ARM64 and ARMv7 binaries. Generic installation uses a writable /data/opt directory. No firmware remount, OTA change or Quasar interception. Autostart is optional and only adds a dedicated SysV service when the init directory is already writable. Uninstall removes only owned files/service.

## Verification

Add regression tests before modifications: malformed RTSP/content lengths and concurrent clients; authenticated/tampered/truncated RTP; real AAC fixtures with independent expected tone/PCM properties; volume/mute; bounded playback/flush/cancel; identity persistence. Run the complete suite, race tests where supported, vet, CGO_ENABLED=0 dependency audit and ARM64/ARMv7 cross-builds. Start temporary instances and test discovery, RTSP and audio paths on both 192.168.88.66 and 192.168.88.24 without permanent boot changes. A real iPhone playback test requires the user's device and must be reported separately from synthetic sender tests.

## Builder discretion

Module/package structure, CLI spelling, implementation of bounded queues and clock scheduling, installer details, and dependency versions may be decided during implementation. Keep persistent identities private and avoid logging pairing secrets. Changes to go-aac itself are outside scope unless a reproducible required decode defect is found; prefer a minimal documented patch within the receiver only when justified.
