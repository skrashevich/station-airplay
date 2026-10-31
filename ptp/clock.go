// Adapted from goplay2; offset updates are atomic and system time is untouched.
package ptp

import (
	"sync/atomic"
	"time"
)

type VirtualClock struct {
	base   time.Time
	offset atomic.Int64
	delay  time.Duration
}

func NewVirtualClock(delay int64) *VirtualClock {
	return &VirtualClock{base: time.Now(), delay: time.Duration(delay) * time.Millisecond}
}
func (v *VirtualClock) Offset(diff time.Duration) { v.offset.Add(int64(diff)) }
func (v *VirtualClock) Now() time.Time {
	return time.Unix(0, int64(time.Since(v.base)-v.delay)-v.offset.Load())
}
