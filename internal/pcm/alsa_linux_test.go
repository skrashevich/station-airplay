//go:build linux && (arm || arm64 || amd64)

package pcm

import (
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
