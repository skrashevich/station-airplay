# Realtime audio: scope and verification

Quasar Bridge issue #27 was reproduced on a Midi with the same iPhone:
Apple Music played buffered AAC; Yandex Music established a control session
but did not request a media stream. Advertising ALAC in a diagnostic build
caused two requests for `type=96`, `ct=2`, `audioFormat=0x40000`, `sr=44100`,
`spf=352`, with a 32-byte shared key. The old buffered-only handler returned
415. The diagnostic build was then removed from the speaker.

## Implementation boundary

The fix belongs in station-airplay. All new runtime code must work with
`CGO_ENABLED=0`, including the ALAC decoder. Quasar Bridge only consumes the
library and packages its existing PCM output. No external audio decoder,
FFmpeg executable, C library or extra installed daemon is required.

Support the captured realtime profile: signed 16-bit stereo ALAC at
44100 Hz, 352 samples per packet. Keep buffered AAC stereo at 44100/48000 Hz.
Do not advertise other formats or RTP redundancy without implementing them.

- `codec/alac.go` adapts the pinned pure-Go saprobe-alac packet decoder.
  The independent compressed fixture must decode bit-exactly to checked-in PCM.
- `audio/realtime.go` owns the UDP data and control sockets, authenticated
  RTP framing, bounded reordering, duplicate/late rejection and retransmission.
  Media is accepted only from the authenticated control peer's IP.
- UDP control packet `0xd7` maps RTP to the existing PTP clock; realtime
  playback must not wait for a buffered-only SETRATEANCHORTIME request.
- Existing Player ownership, software volume, PCM output and shutdown remain
  common to both modes. Close/Wait must join both UDP workers and playout.
- SETUP returns the actual type96 UDP ports. FLUSH resets the realtime queue
  using RTP-Info without writing stale decoded audio after its generation ends.
- Advertise ALAC only together with this transport and decoder.

## Checks before installation

1. Exact ALAC PCM oracle, malformed/truncated packets and recovery after errors.
2. RTP authentication, timestamp/sequence wrap, reordering, duplicates, gaps,
   incorrect peer IP, bounded queues and cancellation without blocked workers.
3. Captured SETUP negotiation, ports, unsupported-format rejection and FLUSH.
4. Full Go tests, race tests, vet, and Linux ARMv7/ARM64 builds with CGO disabled.
5. Inspect runtime dependency closure for CgoFiles. Regenerate attribution in
   embedding applications when they update the module.
6. Install with backup on Midi; compare Yandex Music and Apple Music, pause,
   resume and switching apps. Treat Mini2 compatibility as unverified until
   tested on that hardware; simulator PCM success does not replace a voice or
   physical-output test.

## Protocol references

- [Shairport Sync RTP](https://github.com/mikebrady/shairport-sync/blob/master/rtp.c)
  documents the working realtime datagram, control and retransmit layouts.
- [airplay2-receiver](https://github.com/openairplay/airplay2-receiver/tree/master/ap2/connections)
  provides an independent implementation of type96 setup/control/audio.
- [saprobe-alac](https://github.com/mycophonic/saprobe-alac) provides the pure-Go
  packet decoder; the dependency version and license are pinned in this repo.

## Live result

On 2026-11-01 the user confirmed Yandex Music and Apple Music playback on Midi.
The log shows successful realtime ALAC negotiation and PTP sync, followed by
successful buffered AAC on the same control session. Yandex pause had about
two seconds of audible tail. No pause FLUSH was received during that interval;
button-to-sound latency was not separately measured. Mini2 hardware remains
unverified. Full test/race/vet and CGO-disabled ARMv7/ARM64 builds passed.
