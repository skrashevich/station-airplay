package audio

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
	"golang.org/x/crypto/chacha20poly1305"
)

// These packets include the valid authentication tag even when there is no AAC AU.
func writeBufferedAAC(t *testing.T, connection net.Conn, sequence, timestamp uint32, payload []byte) {
	t.Helper()
	header := make([]byte, 12)
	header[0] = 0x80
	header[1], header[2], header[3] = byte(sequence>>16), byte(sequence>>8), byte(sequence)
	binary.BigEndian.PutUint32(header[4:8], timestamp)
	cipher, err := chacha20poly1305.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], uint64(sequence)+1)
	packet := cipher.Seal(header, nonce, payload, header[4:12])
	packet = append(packet, nonce[4:]...)
	if err := binary.Write(connection, binary.BigEndian, uint16(len(packet)+2)); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(packet); err != nil {
		t.Fatal(err)
	}
}

func waitBufferedConsumed(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if s.Context().Err() != nil {
			t.Fatal("AAC stream closed while consuming an authenticated empty packet")
		}
		if s.buffer.Len() == 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("AAC packet was not consumed")
		case <-ticker.C:
		}
	}
}

func TestBufferedEmptyAACDoesNotOpenOutput(t *testing.T) {
	clock := ptp.NewVirtualClock(0)
	player := NewPlayer(t.Context(), clock, codec.OutputOptions{Command: "/no-audio-process-should-be-started"})
	player.BindIP = "127.0.0.1"
	stream := NewServer(player)
	defer stream.Close()
	port, err := stream.Setup(make([]byte, 32), 44100)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	changed := stream.buffer.Changed()
	writeBufferedAAC(t, connection, 0, 0, nil)
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("authenticated empty packet was not received")
	}
	if stream.buffer.Len() != 1 {
		t.Fatal("empty packet was not queued before playout")
	}
	stream.SetRateAnchorTime(0, clock.Now())
	waitBufferedConsumed(t, stream)
	stream.mu.Lock()
	output := stream.output
	stream.mu.Unlock()
	if output != nil {
		t.Fatal("empty AAC packet opened an audio process")
	}
	connection.Close()
	select {
	case <-stream.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("empty-only stream did not finish")
	}
	if player.Current() != nil {
		t.Fatal("empty-only stream retained audio ownership")
	}
}

func TestBufferedEmptyAACPreservesHistoryAcrossSequenceWrap(t *testing.T) {
	t.Setenv("STATION_AUDIO_HELPER", "1")
	destination := filepath.Join(t.TempDir(), "pcm")
	t.Setenv("STATION_AUDIO_CAPTURE", destination)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	clock := ptp.NewVirtualClock(0)
	player := NewPlayer(t.Context(), clock, codec.OutputOptions{Command: executable, Args: []string{"-test.run=TestAudioOutputHelper", "--"}})
	player.BindIP = "127.0.0.1"
	player.SetVolume(0)
	stream := NewServer(player)
	defer stream.Close()
	port, err := stream.Setup(make([]byte, 32), 44100)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	first, second := fixturePayload(t, 3), fixturePayload(t, 4)
	packets := []struct {
		sequence, timestamp uint32
		data                []byte
	}{
		{0xfffffe, 0, nil}, {0xffffff, 0, first}, {0, 1024, nil}, {1, 1024, second}, {2, 2048, nil},
	}
	// Queue the entire authenticated stream before the anchor to remove timing races.
	for _, packet := range packets {
		writeBufferedAAC(t, connection, packet.sequence, packet.timestamp, packet.data)
	}
	connection.Close()
	select {
	case <-stream.inputDone:
	case <-time.After(2 * time.Second):
		t.Fatal("authenticated media input did not finish")
	}
	stream.SetRateAnchorTime(0, clock.Now())
	select {
	case <-stream.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stream with empty AAC packets did not finish")
	}
	decoder := codec.NewAacDecoder()
	if err := decoder.InitRaw([]byte{0x12, 0x10}); err != nil {
		t.Fatal(err)
	}
	firstPCM, err := decoder.Decode(first)
	if err != nil {
		t.Fatal(err)
	}
	secondPCM, err := decoder.Decode(second)
	if err != nil {
		t.Fatal(err)
	}
	fresh := codec.NewAacDecoder()
	if err := fresh.InitRaw([]byte{0x12, 0x10}); err != nil {
		t.Fatal(err)
	}
	resetPCM, err := fresh.Decode(second)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(secondPCM, resetPCM) {
		t.Fatal("fixture does not distinguish retained AAC history from a decoder reset")
	}
	want := append(firstPCM, secondPCM...)
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("empty packets changed audio/history or generated silence: bytes=%d want=%d", len(got), len(want))
	}
}

func TestBufferedNonemptyMalformedAACStillClosesStream(t *testing.T) {
	clock := ptp.NewVirtualClock(0)
	player := NewPlayer(t.Context(), clock, codec.OutputOptions{Command: "/no-audio-process-should-be-started"})
	player.BindIP = "127.0.0.1"
	stream := NewServer(player)
	defer stream.Close()
	port, err := stream.Setup(make([]byte, 32), 44100)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", fmtPort(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	writeBufferedAAC(t, connection, 0, 0, []byte{0})
	stream.SetRateAnchorTime(0, clock.Now())
	select {
	case <-stream.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("malformed nonempty AAC was silently accepted")
	}
	if player.Current() != nil {
		t.Fatal("malformed AAC retained audio ownership")
	}
}
