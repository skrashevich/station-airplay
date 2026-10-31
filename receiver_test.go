package stationairplay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

type testService struct {
	serve  func(context.Context) error
	closed atomic.Int32
}

func (s *testService) Serve(ctx context.Context) error { return s.serve(ctx) }
func (s *testService) Close()                          { s.closed.Add(1) }
func (s *testService) Status() Status                  { return Status{} }

func testReceiver(t *testing.T) *Receiver {
	t.Helper()
	r, err := New(Config{DataDir: filepath.Join(t.TempDir(), "identity")})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewHasNoIOAndDoesNotRequireInstalledAplay(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	r, err := New(Config{DataDir: dir, Aplay: "missing-firmware-aplay"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("New created state: %v", err)
	}
	if r.Status().State != StateStopped {
		t.Fatal(r.Status())
	}
	if err := r.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentStartIsIdempotentAndStopWaitsForCleanup(t *testing.T) {
	r := testReceiver(t)
	var opened atomic.Int32
	s := &testService{serve: func(ctx context.Context) error { <-ctx.Done(); return nil }}
	r.open = func(context.Context, settings) (service, error) { opened.Add(1); return s, nil }
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if err := r.Start(t.Context()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if opened.Load() != 1 || r.Status().State != StateRunning {
		t.Fatalf("opened=%d status=%+v", opened.Load(), r.Status())
	}
	if err := r.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.closed.Load() != 1 || r.Status().State != StateStopped {
		t.Fatal("Stop returned before cleanup")
	}
	if err := r.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationCancellationStopsReceiver(t *testing.T) {
	r := testReceiver(t)
	s := &testService{serve: func(ctx context.Context) error { <-ctx.Done(); return nil }}
	r.open = func(context.Context, settings) (service, error) { return s, nil }
	ctx, cancel := context.WithCancel(t.Context())
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := r.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.closed.Load() != 1 || r.Status().State != StateStopped {
		t.Fatal(r.Status())
	}
}

func TestFailedStartCanBeRetried(t *testing.T) {
	r := testReceiver(t)
	want := errors.New("port already in use")
	r.open = func(context.Context, settings) (service, error) { return nil, want }
	if err := r.Start(t.Context()); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if r.Status().State != StateStopped || r.Status().Error != want.Error() {
		t.Fatal(r.Status())
	}
	r.open = func(context.Context, settings) (service, error) {
		return &testService{serve: func(ctx context.Context) error { <-ctx.Done(); return nil }}, nil
	}
	if err := r.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.Status().Error != "" {
		t.Fatal(r.Status())
	}
	if err := r.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestStopCancelsStartup(t *testing.T) {
	r := testReceiver(t)
	opening := make(chan struct{})
	r.open = func(ctx context.Context, _ settings) (service, error) {
		close(opening)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- r.Start(t.Context()) }()
	<-opening
	if r.Status().State != StateStarting {
		t.Fatal(r.Status())
	}
	if err := r.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if r.Status().State != StateStopped {
		t.Fatal(r.Status())
	}
}

func TestStopDeadlineDoesNotReportCleanupCompleteOrPermitOverlap(t *testing.T) {
	r := testReceiver(t)
	release := make(chan struct{})
	s := &testService{serve: func(context.Context) error { <-release; return nil }}
	r.open = func(context.Context, settings) (service, error) { return s, nil }
	if err := r.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.Stop(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if r.Status().State != StateStopping || s.closed.Load() != 0 {
		t.Fatal("cleanup incorrectly reported complete")
	}
	if err := r.Start(t.Context()); !errors.Is(err, ErrStopping) {
		t.Fatal(err)
	}
	close(release)
	if err := r.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestWaitReportsRuntimeFailureAndReceiverCanRestart(t *testing.T) {
	r := testReceiver(t)
	fail := make(chan struct{})
	want := errors.New("listener failed")
	s := &testService{serve: func(context.Context) error { <-fail; return want }}
	r.open = func(context.Context, settings) (service, error) { return s, nil }
	if err := r.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(fail)
	if err := r.Wait(t.Context()); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if s.closed.Load() != 1 || r.Status().State != StateStopped || r.Status().Error != want.Error() {
		t.Fatal(r.Status())
	}
}
