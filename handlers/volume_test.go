package handlers

import (
	"bytes"
	"context"
	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
	"github.com/skrashevich/station-airplay/rtsp"
	"howett.net/plist"
	"math"
	"testing"
)

func TestEventVolumeUsesAirPlaySliderScale(t *testing.T) {
	for _, tc := range []struct{ db, unit float64 }{{-144, 0}, {-30, 0}, {-20, 1.0 / 3}, {-15, 0.5}, {0, 1}} {
		if got := eventUnitVolume(tc.db); math.Abs(got-tc.unit) > 1e-10 {
			t.Fatalf("%v dB: got %v, want %v", tc.db, got, tc.unit)
		}
	}
}

func TestLocalVolumeSurvivesPartialTeardownAndStreamSetup(t *testing.T) {
	p := audio.NewPlayer(context.Background(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	p.BindIP = "127.0.0.1"
	s := audio.NewServer(p)
	if _, err := s.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	r := &Rstp{player: p, streams: map[string]*audio.Server{"music": s}, volume: -20}
	t.Cleanup(func() {
		for _, s := range r.streams {
			s.Close()
			s.Wait()
		}
	})
	p.SetVolume(-10)
	response, err := r.OnGetParameterWeb(&rtsp.Request{Path: "music"})
	if err != nil || !bytes.Contains(response.Body, []byte("volume: -10.000000")) {
		t.Fatalf("stale volume: %s, %v", response.Body, err)
	}
	body, _ := plist.Marshal(map[string]any{"streams": []any{}}, plist.BinaryFormat)
	if _, err := r.OnTeardownWeb(&rtsp.Request{Path: "music", Body: body}); err != nil {
		t.Fatal(err)
	}
	body, _ = plist.Marshal(setupRtsp{Streams: []setupStreamRequest{{Type: 103, SharedKey: make([]byte, 32), CompressionType: 4, AudioFormat: 1 << 22}}}, plist.BinaryFormat)
	if _, err := r.OnSetupWeb(&rtsp.Request{Path: "music", Body: body}); err != nil {
		t.Fatal(err)
	}
	if p.Volume() != -10 {
		t.Fatalf("stream setup reset local volume to %v", p.Volume())
	}
}
