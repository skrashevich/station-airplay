package audio

import (
	"context"
	"sync"
)

type Buffer struct {
	mu       sync.Mutex
	frames   []*PCMFrame
	capacity int
	changed  chan struct{}
}

func NewBuffer(capacity int) *Buffer {
	return &Buffer{capacity: capacity, changed: make(chan struct{}, 1)}
}
func (b *Buffer) signal() {
	select {
	case b.changed <- struct{}{}:
	default:
	}
}
func (b *Buffer) Push(ctx context.Context, f *PCMFrame) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		b.mu.Lock()
		if len(b.frames) < b.capacity {
			b.frames = append(b.frames, f)
			b.mu.Unlock()
			b.signal()
			return nil
		}
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-b.changed:
		}
	}
}
func (b *Buffer) Len() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.frames) }
func (b *Buffer) Peek() *PCMFrame {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.frames) == 0 {
		return nil
	}
	return b.frames[0]
}
func (b *Buffer) Pop() *PCMFrame {
	b.mu.Lock()
	if len(b.frames) == 0 {
		b.mu.Unlock()
		return nil
	}
	f := b.frames[0]
	copy(b.frames, b.frames[1:])
	b.frames[len(b.frames)-1] = nil
	b.frames = b.frames[:len(b.frames)-1]
	b.mu.Unlock()
	b.signal()
	return f
}
func sequenceInRange(seq, from, until uint32) bool {
	return (seq-from)&0xffffff <= (until-from)&0xffffff
}
func (b *Buffer) Flush(from, until uint32) {
	b.mu.Lock()
	out := b.frames[:0]
	for _, f := range b.frames {
		if !sequenceInRange(f.SequenceNumber, from, until) {
			out = append(out, f)
		}
	}
	clear(b.frames[len(out):])
	b.frames = out
	b.mu.Unlock()
	b.signal()
}
