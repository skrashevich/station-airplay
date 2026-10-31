package handlers

import (
	"context"
	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
	"github.com/skrashevich/station-airplay/rtsp"
	"howett.net/plist"
	"testing"
)

func TestStreamOnlyTeardownKeepsEventSession(t *testing.T) {
	p := audio.NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	old := audio.NewServer(p)
	r := &Rstp{player: p, streams: map[string]*audio.Server{"x": old}}
	body, _ := plist.Marshal(map[string]any{"streams": []any{map[string]any{"type": 103}}}, plist.BinaryFormat)
	r.OnTeardownWeb(&rtsp.Request{Path: "x", Body: body})
	defer func() {
		for _, s := range r.streams {
			s.Close()
		}
	}()
	if r.streams["x"] == nil || r.streams["x"] == old {
		t.Fatal("stream-only teardown removed event session")
	}
}
func TestEventStageVolumeDoesNotChangeActivePlayer(t *testing.T) {
	p := audio.NewPlayer(context.Background(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	p.BindIP = "127.0.0.1"
	owner := audio.NewServer(p)
	defer owner.Close()
	if _, err := owner.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	other := audio.NewServer(p)
	defer other.Close()
	r := &Rstp{player: p, streams: map[string]*audio.Server{"other": other}}
	r.OnSetParameterWeb(&rtsp.Request{Body: []byte("volume: 0\r\n")})
	if p.Volume() != -20 {
		t.Fatal("unclaimed session changed owner's volume")
	}
}

func TestAnchorReadsAppleTimelineClockID(t *testing.T) {
	body, err := plist.Marshal(map[string]any{"rate": uint64(1), "rtpTime": uint64(123), "networkTimeSecs": uint64(7648), "networkTimeFrac": uint64(0), "networkTimeTimelineID": uint64(0x783f4dcda2660009)}, plist.BinaryFormat)
	if err != nil {
		t.Fatal(err)
	}
	var anchor setRateAnchorTime
	if _, err = plist.Unmarshal(body, &anchor); err != nil {
		t.Fatal(err)
	}
	if anchor.ClockIdentity != 0x783f4dcda2660009 {
		t.Fatalf("clock identity lost: %x", anchor.ClockIdentity)
	}
}
