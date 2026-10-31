package audio

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAudioOutputHelper(t *testing.T) {
	if os.Getenv("STATION_AUDIO_HELPER") == "" {
		return
	}
	b, _ := io.ReadAll(os.Stdin)
	os.WriteFile(os.Getenv("STATION_AUDIO_CAPTURE"), b, 0600)
	os.Exit(0)
}
func TestBufferedStreamDrainsAndReleasesOwnership(t *testing.T) {
	t.Setenv("STATION_AUDIO_HELPER", "1")
	dest := filepath.Join(t.TempDir(), "pcm")
	t.Setenv("STATION_AUDIO_CAPTURE", dest)
	exe, _ := os.Executable()
	clock := ptp.NewVirtualClock(0)
	p := NewPlayer(t.Context(), clock, codec.OutputOptions{Command: exe, Args: []string{"-test.run=TestAudioOutputHelper", "--"}})
	p.BindIP = "127.0.0.1"
	p.SetVolume(0)
	s := NewServer(p)
	defer s.Close()
	port, err := s.Setup(make([]byte, 32), 44100)
	if err != nil {
		t.Fatal(err)
	}
	other := NewServer(p)
	defer other.Close()
	if _, err := other.Setup(make([]byte, 32), 44100); err == nil {
		t.Fatal("second sender stole audio")
	}
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
	if err != nil {
		t.Fatal(err)
	}
	packet := encryptedFixture(t)
	binary.Write(c, binary.BigEndian, uint16(len(packet)+2))
	c.Write(packet)
	c.Close()
	s.SetRateAnchorTime(4096, clock.Now())
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	d := codec.NewAacDecoder()
	d.InitRaw([]byte{0x12, 0x10})
	f, err := NewFrame(d, make([]byte, 32), packet)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, f.Data()) {
		t.Fatalf("drained PCM bytes=%d want=%d", len(got), len(f.Data()))
	}
	s2 := NewServer(p)
	defer s2.Close()
	if _, err := s2.Setup(make([]byte, 32), 44100); err != nil {
		t.Fatal("ownership not released", err)
	}
}
func fmtPort(port int) string { // independent network setup helper
	return fmt.Sprintf("%d", port)
}

func fixturePayload(t *testing.T, index int) []byte {
	t.Helper()
	data, err := os.ReadFile("../codec/testdata/tone.aac")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; len(data) >= 7; i++ {
		n := int(data[3]&3)<<11 | int(data[4])<<3 | int(data[5]>>5)
		if i == index {
			return append([]byte(nil), data[7:n]...)
		}
		data = data[n:]
	}
	t.Fatal("fixture index")
	return nil
}
func waitEmpty(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for s.buffer.Len() != 0 {
		select {
		case <-deadline.C:
			t.Fatal("playback did not consume buffer")
		case <-tick.C:
		}
	}
}
func TestResumeDropsOldFramesAndResetsAACHistory(t *testing.T) {
	t.Setenv("STATION_AUDIO_HELPER", "1")
	dest := filepath.Join(t.TempDir(), "pcm")
	t.Setenv("STATION_AUDIO_CAPTURE", dest)
	exe, _ := os.Executable()
	clock := ptp.NewVirtualClock(0)
	p := NewPlayer(t.Context(), clock, codec.OutputOptions{Command: exe, Args: []string{"-test.run=TestAudioOutputHelper", "--"}})
	p.BindIP = "127.0.0.1"
	p.SetVolume(0)
	s := NewServer(p)
	defer s.Close()
	port, err := s.Setup(make([]byte, 32), 44100)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s.enqueue(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 0, Timestamp: 0}, encodedData: fixturePayload(t, 3)})
	s.SetRateAnchorTime(0, clock.Now())
	waitEmpty(t, s)
	s.SetRate0()
	s.enqueue(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 1, Timestamp: 1024}, encodedData: fixturePayload(t, 4)})
	payload := fixturePayload(t, 5)
	s.enqueue(&PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 20, Timestamp: 4096}, encodedData: payload})
	s.SetRateAnchorTime(4096, clock.Now())
	c.Close()
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("resume hung")
	}
	d := codec.NewAacDecoder()
	d.InitRaw([]byte{0x12, 0x10})
	want, err := d.Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("resumed output contains stale PCM or AAC history: %d bytes", len(got))
	}
}
func TestAnchorRecoversAfterPTPClockConverges(t *testing.T) {
	t.Setenv("STATION_AUDIO_HELPER", "1")
	dest := filepath.Join(t.TempDir(), "pcm")
	t.Setenv("STATION_AUDIO_CAPTURE", dest)
	exe, _ := os.Executable()
	clock := ptp.NewVirtualClock(0)
	p := NewPlayer(t.Context(), clock, codec.OutputOptions{Command: exe, Args: []string{"-test.run=TestAudioOutputHelper", "--"}})
	p.BindIP = "127.0.0.1"
	s := NewServer(p)
	defer s.Close()
	port, err := s.Setup(make([]byte, 32), 44100)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s.enqueue(&PCMFrame{TcpPacket: TcpPacket{Timestamp: 0}, encodedData: fixturePayload(t, 0)})
	s.SetRateAnchorTime(0, clock.Now().Add(time.Hour))
	clock.Offset(-time.Hour)
	c.Close()
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("anchor stayed frozen before PTP convergence")
	}
	if b, err := os.ReadFile(dest); err != nil || len(b) != 4096 {
		t.Fatalf("output=%d err=%v", len(b), err)
	}
}
