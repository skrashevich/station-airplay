package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"log"
	"strconv"
	"strings"
	"testing"

	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/globals"
	"github.com/skrashevich/station-airplay/homekit"
	"github.com/skrashevich/station-airplay/ptp"
	"github.com/skrashevich/station-airplay/rtsp"
	"howett.net/plist"
)

func diagnosticHandler(t *testing.T) (*Rstp, *bytes.Buffer) {
	t.Helper()
	server, err := homekit.NewServer("AA:BB:CC:DD:EE:FF", t.TempDir(), []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	server.EventPort = 60003
	var logs bytes.Buffer
	p := audio.NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{})
	p.Logger = log.New(&logs, "", 0)
	r, err := NewRstpHandler(server, homekit.NewAccessory("AA:BB:CC:DD:EE:FF", "dde0cfc9-a2dc-4454-a706-957eae462cce", globals.NewFeatures(), server.Device.PublicKey()), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, s := range r.streams {
			s.Close()
			s.Wait()
		}
	})
	return r, &logs
}

func TestSetupDiagnosticsExplainRejectedNegotiationWithoutSecrets(t *testing.T) {
	key := bytes.Repeat([]byte("SECRET!!"), 4)
	for _, tc := range []struct {
		name   string
		value  any
		status rtsp.StatusCode
		want   []string
	}{
		{"event", map[string]any{"timingProtocol": "PTP"}, 200, []string{`timing="PTP"`, "streams_present=false", "streams=0", "event_port=60003"}},
		{"unsupported timing", map[string]any{"timingProtocol": "NTP"}, 461, []string{`timing="NTP"`, "streams_present=false"}},
		{"empty streams", map[string]any{"streams": []any{}}, 400, []string{"streams_present=true", "streams=0"}},
		{"unsupported stream", map[string]any{"streams": []any{map[string]any{"type": 96, "ct": 2, "audioFormat": uint64(1 << 22), "sr": 44100, "spf": 352, "shk": key, "ekey": []byte("ENCRYPTED-KEY-SECRET")}}}, 415, []string{"streams_present=true", "streams=1", "type=96", "ct=2", "audio_format=0x400000", "sr=44100", "spf=352", "key_bytes=32"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, logs := diagnosticHandler(t)
			body, err := plist.Marshal(tc.value, plist.BinaryFormat)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := r.OnSetupWeb(&rtsp.Request{Method: "SETUP", Path: "music", Body: body})
			if err != nil || resp.StatusCode != tc.status {
				t.Fatalf("response=%+v error=%v", resp, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("missing %q in %s", want, logs)
				}
			}
			for _, secret := range []string{string(key), hex.EncodeToString(key), base64.StdEncoding.EncodeToString(key), "ENCRYPTED-KEY-SECRET", "shk", "ekey"} {
				if strings.Contains(logs.String(), secret) {
					t.Errorf("secret %q leaked in %s", secret, logs)
				}
			}
		})
	}
}

func TestRTSPDiagnosticsReportResponseStatusWithoutPairingBody(t *testing.T) {
	for _, tc := range []struct {
		method rtsp.Method
		path   string
		status rtsp.StatusCode
	}{
		{"OPTIONS", "*", 200}, {"GET", "unknown", 501}, {"POST", "pair-setup", 400},
	} {
		t.Run(string(tc.method), func(t *testing.T) {
			r, logs := diagnosticHandler(t)
			resp, err := r.Handle(nil, &rtsp.Request{Method: tc.method, Path: tc.path, Header: rtsp.Header{"CSeq": {"17"}, "Authorization": {"AUTH-SECRET"}}, Body: []byte("PAIRING-SECRET")})
			if err != nil || resp == nil || resp.StatusCode != tc.status {
				t.Fatalf("response=%+v error=%v", resp, err)
			}
			if !strings.Contains(logs.String(), "status="+strconv.Itoa(int(tc.status))) || !strings.Contains(logs.String(), `cseq="17"`) {
				t.Fatalf("missing response diagnostics: %s", logs)
			}
			for _, secret := range []string{"PAIRING-SECRET", "AUTH-SECRET"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("secret leaked: %s", logs)
				}
			}
		})
	}
}
