package ptp

import (
	"net"
	"testing"
	"time"
)

func TestTimingSelectionRejectsForeignPeerAndStaleOwner(t *testing.T) {
	s := new(Server)
	owner, other := new(int), new(int)
	s.Bind(owner, []net.IP{net.ParseIP("192.0.2.1")})
	s.SetPeers(owner, []net.IP{net.ParseIP("192.0.2.1")}, 42)
	if _, ok := s.accept(net.ParseIP("192.0.2.2"), 42); ok {
		t.Fatal("foreign peer accepted")
	}
	if _, ok := s.accept(net.ParseIP("192.0.2.1"), 43); ok {
		t.Fatal("foreign clock accepted")
	}
	if _, ok := s.accept(net.ParseIP("192.0.2.1"), 42); !ok {
		t.Fatal("selected peer rejected")
	}
	s.Bind(other, []net.IP{net.ParseIP("192.0.2.2")})
	s.Clear(owner)
	if _, ok := s.accept(net.ParseIP("192.0.2.2"), 77); !ok {
		t.Fatal("stale owner cleared new timing selection")
	}
}

func TestStaleSelectionCannotAdjustNewOwnersClock(t *testing.T) {
	clock := NewVirtualClock(0)
	s := &Server{clock: clock}
	a, b := new(int), new(int)
	s.Bind(a, []net.IP{net.ParseIP("192.0.2.1")})
	generation, ok := s.accept(net.ParseIP("192.0.2.1"), 42)
	if !ok {
		t.Fatal("selection rejected")
	}
	s.Bind(b, []net.IP{net.ParseIP("192.0.2.2")})
	before := clock.Now()
	if s.applyOffset(generation, time.Hour) {
		t.Fatal("stale PTP packet changed new owner's clock")
	}
	if delta := clock.Now().Sub(before); delta < 0 || delta > time.Second {
		t.Fatalf("clock changed: %v", delta)
	}
}
