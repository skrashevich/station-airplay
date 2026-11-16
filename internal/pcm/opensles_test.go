//go:build (linux && (arm || arm64 || amd64)) || darwin

package pcm

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func queueFixture(t *testing.T) (*openSLDevice, *uint32, *[][]byte) {
	t.Helper()
	memory, err := unix.Mmap(-1, 0, 3*4096, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Munmap(memory) })
	count, received := new(uint32), new([][]byte)
	d := &openSLDevice{memory: memory, state: func(state *slQueueState) uint32 { state.Count = *count; return 0 },
		enqueue: func(data unsafe.Pointer, length uint32) uint32 {
			*received = append(*received, bytes.Clone(unsafe.Slice((*byte)(data), int(length))))
			*count++
			return 0
		}, play: func(uint32) uint32 { return 0 }, clear: func() uint32 { *count = 0; return 0 }}
	return d, count, received
}

// Reusing a queued buffer or returning a byte count instead of frames corrupts audio.
func TestOpenSLQueueRetainsPCMAndWaitsForFreeSlots(t *testing.T) {
	d, count, received := queueFixture(t)
	for _, value := range []byte{1, 2, 3} {
		input := bytes.Repeat([]byte{value}, 4096)
		if written := d.write(input); written != 1024 {
			t.Fatalf("frame count=%d", written)
		}
		clear(input)
	}
	if written := d.write(bytes.Repeat([]byte{4}, 4096)); written != -int(syscall.EAGAIN) || len(*received) != 3 {
		t.Fatalf("full queue was overwritten: written=%d", written)
	}
	for i, value := range []byte{1, 2, 3} {
		if !bytes.Equal(d.memory[i*4096:(i+1)*4096], bytes.Repeat([]byte{value}, 4096)) {
			t.Fatal("native queue did not retain its PCM buffer")
		}
	}
	*count = 2
	if written := d.write([]byte{4, 3, 2, 1}); written != 1 || !bytes.Equal((*received)[3], []byte{4, 3, 2, 1}) {
		t.Fatalf("released slot not reused with exact tail: written=%d", written)
	}
}

func TestOpenSLDrainStartsShortStreamOnceAndWaits(t *testing.T) {
	d, count, _ := queueFixture(t)
	var states []uint32
	d.play = func(state uint32) uint32 { states = append(states, state); return 0 }
	if d.write([]byte{1, 2, 3, 4}) != 1 || len(states) != 0 {
		t.Fatal("stream started before prebuffering")
	}
	for range 2 {
		if d.drain() != -int(syscall.EAGAIN) {
			t.Fatal("queued audio reported as drained")
		}
	}
	*count = 0
	if d.drain() != 0 || !slices.Equal(states, []uint32{3}) {
		t.Fatalf("short stream started repeatedly or lost tail: %v", states)
	}
}

func TestOpenSLDropStopsBeforeClearing(t *testing.T) {
	d, _, _ := queueFixture(t)
	var calls []string
	d.play = func(state uint32) uint32 {
		if state != 1 {
			t.Fatal("drop did not select STOPPED")
		}
		calls = append(calls, "stop")
		return 0
	}
	d.clear = func() uint32 { calls = append(calls, "clear"); return 0 }
	d.drop()
	if !slices.Equal(calls, []string{"stop", "clear"}) {
		t.Fatalf("drop order=%v", calls)
	}
}

func TestOpenSLReportsNativeFailureAndCancellation(t *testing.T) {
	d, count, _ := queueFixture(t)
	*count = 4
	if code := d.write([]byte{1, 2, 3, 4}); code != -int(syscall.EIO) || d.err(code) == nil {
		t.Fatal("impossible native queue count accepted")
	}
	d, count, _ = queueFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d.state = func(state *slQueueState) uint32 { state.Count = 3; cancel(); return 0 }
	if err := transfer(ctx, bytes.NewReader([]byte{1, 2, 3, 4}), d); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestOpenSLPropagatesNativeErrors(t *testing.T) {
	for _, step := range []string{"state", "enqueue", "play"} {
		t.Run(step, func(t *testing.T) {
			d, _, _ := queueFixture(t)
			switch step {
			case "state":
				d.state = func(*slQueueState) uint32 { return 13 }
			case "enqueue":
				d.enqueue = func(unsafe.Pointer, uint32) uint32 { return 13 }
			case "play":
				d.play = func(uint32) uint32 { return 13 }
				if d.write([]byte{1, 2, 3, 4}) != 1 {
					t.Fatal("first buffer failed before the player started")
				}
			}
			code := d.write([]byte{1, 2, 3, 4})
			if code != -int(syscall.EIO) || !strings.Contains(d.err(code).Error(), "result 13") {
				t.Fatalf("native %s error lost: code=%d error=%v", step, code, d.err(code))
			}
		})
	}
}

func TestOpenSLValidatesBeforeLoadingLibrary(t *testing.T) {
	t.Setenv("STATION_PCM_LIBRARY", "/missing-open-sl-library")
	for _, cfg := range []Config{{Rate: 22050}, {Rate: 44100, Device: "hw:0"}} {
		if err := playOpenSL(t.Context(), strings.NewReader(""), cfg); err == nil || strings.Contains(err.Error(), "load OpenSL") {
			t.Fatalf("invalid native settings attempted to load a library: %v", err)
		}
	}
	if err := checkOpenSL(); err == nil || !strings.Contains(err.Error(), "missing-open-sl-library") {
		t.Fatalf("OpenSL library override ignored: %v", err)
	}
}

func TestNativeBackendMarkerMatchesSelectedLoader(t *testing.T) {
	// Android hosts can also have an isolated glibc userspace. Its helper must
	// select ALSA despite Android system library paths existing on that host.
	t.Setenv("STATION_PCM_BACKEND", "alsa")
	if androidPCM() {
		t.Fatal("glibc loader marker selected the Android backend")
	}
	t.Setenv("STATION_PCM_BACKEND", "opensl")
	if !androidPCM() {
		t.Fatal("Bionic loader marker selected the ALSA backend")
	}
}
