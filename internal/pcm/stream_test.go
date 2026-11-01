package pcm

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

type testDevice struct {
	output           []byte
	results          []int
	waitCancel       context.CancelFunc
	recoverError     int
	drained, dropped bool
}

func (d *testDevice) write(p []byte) int {
	n := len(p) / 4
	if len(d.results) > 0 {
		n = d.results[0]
		d.results = d.results[1:]
	}
	if n > 0 && n <= len(p)/4 {
		d.output = append(d.output, p[:n*4]...)
	}
	return n
}
func (d *testDevice) wait() int {
	if d.waitCancel != nil {
		d.waitCancel()
	}
	return 0
}
func (d *testDevice) recover(int) int { return d.recoverError }
func (d *testDevice) drain() int      { d.drained = true; return 0 }
func (d *testDevice) drop()           { d.dropped = true }
func (d *testDevice) err(n int) error { return syscall.Errno(-n) }

func TestTransferPreservesPCMWithShortWritesAndRecovery(t *testing.T) {
	input := bytes.Repeat([]byte{1, 2, 3, 4, 5, 6, 7, 8}, 1500)
	d := &testDevice{results: []int{-int(syscall.EAGAIN), -int(syscall.EPIPE), 1, 2, 3}}
	if err := transfer(t.Context(), bytes.NewReader(input), d); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(input, d.output) {
		t.Fatal("PCM changed or lost")
	}
	if !d.drained || d.dropped {
		t.Fatal("finite input was not drained")
	}
}
func TestTransferRejectsTruncatedFrame(t *testing.T) {
	d := &testDevice{}
	if err := transfer(t.Context(), bytes.NewReader([]byte{1, 2, 3, 4, 5}), d); err == nil {
		t.Fatal("accepted incomplete stereo frame")
	}
	if len(d.output) != 0 || !d.dropped || d.drained {
		t.Fatal("truncated input reached output or was drained")
	}
}
func TestTransferCancellationDropsWithoutDrain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d := &testDevice{results: []int{-int(syscall.EAGAIN)}, waitCancel: cancel}
	err := transfer(ctx, bytes.NewReader([]byte{1, 2, 3, 4}), d)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if !d.dropped || d.drained {
		t.Fatal("canceled stream was not dropped")
	}
}
func TestTransferReturnsOutputError(t *testing.T) {
	d := &testDevice{results: []int{-int(syscall.ENODEV)}, recoverError: -int(syscall.ENODEV)}
	if err := transfer(t.Context(), bytes.NewReader([]byte{1, 2, 3, 4}), d); !errors.Is(err, syscall.ENODEV) {
		t.Fatalf("got %v", err)
	}
	if !d.dropped || d.drained {
		t.Fatal("failed output was not dropped")
	}
}

type failedReader struct{}

func (failedReader) Read(p []byte) (int, error) {
	copy(p, []byte{1, 2, 3, 4})
	return 4, io.ErrClosedPipe
}
func TestTransferReportsReaderError(t *testing.T) {
	d := &testDevice{}
	if err := transfer(t.Context(), failedReader{}, d); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("got %v", err)
	}
	if !d.dropped || d.drained {
		t.Fatal("failed input was not dropped")
	}
}

func TestTransferEmptyInputDrains(t *testing.T) {
	d := &testDevice{}
	if err := transfer(t.Context(), bytes.NewReader(nil), d); err != nil {
		t.Fatal(err)
	}
	if len(d.output) != 0 || !d.drained || d.dropped {
		t.Fatal("empty stream did not drain cleanly")
	}
}
func TestTransferRejectsInvalidBackendFrameCount(t *testing.T) {
	d := &testDevice{results: []int{2}}
	if err := transfer(t.Context(), bytes.NewReader([]byte{1, 2, 3, 4}), d); err == nil {
		t.Fatal("accepted invalid backend count")
	}
	if !d.dropped || d.drained {
		t.Fatal("invalid backend count did not drop")
	}
}
func TestTransferAlreadyCanceledDoesNotWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d := &testDevice{}
	if err := transfer(ctx, bytes.NewReader([]byte{1, 2, 3, 4}), d); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if len(d.output) != 0 || !d.dropped || d.drained {
		t.Fatal("canceled stream wrote PCM")
	}
}

type notifyingReader struct {
	input   io.Reader
	started chan struct{}
}

func (r notifyingReader) Read(p []byte) (int, error) { close(r.started); return r.input.Read(p) }
func TestTransferClosedInputReportsContextCancellation(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stopClose := context.AfterFunc(ctx, func() { read.Close() })
	defer stopClose()
	d := &testDevice{}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- transfer(ctx, notifyingReader{read, started}, d) }()
	<-started
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
		if !d.dropped || d.drained {
			t.Fatal("canceled input was not dropped")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled input did not stop")
	}
}
