package stationairplay

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
)

func TestLocalVolumeDoesNotChangeReplacementSender(t *testing.T) {
	p := audio.NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	p.BindIP = "127.0.0.1"
	first := audio.NewServer(p)
	if _, err := first.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	first.Close()
	first.Wait()
	second := audio.NewServer(p)
	if _, err := second.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close(); second.Wait() })
	p.SetVolume(-10)
	svc := &protocolService{player: p}
	if err := svc.setOwnerVolume(t.Context(), first, -17); !errors.Is(err, ErrNoSender) {
		t.Fatal(err)
	}
	if p.Volume() != -10 {
		t.Fatalf("changed replacement gain to %v", p.Volume())
	}
}

func TestPublicMediaRejectsStoppedAndInvalidVolume(t *testing.T) {
	r := testReceiver(t)
	if err := r.Control(t.Context(), "pause"); !errors.Is(err, ErrNotRunning) {
		t.Fatal(err)
	}
	for _, db := range []float64{1, -145, math.NaN(), math.Inf(1)} {
		if err := r.SetVolume(t.Context(), db); err == nil {
			t.Fatal("accepted invalid volume")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.Control(ctx, "pause"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
