# Validation — 2026-10-31

## Builds and automated checks

The full test suite, host race tests, vet, and CGO_ENABLED=0 dependency audit passed.
Static ARM64 and ARMv7 cross-builds were produced. The ARM64 binary ran on both
real Stations. ARMv7 is build-verified; no ARMv7 hardware was available in this test.

Tests cover independent AAC reference PCM, encrypted packet tampering/truncation,
RTSP bounds/concurrency, transient pairing and encryption transition, output
process cleanup, volume ownership, future/blocked flush admission, fresh decoder
state after resume, PTP selection/generation, timeline ID parsing, profile TLVs,
identity/storage persistence, and installer ownership/update/uninstall.

## Hardware and user verification

- Midi: 192.168.88.66, firmware 0.369.1.46.4778262955.20261013.160.
- Station 2: 192.168.88.24, firmware 0.369.1.46.4793639783.20261020.295.
- Same static ARM64 executable and `aplay -D default` path on both; no model branches.
- Synthetic SRP/encrypted RTSP/AAC sender completed on both.
- User's iPhone discovered both receivers. User confirmed sound on Midi after the
  extended PTP FollowUp fix, and later confirmed Station 2 playback.
- Adding Station 2 to the group exposed further timing/lifecycle issues. The final
  group retest initially failed because abrupt child termination stranded dmix IPC.
  After lifecycle correction and verified one-time orphan cleanup, the user
  confirmed both Stations play concurrently from the same iPhone. Both hardware
  playback PCMs were subsequently observed RUNNING; Masters stayed unchanged.
- Live Midi playback used stereo 44100-Hz PCM; ALSA converted it to 4 channels at
  96000 Hz. Master remained 168,168; receiver RSS was about 12 MiB in one snapshot.

## Defects reproduced and corrected during live testing

1. iPhone sends a 96-byte PTP FollowUp with a profile TLV. The dependency's timestamp
   decoder requires the fixed ten-byte timestamp payload. A regression test now
   verifies the standard header/body is parsed without interpreting trailing TLVs.
2. Apple's anchor carries `networkTimeTimelineID`. The reference's `networkTimeId`
   field ignored it. The real key now controls timing selection.
3. Immediate SIGKILL of aplay stranded Midi dmix shared memory belonging to a
   previously started receiver child. Its old creator PID was verified; playback
   was closed and attachment counts were zero before the two orphaned segments
   and their semaphore were removed once. The same check/recovery was performed
   for receiver-owned orphaned resources on Station 2. This is not receiver startup behavior
   and no automatic/global IPC deletion is implemented. Output cancellation now
   sends SIGTERM and allows cleanup before the bounded kill fallback. A real child
   signal-handler test proves cleanup is reached. A subsequent finite Midi probe
   exited with no remaining dmix shared-memory segments.

Installation is contained under /data/opt/station-airplay. Boot hooks have not
been enabled on either device; no vendor services or firmware settings were changed.

## Limits

No sample-accurate multiroom latency/drift measurement, extended-duration soak,
other Station models, alternate iPhone applications/transports, or ARMv7 runtime
validation has been completed. Synthetic sender tests use a deliberate past-time
anchor and do not by themselves validate live clock synchronization.
