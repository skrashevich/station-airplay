package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/skrashevich/station-airplay/internal/pcm"
)

func TestRunValidatesAndPassesPCMConfig(t *testing.T) {
	var got pcm.Config
	input := bytes.NewReader([]byte{1, 2, 3, 4})
	err := run(t.Context(), []string{"-q", "-D", "music", "-t", "raw", "-f", "S16_LE", "-c", "2", "-r", "48000"}, input, io.Discard, func() error { t.Fatal("unexpected check"); return nil }, func(_ context.Context, r io.Reader, c pcm.Config) error {
		got = c
		if r != input {
			t.Fatal("input replaced")
		}
		return nil
	})
	if err != nil || got.Device != "music" || got.Rate != 48000 {
		t.Fatalf("%+v %v", got, err)
	}
}
func TestCheckDoesNotOpenPCM(t *testing.T) {
	failure := errors.New("missing ALSA")
	err := run(t.Context(), []string{"--check"}, strings.NewReader(""), io.Discard, func() error { return failure }, func(context.Context, io.Reader, pcm.Config) error { t.Fatal("check opened PCM"); return nil })
	if !errors.Is(err, failure) {
		t.Fatalf("got %v", err)
	}
}
func TestRejectUnsupportedArgumentsBeforeOpeningPCM(t *testing.T) {
	for _, args := range [][]string{{"-f", "FLOAT_LE"}, {"-c", "1"}, {"-r", "22050"}, {"-t", "wav"}, {"song.raw"}, {"-unknown"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := run(t.Context(), args, strings.NewReader(""), io.Discard, func() error { t.Fatal("invalid arguments load ALSA"); return nil }, func(context.Context, io.Reader, pcm.Config) error { t.Fatal("invalid arguments open PCM"); return nil })
			if err == nil {
				t.Fatal("accepted unsupported input")
			}
		})
	}
}
