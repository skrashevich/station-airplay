package ptp

import (
	"encoding/binary"
	wire "github.com/albanseurat/go-ptp"
	"testing"
	"time"
)

func TestFollowUpAcceptsTrailingProfileTLV(t *testing.T) {
	// iPhone FollowUp: fixed 44-byte PTP message followed by a profile TLV.
	b := make([]byte, 96)
	b[0] = 0x18
	b[1] = 2
	binary.BigEndian.PutUint16(b[2:], 96)
	binary.BigEndian.PutUint64(b[20:], 0x1020304050607080)
	binary.BigEndian.PutUint16(b[30:], 820)
	binary.BigEndian.PutUint32(b[36:], 7449)
	binary.BigEndian.PutUint32(b[40:], 405603458)
	binary.BigEndian.PutUint16(b[44:], 3)
	binary.BigEndian.PutUint16(b[46:], 48)
	msg, err := decodePacket(b)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := msg.(*wire.FollowUpMsg)
	if !ok {
		t.Fatalf("unexpected message %T", msg)
	}
	if !f.PreciseOriginTimestamp.Equal(time.Unix(7449, 405603458)) {
		t.Fatal("profile TLV affected timestamp")
	}
}

func TestFollowUpRespectsDeclaredMessageLength(t *testing.T) {
	b := make([]byte, 96)
	b[0] = 0x18
	b[1] = 2
	binary.BigEndian.PutUint16(b[2:], 34)
	if _, err := decodePacket(b); err == nil {
		t.Fatal("FollowUp accepted a timestamp outside its declared message")
	}
}

func TestVirtualOffsetDoesNotChangeSystemClock(t *testing.T) {
	v := NewVirtualClock(0)
	before := v.Now()
	v.Offset(2 * time.Second)
	delta := v.Now().Sub(before)
	if delta < -2100*time.Millisecond || delta > -1900*time.Millisecond {
		t.Fatalf("offset=%v", delta)
	}
}
func TestTruncatedPTPPacketDoesNotPanic(t *testing.T) {
	for n := 0; n < 34; n++ {
		if _, err := decodePacket(make([]byte, n)); err == nil {
			t.Fatalf("accepted %d-byte packet", n)
		}
	}
}
