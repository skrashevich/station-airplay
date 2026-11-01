package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
	"golang.org/x/crypto/chacha20poly1305"
)

func realtimePacket(t *testing.T, seq uint16, stamp uint32, payload []byte) []byte {
	t.Helper()
	raw := make([]byte, 12)
	raw[0], raw[1] = 0x80, 0xe0
	binary.BigEndian.PutUint16(raw[2:], seq)
	binary.BigEndian.PutUint32(raw[4:], stamp)
	binary.BigEndian.PutUint32(raw[8:], 0x01020304)
	cipher, err := chacha20poly1305.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	nonce := [12]byte{4: 1, 11: 9}
	raw = cipher.Seal(raw, nonce[:], payload, raw[4:12])
	return append(raw, nonce[4:]...)
}
func TestRealtimeRTPAuthenticationAndSequence(t *testing.T) {
	raw := realtimePacket(t, 65535, 0xfffffff0, []byte{1, 2, 3, 4})
	got, err := ParseRealtimeFrame(make([]byte, 32), raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.SequenceNumber != 65535 || got.Timestamp != 0xfffffff0 || !bytes.Equal(got.encodedData, []byte{1, 2, 3, 4}) {
		t.Fatalf("frame=%+v", got)
	}
	for _, offset := range []int{4, 8, 12, len(raw) - 9, len(raw) - 1} {
		bad := bytes.Clone(raw)
		bad[offset] ^= 1
		if _, err := ParseRealtimeFrame(make([]byte, 32), bad); err == nil {
			t.Fatalf("accepted tampering at %d", offset)
		}
	}
	for n := range 36 {
		if _, err := ParseRealtimeFrame(make([]byte, 32), raw[:n]); err == nil {
			t.Fatalf("accepted %d bytes", n)
		}
	}
	bad := bytes.Clone(raw)
	bad[0] = 0x90
	if _, err := ParseRealtimeFrame(make([]byte, 32), bad); err == nil {
		t.Fatal("accepted extension header")
	}
}
func syncPacket(rtp uint32, ns uint64, clock uint64) []byte {
	raw := make([]byte, 28)
	raw[0], raw[1] = 0x80, 0xd7
	binary.BigEndian.PutUint32(raw[4:], rtp)
	binary.BigEndian.PutUint64(raw[8:], ns)
	binary.BigEndian.PutUint32(raw[16:], rtp+77175)
	binary.BigEndian.PutUint64(raw[20:], clock)
	return raw
}
func TestRealtimeSyncUsesNanosecondPTPAnchor(t *testing.T) {
	rtp, network, clock, err := parseRealtimeSync(syncPacket(22050, 1234567890, 0x12345678))
	if err != nil {
		t.Fatal(err)
	}
	if rtp != 11025 || !network.Equal(time.Unix(1, 234567890)) || clock != 0x12345678 {
		t.Fatalf("rtp=%d network=%s clock=%x", rtp, network, clock)
	}
	for _, raw := range [][]byte{nil, make([]byte, 27), syncPacket(22050, ^uint64(0), 1)} {
		if _, _, _, err := parseRealtimeSync(raw); err == nil {
			t.Fatal("invalid sync accepted")
		}
	}
}
func TestRealtimeJitterBufferOrdersWrapAndDropsDuplicates(t *testing.T) {
	b := NewBuffer(3)
	for _, f := range []*PCMFrame{
		{TcpPacket: TcpPacket{SequenceNumber: 0, Timestamp: 16}},
		{TcpPacket: TcpPacket{SequenceNumber: 65535, Timestamp: 0xfffffeb0}},
		{TcpPacket: TcpPacket{SequenceNumber: 1, Timestamp: 368}},
	} {
		if !b.tryPushOrdered(f) {
			t.Fatal("push failed")
		}
	}
	if b.tryPushOrdered(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 0, Timestamp: 16}}) {
		t.Fatal("duplicate accepted")
	}
	if b.tryPushOrdered(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 2, Timestamp: 720}}) {
		t.Fatal("full buffer accepted newer frame")
	}
	for _, stamp := range []uint32{0xfffffeb0, 16, 368} {
		if got := b.Pop(); got == nil || got.Timestamp != stamp {
			t.Fatalf("want timestamp=%d got=%+v", stamp, got)
		}
	}
}
func TestRealtimeCancellationJoinsBothSockets(t *testing.T) {
	p := NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	p.BindIP = "127.0.0.1"
	s := NewServer(p)
	s.PeerIP = net.ParseIP("127.0.0.1")
	data, control, err := s.SetupRealtime(make([]byte, 32), 44100)
	if err != nil {
		t.Fatal(err)
	}
	if data == 0 || control == 0 || data == control {
		t.Fatalf("ports %d/%d", data, control)
	}
	s.Close()
	joined := make(chan struct{})
	go func() { s.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("UDP stream did not join")
	}
	if p.Current() != nil {
		t.Fatal("ownership retained")
	}
}

func TestRealtimeAudioOutputHelper(t *testing.T) {
	if os.Getenv("STATION_REALTIME_HELPER") == "" {
		return
	}
	n, _ := strconv.Atoi(os.Getenv("STATION_REALTIME_BYTES"))
	b := make([]byte, n)
	if _, err := io.ReadFull(os.Stdin, b); err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("STATION_REALTIME_CAPTURE"), b, 0600); err != nil {
		os.Exit(3)
	}
	io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func waitRealtimeCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-deadline.C:
			t.Fatal("realtime condition timed out")
		case <-tick.C:
		}
	}
}

func TestRealtimeUDPDecodesReorderedALACAndPeriodicSync(t *testing.T) {
	payload, err := os.ReadFile("../codec/testdata/alac-stereo16-44100.packet")
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("../codec/testdata/alac-stereo16-44100.pcm")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STATION_REALTIME_HELPER", "1")
	t.Setenv("STATION_REALTIME_BYTES", strconv.Itoa(len(expected)*3))
	capture := filepath.Join(t.TempDir(), "pcm")
	t.Setenv("STATION_REALTIME_CAPTURE", capture)
	exe, _ := os.Executable()
	clock := ptp.NewVirtualClock(0)
	p := NewPlayer(t.Context(), clock, codec.OutputOptions{Command: exe, Args: []string{"-test.run=TestRealtimeAudioOutputHelper", "--"}})
	p.BindIP = "127.0.0.1"
	p.SetVolume(0)
	s := NewServer(p)
	s.PeerIP = net.ParseIP("127.0.0.1")
	defer func() { s.Close(); s.Wait() }()
	dataPort, controlPort, err := s.SetupRealtime(make([]byte, 32), 44100)
	if err != nil {
		t.Fatal(err)
	}
	data, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: s.PeerIP, Port: dataPort})
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	control, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: s.PeerIP, Port: controlPort})
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	first := uint32(0xfffffeb0)
	packets := [][]byte{realtimePacket(t, 0, 16, payload), realtimePacket(t, 65535, first, payload), realtimePacket(t, 1, 368, payload)}
	tampered := bytes.Clone(packets[0])
	tampered[12] ^= 1
	data.Write(tampered)
	for _, packet := range packets {
		if _, err := data.Write(packet); err != nil {
			t.Fatal(err)
		}
	}
	data.Write(packets[0])
	waitRealtimeCondition(t, func() bool { return s.buffer.Len() == 3 })
	stamp := clock.Now().Add(30 * time.Millisecond)
	packet := syncPacket(first+realtimeAnchorOffset, uint64(stamp.UnixNano()), 0x1234)
	if _, err := control.Write(packet); err != nil {
		t.Fatal(err)
	}
	waitRealtimeCondition(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.playing })
	s.mu.Lock()
	generation := s.generation
	s.mu.Unlock()
	// A valid periodic sync must not change the output generation.
	if _, err := control.Write(packet); err != nil {
		t.Fatal(err)
	}
	waitRealtimeCondition(t, func() bool { b, err := os.ReadFile(capture); return err == nil && len(b) == len(expected)*3 })
	got, _ := os.ReadFile(capture)
	want := bytes.Repeat(expected, 3)
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded PCM mismatch: got=%d want=%d", len(got), len(want))
	}
	s.mu.Lock()
	after := s.generation
	s.mu.Unlock()
	if after != generation {
		t.Fatalf("periodic sync reset output generation: %d -> %d", generation, after)
	}
	// Already played packets cannot be queued a second time.
	data.Write(packets[0])
	time.Sleep(10 * time.Millisecond)
	if s.buffer.Len() != 0 {
		t.Fatal("late duplicate entered playout queue")
	}
}

func TestRealtimeUDPRejectsWrongPeerWithoutClosing(t *testing.T) {
	p := NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	p.BindIP = "127.0.0.1"
	s := NewServer(p)
	s.PeerIP = net.ParseIP("192.0.2.1")
	defer func() { s.Close(); s.Wait() }()
	port, controlPort, err := s.SetupRealtime(make([]byte, 32), 44100)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		port    int
		payload []byte
	}{{port, realtimePacket(t, 1, 0, []byte{1, 2})}, {controlPort, syncPacket(22050, 1234567890, 1)}} {
		c, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: entry.port})
		if err != nil {
			t.Fatal(err)
		}
		c.Write(entry.payload)
		c.Close()
	}
	time.Sleep(20 * time.Millisecond)
	s.mu.Lock()
	playing := s.playing
	s.mu.Unlock()
	if s.buffer.Len() != 0 || playing || s.Context().Err() != nil {
		t.Fatal("wrong peer influenced realtime stream")
	}
}

func TestRealtimeFlushRejectsOldTimeline(t *testing.T) {
	p := NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	s := NewServer(p)
	defer s.Close()
	s.realtime = &realtimeState{}
	s.buffer = NewBuffer(3)
	s.enqueueRealtime(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 1, Timestamp: 100}})
	s.FlushRealtime(452)
	if s.enqueueRealtime(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 1, Timestamp: 100}}) {
		t.Fatal("old packet accepted after flush")
	}
	if !s.enqueueRealtime(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 2, Timestamp: 452}}) {
		t.Fatal("new timeline packet rejected")
	}
	s.mu.Lock()
	playing, anchor := s.playing, s.haveAnchor
	s.mu.Unlock()
	if playing || anchor {
		t.Fatal("flush preserved obsolete time mapping")
	}
	s.realtime = nil // No sockets/workers were created in this state-only test.
}

func TestRealtimeParentCancellationReleasesOwnership(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	p := NewPlayer(ctx, ptp.NewVirtualClock(0), codec.OutputOptions{})
	p.BindIP = "127.0.0.1"
	s := NewServer(p)
	s.PeerIP = net.ParseIP("127.0.0.1")
	if _, _, err := s.SetupRealtime(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	cancel()
	joined := make(chan struct{})
	go func() { s.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not close UDP stream")
	}
	if p.Current() != nil {
		t.Fatal("parent cancellation retained ownership")
	}
}

func TestRealtimeRequestsAndAcceptsRetransmission(t *testing.T) {
	p := NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	p.BindIP = "127.0.0.1"
	s := NewServer(p)
	s.PeerIP = net.ParseIP("127.0.0.1")
	defer func() { s.Close(); s.Wait() }()
	dataPort, controlPort, err := s.SetupRealtime(make([]byte, 32), 44100)
	if err != nil {
		t.Fatal(err)
	}
	senderControl, err := net.ListenUDP("udp4", &net.UDPAddr{IP: s.PeerIP})
	if err != nil {
		t.Fatal(err)
	}
	defer senderControl.Close()
	s.SetRealtimeControlPort(uint16(senderControl.LocalAddr().(*net.UDPAddr).Port))
	data, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: s.PeerIP, Port: dataPort})
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	data.Write(realtimePacket(t, 65535, 0, []byte{1}))
	data.Write(realtimePacket(t, 1, 704, []byte{3}))
	senderControl.SetReadDeadline(time.Now().Add(time.Second))
	request := make([]byte, 32)
	n, _, err := senderControl.ReadFromUDP(request)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 || request[0] != 0x80 || request[1] != 0xd5 || binary.BigEndian.Uint16(request[4:6]) != 0 || binary.BigEndian.Uint16(request[6:8]) != 1 {
		t.Fatalf("retransmit request=%x", request[:n])
	}
	reply := append([]byte{0x80, 0xd6, 0, 1}, realtimePacket(t, 0, 352, []byte{2})...)
	if _, err := senderControl.WriteToUDP(reply, &net.UDPAddr{IP: s.PeerIP, Port: controlPort}); err != nil {
		t.Fatal(err)
	}
	waitRealtimeCondition(t, func() bool { return s.buffer.Len() == 3 })
	for _, seq := range []uint32{65535, 0, 1} {
		if f := s.buffer.Pop(); f.SequenceNumber != seq {
			t.Fatalf("seq=%d want%d", f.SequenceNumber, seq)
		}
	}
}

func TestRealtimeMissingPacketConcealsGapAtPlayoutDeadline(t *testing.T) {
	payload, err := os.ReadFile("../codec/testdata/alac-stereo16-44100.packet")
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := os.ReadFile("../codec/testdata/alac-stereo16-44100.pcm")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STATION_REALTIME_HELPER", "1")
	t.Setenv("STATION_REALTIME_BYTES", strconv.Itoa(len(pcm)*3))
	capture := filepath.Join(t.TempDir(), "pcm")
	t.Setenv("STATION_REALTIME_CAPTURE", capture)
	exe, _ := os.Executable()
	clock := ptp.NewVirtualClock(0)
	p := NewPlayer(t.Context(), clock, codec.OutputOptions{Command: exe, Args: []string{"-test.run=TestRealtimeAudioOutputHelper", "--"}})
	p.BindIP = "127.0.0.1"
	p.SetVolume(0)
	s := NewServer(p)
	s.PeerIP = net.ParseIP("127.0.0.1")
	defer func() { s.Close(); s.Wait() }()
	if _, _, err := s.SetupRealtime(make([]byte, 32), 44100); err != nil {
		t.Fatal(err)
	}
	for _, f := range []*PCMFrame{{TcpPacket: TcpPacket{SequenceNumber: 1, Timestamp: 0}, encodedData: bytes.Clone(payload)}, {TcpPacket: TcpPacket{SequenceNumber: 3, Timestamp: 704}, encodedData: bytes.Clone(payload)}} {
		s.enqueueRealtime(f)
	}
	s.applyRealtimeSync(0, clock.Now())
	waitRealtimeCondition(t, func() bool { b, err := os.ReadFile(capture); return err == nil && len(b) == len(pcm)*3 })
	got, _ := os.ReadFile(capture)
	want := append(bytes.Clone(pcm), make([]byte, len(pcm))...)
	want = append(want, pcm...)
	if !bytes.Equal(got, want) {
		t.Fatal("missing RTP packet was not replaced by one frame of silence")
	}
}

func TestRealtimeFlushAllRetainsHighBitTimelineFloor(t *testing.T) {
	p := NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	s := NewServer(p)
	defer s.Close()
	s.realtime = &realtimeState{}
	s.buffer = NewBuffer(4)
	s.enqueueRealtime(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 1, Timestamp: 0x80000000}})
	s.enqueueRealtime(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 2, Timestamp: 0x80000160}})
	s.FlushRealtimeAll()
	if s.buffer.Len() != 0 {
		t.Fatal("headerless flush retained queued audio")
	}
	for _, stamp := range []uint32{0x80000000, 0x80000160} {
		if s.enqueueRealtime(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 2, Timestamp: stamp}}) {
			t.Fatal("flushed packet accepted")
		}
	}
	if !s.enqueueRealtime(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 3, Timestamp: 0x800002c0}}) {
		t.Fatal("new high-bit RTP timeline rejected")
	}
	s.applyRealtimeSync(0x800002c0, p.Clock.Now())
	s.mu.Lock()
	playing, anchor := s.playing, s.haveAnchor
	s.mu.Unlock()
	if !playing || !anchor || s.buffer.Len() != 1 {
		t.Fatal("new sync did not resume retained future audio")
	}
	s.realtime = nil
}

func TestRealtimeFlushAllWithoutKnownTimestampDoesNotInventFloor(t *testing.T) {
	p := NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	s := NewServer(p)
	defer s.Close()
	s.realtime = &realtimeState{}
	s.FlushRealtimeAll()
	if !s.enqueueRealtime(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 1, Timestamp: 0x90000000}}) {
		t.Fatal("headerless flush invented a zero timestamp floor")
	}
	s.realtime = nil
}
