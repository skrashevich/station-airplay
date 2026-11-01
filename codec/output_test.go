package codec

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOutputHelper(t *testing.T) {
	if os.Getenv("STATION_OUTPUT_HELPER") == "" {
		return
	}
	if path := os.Getenv("STATION_OUTPUT_ENV_CAPTURE"); path != "" {
		if err := os.WriteFile(path, []byte(os.Getenv("STATION_OUTPUT_ENV_VALUE")), 0o600); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	if path := os.Getenv("STATION_OUTPUT_TERM"); path != "" {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		os.WriteFile(path+".ready", []byte("ready"), 0600)
		<-ch
		os.WriteFile(path, []byte("clean"), 0600)
		os.Exit(0)
	}
	if os.Getenv("STATION_OUTPUT_BLOCK") == "1" {
		time.Sleep(time.Hour)
		return
	}
	b, _ := io.ReadAll(os.Stdin)
	if err := os.WriteFile(os.Getenv("STATION_OUTPUT_CAPTURE"), append([]byte(strings.Join(os.Args, " ")+"\n"), b...), 0600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestOutputCloseAllowsChildToReleaseMixer(t *testing.T) {
	t.Setenv("STATION_OUTPUT_HELPER", "1")
	dest := filepath.Join(t.TempDir(), "cleanup")
	t.Setenv("STATION_OUTPUT_TERM", dest)
	exe, _ := os.Executable()
	o, err := OpenOutput(t.Context(), OutputOptions{Command: exe, Args: []string{"-test.run=TestOutputHelper", "--"}, Rate: 44100})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(dest + ".ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child not ready")
		}
		time.Sleep(time.Millisecond)
	}
	o.Close()
	if b, err := os.ReadFile(dest); err != nil || string(b) != "clean" {
		t.Fatalf("child killed before mixer cleanup: %v", err)
	}
}

func TestOutputDeliversPCMWithNegotiatedFormat(t *testing.T) {
	t.Setenv("STATION_OUTPUT_HELPER", "1")
	dest := filepath.Join(t.TempDir(), "capture")
	t.Setenv("STATION_OUTPUT_CAPTURE", dest)
	exe, _ := os.Executable()
	o, err := OpenOutput(t.Context(), OutputOptions{Command: exe, Args: []string{"-test.run=TestOutputHelper", "--"}, Device: "default", Rate: 44100})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	input := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	if _, err = o.Write(input); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err = o.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "-D default -t raw -f S16_LE -c 2 -r 44100") {
		t.Fatalf("wrong aplay parameters: %q", got)
	}
	if !bytes.HasSuffix(got, input) {
		t.Fatal("PCM altered or truncated")
	}
}

func TestOutputCloseUnblocksBlockedWriter(t *testing.T) {
	t.Setenv("STATION_OUTPUT_HELPER", "1")
	t.Setenv("STATION_OUTPUT_BLOCK", "1")
	exe, _ := os.Executable()
	o, err := OpenOutput(t.Context(), OutputOptions{Command: exe, Args: []string{"-test.run=TestOutputHelper", "--"}, Device: "default", Rate: 48000})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := o.Write(make([]byte, 1024*1024)); done <- err }()
	o.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("write remains blocked after Close")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOutputEnvironmentIsScopedToChild(t *testing.T) {
	t.Setenv("STATION_OUTPUT_ENV_VALUE", "parent-value")
	destination := filepath.Join(t.TempDir(), "child-env")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	output, err := OpenOutput(t.Context(), OutputOptions{Command: executable, Args: []string{"-test.run=TestOutputHelper", "--"}, Rate: 44100, Env: []string{"STATION_OUTPUT_HELPER=1", "STATION_OUTPUT_ENV_CAPTURE=" + destination, "STATION_OUTPUT_ENV_VALUE=child-value"}})
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err = output.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(destination)
	if err != nil || string(actual) != "child-value" {
		t.Fatalf("child environment: %q %v", actual, err)
	}
	if os.Getenv("STATION_OUTPUT_ENV_VALUE") != "parent-value" {
		t.Fatal("process environment changed")
	}
}
