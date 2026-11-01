package stationairplay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func outputFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "station-pcm")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestDefaultOutputUsesHostPayloadAndNewIsPassive(t *testing.T) {
	state := filepath.Join(t.TempDir(), "identity")
	receiver, err := New(Config{DataDir: state})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := executable
	if receiver.config.aplay != want || !receiver.config.ownedOutput {
		t.Fatalf("default output=%q owned=%v; want %q", receiver.config.aplay, receiver.config.ownedOutput, want)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("New created state: %v", err)
	}
	if receiver.Status().State != StateStopped {
		t.Fatal(receiver.Status())
	}
}
func TestOwnedOutputCheckUsesPreflightMode(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "checked")
	helper := outputFixture(t, "[ \"$#\" = 1 ] && [ \"$1\" = --check ] || exit 9\nprintf checked > '"+marker+"'")
	if err := checkOutput(t.Context(), helper, nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "checked" {
		t.Fatalf("preflight was not run: %q %v", data, err)
	}
}
func TestOwnedOutputFailureStopsStartupBeforeCreatingState(t *testing.T) {
	state := filepath.Join(t.TempDir(), "identity")
	receiver, err := New(Config{DataDir: state})
	if err != nil {
		t.Fatal(err)
	}
	host := filepath.Join(t.TempDir(), "unbundled-ELF")
	header := make([]byte, 64)
	copy(header, "\x7fELF")
	header[4] = 2
	header[5] = 1
	if err = os.WriteFile(host, header, 0o700); err != nil {
		t.Fatal(err)
	}
	receiver.config.aplay = host
	if _, err := receiver.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "PCM payload") {
		t.Fatalf("Check: %v", err)
	}
	if err := receiver.Start(t.Context()); err == nil || !strings.Contains(err.Error(), "PCM payload") {
		t.Fatalf("Start: %v", err)
	}
	status := receiver.Status()
	if status.State != StateStopped || !strings.Contains(status.Error, "PCM payload") {
		t.Fatalf("failed startup status: %+v", status)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed preflight created identity: %v", err)
	}
	if err := receiver.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}
func TestCustomOutputIsNotExecutedDuringEnvironmentCheck(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	helper := outputFixture(t, "printf unexpected > '"+marker+"'\nexit 9")
	receiver, err := New(Config{DataDir: t.TempDir(), Aplay: helper, Interface: "no-such-airplay-interface"})
	if err != nil {
		t.Fatal(err)
	}
	if receiver.config.ownedOutput {
		t.Fatal("custom command marked owned")
	}
	if _, err := receiver.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "no usable IPv4") {
		t.Fatalf("unexpected custom output check: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("custom output executed in preflight: %v", err)
	}
}
func TestOwnedOutputPreflightHonorsCancellation(t *testing.T) {
	helper := outputFixture(t, "exec sleep 30")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := checkOutput(ctx, helper, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("check cancellation: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("canceled check took %v", elapsed)
	}
}

func TestOwnedPreflightEnvironmentIsScopedToChild(t *testing.T) {
	t.Setenv("STATION_PREFLIGHT_VALUE", "parent-value")
	marker := filepath.Join(t.TempDir(), "child-env")
	helper := outputFixture(t, "printf '%s' \"$STATION_PREFLIGHT_VALUE\" > '"+marker+"'")
	if err := checkOutput(t.Context(), helper, []string{"STATION_PREFLIGHT_VALUE=child-value"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "child-value" {
		t.Fatalf("child env: %q %v", data, err)
	}
	if os.Getenv("STATION_PREFLIGHT_VALUE") != "parent-value" {
		t.Fatal("process environment changed")
	}
}
func TestOwnedPreflightReportsHelperDiagnostic(t *testing.T) {
	helper := outputFixture(t, "echo missing-alsa-symbol >&2\nexit 7")
	if err := checkOutput(t.Context(), helper, nil); err == nil || !strings.Contains(err.Error(), "missing-alsa-symbol") {
		t.Fatalf("preflight diagnostic: %v", err)
	}
}
