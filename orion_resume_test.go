package stationairplay

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
)

func TestResumeAfterPhoneTearsDownPausedAudio(t *testing.T) {
	p := audio.NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	p.BindIP = "127.0.0.1"
	owner := audio.NewServer(p)
	available := true
	var actions []string
	owner.RemoteAvailable = func() bool { return available }
	owner.RemoteControl = func(_ context.Context, a string) error { actions = append(actions, a); return nil }
	if _, err := owner.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	control, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	svc := &protocolService{player: p, control: control}
	if err := svc.Control(t.Context(), "pause"); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	owner.Wait()
	if p.Current() != nil {
		t.Fatal("audio owner retained")
	}
	if status := svc.Status(); !status.Connected || status.Playing {
		t.Fatalf("paused sender status: %+v", status)
	}
	if err := svc.Control(t.Context(), "resume"); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 || actions[1] != "resume" {
		t.Fatalf("actions=%v", actions)
	}
	available = false
	if status := svc.Status(); status.Connected {
		t.Fatalf("closed event channel status: %+v", status)
	}
	if err := svc.Control(t.Context(), "resume"); !errors.Is(err, ErrNoSender) {
		t.Fatalf("closed event channel: %v", err)
	}
}

func TestNewSenderInvalidatesPausedRemote(t *testing.T) {
	svc, _, _ := retainedSenderFixture(t)
	replacement := audio.NewServer(svc.player)
	if _, err := replacement.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	defer func() { replacement.Close(); replacement.Wait() }()
	if svc.pausedSender() != nil {
		t.Fatal("old sender survived replacement")
	}
}
