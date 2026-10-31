package codec

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOutputHelper(t *testing.T) {
	if os.Getenv("STATION_OUTPUT_HELPER") == "" {
		return
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
