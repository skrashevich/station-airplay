package ptp

import (
	"testing"
	"time"
)

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
