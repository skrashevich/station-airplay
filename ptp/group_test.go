package ptp

import (
	"encoding/binary"
	"encoding/hex"
	"net"
	"testing"
	"time"

	wire "github.com/albanseurat/go-ptp"
)

// A relaying sender retains its sourcePortIdentity when the group elects a
// different grandmaster. These packets reproduce that transition without TLVs.
func TestGroupGrandmasterTransitionPreservesPreviousTimeline(t *testing.T) {
	clock := NewVirtualClock(0)
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	s := &Server{clock: clock, identity: 99, measurements: newMeasurements(), eventConn: conn}
	peer := net.IPv4(127, 0, 0, 1)
	owner := new(int)
	s.Bind(owner, []net.IP{peer})
	s.SetPeers(owner, []net.IP{peer}, 0x783f4dcda2660008)
	const phone = uint64(0x783f4dcda2660008)
	const homepod = uint64(0x58d3492eabbf0008)
	var seq uint16
	announceAge := 500 * time.Millisecond
	send := func(kind byte, stamp time.Time, correction time.Duration, gm uint64) {
		t.Helper()
		n := 44
		if kind == 11 {
			n = 64
		}
		if kind == 9 {
			n = 54
		}
		b := make([]byte, n)
		b[0] = 0x10 | kind
		b[1] = 2
		binary.BigEndian.PutUint16(b[2:], uint16(n))
		binary.BigEndian.PutUint64(b[8:], uint64(int64(correction)<<16))
		binary.BigEndian.PutUint64(b[20:], phone)
		binary.BigEndian.PutUint16(b[28:], 1)
		binary.BigEndian.PutUint16(b[30:], seq)
		if kind == 11 {
			binary.BigEndian.PutUint64(b[53:], gm)
			b[48] = 248
			b[49] = 0x21
			b[63] = 0xa0
		} else {
			binary.BigEndian.PutUint16(b[34:], uint16(uint64(stamp.Unix())>>32))
			binary.BigEndian.PutUint32(b[36:], uint32(stamp.Unix()))
			binary.BigEndian.PutUint32(b[40:], uint32(stamp.Nanosecond()))
		}
		if kind == 9 {
			binary.BigEndian.PutUint64(b[44:], 99)
			binary.BigEndian.PutUint16(b[52:], 1)
		}
		msg, err := decodePacket(b)
		if err != nil {
			t.Fatal(err)
		}
		received := time.Now()
		if kind == 11 {
			received = received.Add(-announceAge)
		}
		s.processPacket(serverMsg{LocalTimeStamp: received, Msg: msg, Src: &net.UDPAddr{IP: peer, Port: 320}, ClockIdentity: phone})
	}
	exchange := func(epoch int64, syncCorrection, followCorrection, reverseCorrection time.Duration) {
		t.Helper()
		seq++
		masterNow := func() time.Time { return time.Unix(epoch, 0).Add(time.Since(clock.base)) }
		send(0, time.Time{}, syncCorrection, 0)
		send(8, masterNow().Add(-syncCorrection-followCorrection), followCorrection, 0)
		send(9, masterNow().Add(-reverseCorrection), reverseCorrection, 0)
	}
	check := func(id uint64, epoch int64) {
		t.Helper()
		now, known := clock.NowForTimeline(id)
		if !known {
			t.Fatalf("timeline %016x unavailable", id)
		}
		want := time.Unix(epoch, 0).Add(time.Since(clock.base))
		if d := now.Sub(want); d < -30*time.Millisecond || d > 30*time.Millisecond {
			t.Fatalf("timeline %016x shifted by %v", id, d)
		}
	}
	send(11, time.Time{}, 0, phone)
	exchange(181620, 0, 0, 0)
	check(phone, 181620)
	oldAnchor, _ := clock.NowForTimeline(phone)
	oldAnchor = oldAnchor.Add(time.Second)
	// Event and general ports are independent: new-epoch timing can arrive
	// before its Announce. It must not shift the established phone timeline.
	exchange(54630, 0, time.Second, 0)
	check(phone, 181620)
	// An incomplete sample from the previous GM must not complete after Announce.
	seq++
	send(0, time.Time{}, 0, 0)
	announceAge = 0
	send(11, time.Time{}, 0, homepod)
	// Even two queued old-epoch exchanges can agree immediately after Announce.
	exchange(181620, 0, 0, 0)
	exchange(181620, 0, 0, 0)
	if _, known := clock.NowForTimeline(homepod); known {
		t.Fatal("new GM published within settling interval")
	}
	// Advance only the metadata receive timestamp to avoid sleeping in the test.
	s.grandmasterChangedAt = s.grandmasterChangedAt.Add(-500 * time.Millisecond)
	announceAge = 500 * time.Millisecond
	send(8, time.Unix(54630, 0), 0, 0)
	send(9, time.Unix(54630, 0), 0, 0)
	if _, known := clock.NowForTimeline(homepod); known {
		t.Fatal("old GM Sync completed an exchange in the new GM domain")
	}
	// Conversely, an old-epoch exchange may be delayed behind the Announce.
	exchange(181620, 0, 0, 0)
	if _, known := clock.NowForTimeline(homepod); known {
		t.Fatal("published old epoch as new GM")
	}
	exchange(54630, 150*time.Millisecond, 3*time.Second, -25*time.Millisecond)
	if _, known := clock.NowForTimeline(homepod); known {
		t.Fatal("unconfirmed GM epoch published")
	}
	exchange(54630, 150*time.Millisecond, 3*time.Second, -25*time.Millisecond)
	check(phone, 181620)
	check(homepod, 54630)
	oldNow, _ := clock.NowForTimeline(phone)
	if wait := oldAnchor.Sub(oldNow); wait < 900*time.Millisecond || wait > 1100*time.Millisecond {
		t.Fatalf("old anchor deadline changed to %v", wait)
	}
	// Relay residence time can vary, including a signed correction.
	exchange(54630, -10*time.Millisecond, 100*time.Millisecond, 12*time.Millisecond)
	check(phone, 181620)
	check(homepod, 54630)
	s.SetPeers(owner, []net.IP{peer}, homepod)
	exchange(54630, 0, 2*time.Second, 0)
	check(homepod, 54630)
	send(11, time.Time{}, 0, phone)
	exchange(181620, 0, 250*time.Millisecond, 0)
	exchange(181620, 0, 250*time.Millisecond, 0)
	check(phone, 181620)
	check(homepod, 54630)
}

func TestIdenticalTimingPeersPreservePendingExchange(t *testing.T) {
	s := &Server{clock: NewVirtualClock(0)}
	owner := new(int)
	peers := []net.IP{net.IPv4(192, 0, 2, 1)}
	s.Bind(owner, peers)
	s.SetPeers(owner, peers, 42)
	gen, ok := s.accept(peers[0], 42)
	if !ok {
		t.Fatal("selected source rejected")
	}
	s.SetPeers(owner, peers, 42)
	if !s.applyOffset(gen, time.Second) {
		t.Fatal("identical peer update invalidated the in-flight exchange")
	}
}

func TestSignedPTPCorrectionDecoding(t *testing.T) {
	for _, want := range []time.Duration{-3 * time.Second, 150 * time.Millisecond, 3 * time.Second} {
		b := make([]byte, 44)
		b[0] = 8
		b[1] = 2
		binary.BigEndian.PutUint16(b[2:], 44)
		binary.BigEndian.PutUint64(b[8:], uint64(int64(want)<<16))
		binary.BigEndian.PutUint32(b[36:], 181620)
		msg, err := decodePacket(b)
		if err != nil {
			t.Fatal(err)
		}
		f := msg.(*wire.FollowUpMsg)
		// The wire library exposes only the upper 48 bits as an unsigned integer.
		if got := correction(f.Header); got != want {
			t.Fatalf("correction=%v want %v", got, want)
		}
	}
}

func TestPacketSelectionPinsRelaySourceAndAddress(t *testing.T) {
	s := &Server{clock: NewVirtualClock(0)}
	owner := new(int)
	a, b := net.IPv4(192, 0, 2, 1), net.IPv4(192, 0, 2, 2)
	s.Bind(owner, []net.IP{a, b})
	s.SetPeers(owner, []net.IP{a, b}, 42)
	packet := serverMsg{Src: &net.UDPAddr{IP: a}, ClockIdentity: 17, Msg: &wire.AnnounceMsg{Header: wire.Header{PortNumber: 1}, GMIdentity: 42}}
	if !s.acceptPacket(packet) {
		t.Fatal("selected GM's relay rejected")
	}
	packet.ClockIdentity = 18
	if s.acceptPacket(packet) {
		t.Fatal("second source can mix sequence-number measurements")
	}
	packet.ClockIdentity = 17
	packet.Src = &net.UDPAddr{IP: b}
	if s.acceptPacket(packet) {
		t.Fatal("same source ID at another address accepted")
	}
	packet.Src = &net.UDPAddr{IP: a}
	packet.Msg = &wire.SyncMsg{Header: wire.Header{PortNumber: 2}}
	if s.acceptPacket(packet) {
		t.Fatal("different source PTP port accepted")
	}
}

func TestUnknownTimelineWaitsAndNewOwnerDiscardsMappings(t *testing.T) {
	clock := NewVirtualClock(0)
	if _, ok := clock.NowForTimeline(42); ok {
		t.Fatal("unknown timeline treated as raw local clock")
	}
	if _, ok := clock.NowForTimeline(0); !ok {
		t.Fatal("legacy raw timeline unavailable")
	}
	clock.OffsetForTimeline(42, -181620*time.Second)
	if _, ok := clock.NowForTimeline(42); !ok {
		t.Fatal("converged timeline unavailable")
	}
	s := &Server{clock: clock}
	s.Bind(new(int), []net.IP{net.IPv4(192, 0, 2, 1)})
	if _, ok := clock.NowForTimeline(42); ok {
		t.Fatal("new owner inherited previous owner's timeline")
	}
}

// Captured iPhone relay sequence195 repeats the GM origin timestamp in both
// messages; its increasing correction advances that timestamp to each event.
func TestAppleForwardedDelayResponseAdvancesOriginTimestamp(t *testing.T) {
	clock := NewVirtualClock(0)
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const source = uint64(0x783f4dcda2660008)
	const identity = uint64(0x42b782fffe7a3e20)
	s := &Server{clock: clock, identity: identity, eventConn: conn, measurements: newMeasurements()}
	peer := net.IPv4(127, 0, 0, 1)
	owner := new(int)
	s.Bind(owner, []net.IP{peer})
	s.SetPeers(owner, []net.IP{peer}, source)
	// The corrected FU timestamp is 55472.281276607s; put our receive
	// clock 1ms later, representing a small positive network transit time.
	clock.Offset(-time.Duration(55472282276607))
	before := clock.Now()
	send := func(kind byte, correction uint64, payload string) {
		t.Helper()
		body, err := hex.DecodeString(payload)
		if err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 34+len(body))
		b[0] = 0x10 | kind
		b[1] = 2
		binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
		binary.BigEndian.PutUint64(b[8:], correction)
		binary.BigEndian.PutUint64(b[20:], source)
		binary.BigEndian.PutUint16(b[28:], 1)
		binary.BigEndian.PutUint16(b[30:], 195)
		copy(b[34:], body)
		msg, err := decodePacket(b)
		if err != nil {
			t.Fatal(err)
		}
		s.processPacket(serverMsg{LocalTimeStamp: time.Now(), Msg: msg, Src: &net.UDPAddr{IP: peer, Port: 320}, ClockIdentity: source})
	}
	send(0, 0, "00000000000000000000")
	send(8, 68001613404727, "00000000d8af0e85debd")
	send(9, 68372923290113, "00000000d8af0e85debd42b782fffe7a3e200001")
	if change := clock.Now().Sub(before); change < -50*time.Millisecond || change > 50*time.Millisecond {
		t.Fatalf("forwarded DelayResp moved clock by %v; repeated origin correction must advance the timestamp", change)
	}
}
