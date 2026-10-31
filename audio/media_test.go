package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/skrashevich/station-airplay/codec"
	"golang.org/x/crypto/chacha20poly1305"
	"os"
	"testing"
	"time"
)

func encryptedFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../codec/testdata/tone.aac")
	if err != nil {
		t.Fatal(err)
	}
	n := int(data[3]&3)<<11 | int(data[4])<<3 | int(data[5]>>5)
	header := []byte{0x80, 0x01, 0x02, 0x03, 0, 0, 0x10, 0, 0, 0, 0, 0}
	aead, _ := chacha20poly1305.New(make([]byte, 32))
	nonce := make([]byte, 12)
	nonce[11] = 1
	b := aead.Seal(append([]byte(nil), header...), nonce, data[7:n], header[4:12])
	return append(b, nonce[4:]...)
}
func TestAuthenticatedAACFrameAndCorruptPackets(t *testing.T) {
	packet := encryptedFixture(t)
	d := codec.NewAacDecoder()
	d.InitRaw([]byte{0x12, 0x10})
	f, err := NewFrame(d, make([]byte, 32), packet)
	if err != nil {
		t.Fatal(err)
	}
	if f.SequenceNumber != 0x010203 || f.Timestamp != 4096 || len(f.Data()) != 4096 {
		t.Fatalf("wrong frame: %#v", f)
	}
	for _, bad := range [][]byte{nil, packet[:10], packet[:35], append([]byte(nil), packet...)} {
		if len(bad) == len(packet) {
			bad[15] ^= 1
		}
		if _, err := NewFrame(d, make([]byte, 32), bad); err == nil {
			t.Fatal("accepted corrupt/truncated media")
		}
	}
}
func TestReadPacketRejectsUnderflow(t *testing.T) {
	for _, b := range [][]byte{{0, 0}, {0, 1}, {0, 37}, {0, 40, 1, 2}} {
		if _, err := ReadPacket(bytes.NewReader(b)); err == nil {
			t.Fatalf("accepted %x", b)
		}
	}
}
func TestBufferFlushAndCancellation(t *testing.T) {
	b := NewBuffer(2)
	ctx := t.Context()
	b.Push(ctx, &PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 0xffffff}})
	b.Push(ctx, &PCMFrame{TcpPacket: TcpPacket{SequenceNumber: 0}})
	c, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- b.Push(c, &PCMFrame{}) }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("blocked push ignored cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("push hung")
	}
	b.Flush(0xffffff, 0)
	if b.Len() != 0 {
		t.Fatal("wrapped sequence range not flushed")
	}
}
func TestRTPFramingRoundTrip(t *testing.T) {
	p := encryptedFixture(t)
	var b bytes.Buffer
	binary.Write(&b, binary.BigEndian, uint16(len(p)+2))
	b.Write(p)
	got, err := ReadPacket(&b)
	if err != nil || !bytes.Equal(got, p) {
		t.Fatalf("read: %v", err)
	}
}
