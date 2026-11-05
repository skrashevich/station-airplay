package stationairplay

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"

	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
)

func retainedSenderFixture(t *testing.T) (*protocolService, *audio.Server, *[]string) {
	t.Helper()
	p := audio.NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	p.BindIP = "127.0.0.1"
	owner := audio.NewServer(p)
	actions := new([]string)
	owner.RemoteAvailable = func() bool { return true }
	// Only the phone's external command transport is replaced. Audio ownership,
	// stream setup/teardown and protocolService routing use their real code.
	owner.RemoteControl = func(_ context.Context, action string) error {
		*actions = append(*actions, action)
		return nil
	}
	if _, err := owner.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Close(); owner.Wait() })
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	svc := &protocolService{player: p, control: l}
	if err := svc.Control(t.Context(), "pause"); err != nil {
		t.Fatal(err)
	}
	owner.Close()
	owner.Wait()
	return svc, owner, actions
}

func TestRetainedSenderInvalidatedByUnobservedReplacement(t *testing.T) {
	svc, _, actions := retainedSenderFixture(t)
	replacement := audio.NewServer(svc.player)
	if _, err := replacement.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	replacement.Close()
	replacement.Wait()
	// A library client need not poll while the replacement owns audio.
	if svc.Status().Connected {
		t.Error("previous sender still connected after a replacement claimed audio")
	}
	if err := svc.Control(t.Context(), "resume"); !errors.Is(err, ErrNoSender) {
		t.Errorf("resume after replacement: %v", err)
	}
	if !reflect.DeepEqual(*actions, []string{"pause"}) {
		t.Fatalf("commands sent to obsolete sender: %v", *actions)
	}
}

func TestRetainedSenderStopsAfterResumeBeforeAudioSetup(t *testing.T) {
	svc, _, actions := retainedSenderFixture(t)
	if err := svc.Control(t.Context(), "resume"); err != nil {
		t.Fatal(err)
	}
	// Alice maps the spoken stop command to pause so it can later be resumed.
	if err := svc.Control(t.Context(), "pause"); err != nil {
		t.Fatalf("stop before new audio setup: %v", err)
	}
	if !reflect.DeepEqual(*actions, []string{"pause", "resume", "pause"}) {
		t.Fatalf("phone commands: %v", *actions)
	}
}

func TestRetainedSenderIgnoresLatePauseFromPreviousOwner(t *testing.T) {
	svc, _, _ := retainedSenderFixture(t)
	first := audio.NewServer(svc.player)
	first.RemoteAvailable = func() bool { return true }
	started, complete := make(chan struct{}), make(chan struct{})
	first.RemoteControl = func(context.Context, string) error {
		close(started)
		select {
		case <-complete:
			// A completed remote ACK may be observed after the audio owner changed.
			return nil
		case <-t.Context().Done():
			return t.Context().Err()
		}
	}
	if _, err := first.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close(); first.Wait() })
	paused := make(chan error, 1)
	go func() { paused <- svc.Control(t.Context(), "pause") }()
	<-started
	first.Close()
	first.Wait()
	replacement := audio.NewServer(svc.player)
	replacement.RemoteAvailable = func() bool { return true }
	var actions []string
	replacement.RemoteControl = func(_ context.Context, action string) error {
		actions = append(actions, action)
		return nil
	}
	if _, err := replacement.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { replacement.Close(); replacement.Wait() })
	if err := svc.Control(t.Context(), "pause"); err != nil {
		t.Fatal(err)
	}
	replacement.Close()
	replacement.Wait()
	close(complete)
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	if err := svc.Control(t.Context(), "resume"); err != nil {
		t.Fatalf("late pause displaced the replacement's control: %v", err)
	}
	if !reflect.DeepEqual(actions, []string{"pause", "resume"}) {
		t.Fatalf("replacement commands: %v", actions)
	}
}
