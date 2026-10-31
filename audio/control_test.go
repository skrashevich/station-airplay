package audio

import (
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
	"testing"
	"time"
)

func TestOnlyClaimedOwnerMayChangeVolume(t *testing.T) {
	p := NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	owner := NewServer(p)
	other := NewServer(p)
	defer owner.Close()
	defer other.Close()
	if err := p.claim(owner); err != nil {
		t.Fatal(err)
	}
	if p.SetVolumeFor(other, 0) {
		t.Fatal("nonowner changed volume")
	}
	if p.Volume() != -20 {
		t.Fatal("volume changed")
	}
	if !p.SetVolumeFor(owner, -30) || p.Volume() != -30 {
		t.Fatal("owner volume failed")
	}
}
func TestFlushRejectsFutureAndBlockedQueueAdmission(t *testing.T) {
	p := NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	s := NewServer(p)
	defer s.Close()
	for i := range 128 {
		s.buffer.Push(t.Context(), &PCMFrame{TcpPacket: TcpPacket{SequenceNumber: uint32(i)}})
	}
	done := make(chan error, 1)
	go func() { done <- s.enqueue(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 1500}}) }()
	s.Flush(1000, 2000)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("flushed producer remains blocked")
	}
	if s.buffer.Len() != 128 {
		t.Fatal("flushed frame admitted")
	}
}
