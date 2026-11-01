package probe

import (
	"bytes"
	"context"
	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/globals"
	"github.com/skrashevich/station-airplay/handlers"
	"github.com/skrashevich/station-airplay/homekit"
	"github.com/skrashevich/station-airplay/ptp"
	"github.com/skrashevich/station-airplay/rtsp"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"testing"
)

func TestProbeOutput(t *testing.T) {
	if os.Getenv("STATION_PROBE_HELPER") == "" {
		return
	}
	b, _ := io.ReadAll(os.Stdin)
	os.WriteFile(os.Getenv("STATION_PROBE_FILE"), b, 0600)
	os.Exit(0)
}
func TestProbeExercisesEncryptedRTSPAndMedia(t *testing.T) {
	t.Setenv("STATION_PROBE_HELPER", "1")
	capture := t.TempDir() + "/pcm"
	t.Setenv("STATION_PROBE_FILE", capture)
	server, err := homekit.NewServer("AA:BB:CC:DD:EE:FF", t.TempDir(), []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	server.EventPort = 60003
	accessory := homekit.NewAccessory("AA:BB:CC:DD:EE:FF", "dde0cfc9-a2dc-4454-a706-957eae462cce", globals.NewFeatures().Set(9), server.Device.PublicKey())
	exe, _ := os.Executable()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p := audio.NewPlayer(ctx, ptp.NewVirtualClock(0), codec.OutputOptions{Command: exe, Args: []string{"-test.run=TestProbeOutput", "--"}})
	var logs bytes.Buffer
	p.Logger = log.New(&logs, "", 0)
	p.BindIP = "127.0.0.1"
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	done := make(chan error, 1)
	go func() {
		done <- rtsp.Serve(ctx, l, func() (rtsp.Handler, error) { return handlers.NewRstpHandler(server, accessory, p) })
	}()
	if err := Run(ctx, l.Addr().String(), "../../codec/testdata/tone.aac", -30); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`method=POST path="pair-setup"`, `method=SETUP path="probe"`, "status=200", `timing="PTP"`, "streams_present=false", "streams_present=true", "type=103 ct=4", "key_bytes=32", "event_port=60003", "data_port="} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("missing %q in encrypted probe diagnostics: %s", want, &logs)
		}
	}
	b, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 9*4096 {
		t.Fatalf("captured %d PCM bytes", len(b))
	}
}
