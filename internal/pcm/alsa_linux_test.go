//go:build linux && (arm || arm64 || amd64)

package pcm

import (
	"slices"
	"strings"
	"syscall"
	"testing"
)

func TestCheckHonorsLibraryOverrideWithoutFallback(t *testing.T) {
	t.Setenv("STATION_PCM_LIBRARY", "/missing-station-pcm-test/libasound.so.2")
	err := Check()
	if err == nil || !strings.Contains(err.Error(), "/missing-station-pcm-test/libasound.so.2") {
		t.Fatalf("override was ignored: %v", err)
	}
}

func TestCancelPendingDrainCompletesMixerBookkeepingBeforeDrop(t *testing.T) {
	var calls []string
	updates := 0
	d := &alsaDevice{draining: true, api: &alsa{
		statePCM: func(uintptr) int32 {
			if updates >= 2 {
				return 1
			}
			return 5
		},
		resetPCM: func(uintptr) int32 { calls = append(calls, "reset"); return 0 },
		availPCM: func(uintptr) int { updates++; calls = append(calls, "sync"); return 4096 },
		dropPCM:  func(uintptr) int32 { calls = append(calls, "drop"); return 0 },
	}}
	d.drop()
	if !slices.Equal(calls, []string{"reset", "sync", "sync", "drop"}) {
		t.Fatalf("pending drain cleanup order: %v", calls)
	}
}

func TestStopRunningPlaybackDropsWithoutReset(t *testing.T) {
	dropped := false
	d := &alsaDevice{api: &alsa{
		resetPCM: func(uintptr) int32 { t.Fatal("Stop reset running PCM"); return 0 },
		dropPCM:  func(uintptr) int32 { dropped = true; return 0 },
	}}
	d.drop()
	if !dropped {
		t.Fatal("Stop did not drop buffered audio")
	}
}

func TestPlayRejectsRateBeforeLoadingLibrary(t *testing.T) {
	t.Setenv("STATION_PCM_LIBRARY", "/missing-station-pcm-test/libasound.so.2")
	err := Play(t.Context(), strings.NewReader(""), Config{Rate: 22050})
	if err == nil || !strings.Contains(err.Error(), "PCM rate") {
		t.Fatalf("rate was not validated: %v", err)
	}
}

func TestSuspendedPCMDoesNotEnterUnboundedNativeRecovery(t *testing.T) {
	d := &alsaDevice{api: &alsa{recoverPCM: func(uintptr, int32, int32) int32 { panic("suspended PCM entered native recovery") }}}
	if code := d.recover(-int(syscall.ESTRPIPE)); code != -int(syscall.ESTRPIPE) {
		t.Fatalf("lost suspended PCM error: %d", code)
	}
}

func TestDrainFlushesRateConverterOnceAndUpdatesMixer(t *testing.T) {
	calls, updates := 0, 0
	d := &alsaDevice{api: &alsa{
		drainPCM: func(uintptr) int32 { calls++; return -int32(syscall.EAGAIN) },
		availPCM: func(uintptr) int { updates++; return 4096 },
		statePCM: func(uintptr) int32 {
			if updates >= 2 {
				return 1
			} // SETUP after dmix pointer synchronization.
			return 5 // DRAINING
		},
	}}
	for range 3 {
		if d.drain() == 0 {
			break
		}
	}
	if calls != 1 || updates != 2 {
		t.Fatalf("drain restarted rate conversion or failed to advance mixer: calls=%d updates=%d", calls, updates)
	}
}

func TestInterruptedDrainPreservesStartedTransition(t *testing.T) {
	calls, state := 0, int32(5)
	d := &alsaDevice{api: &alsa{
		drainPCM: func(uintptr) int32 { calls++; return -int32(syscall.EINTR) },
		statePCM: func(uintptr) int32 { return state },
		availPCM: func(uintptr) int { state = 1; return 4096 },
	}}
	if code := d.drain(); code != 0 || !d.draining || calls != 1 {
		t.Fatalf("interrupted pending drain lost: code=%d pending=%v calls=%d", code, d.draining, calls)
	}
}
