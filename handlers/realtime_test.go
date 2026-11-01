package handlers

import (
	"net"
	"testing"

	"github.com/skrashevich/station-airplay/rtsp"
	"howett.net/plist"
)

func TestStreamRateAcceptsCapturedRealtimeALAC(t *testing.T) {
	stream := setupStreamRequest{Type: 96, CompressionType: 2, AudioFormat: 1 << 18, SampleRate: 44100, Spf: 352, SharedKey: make([]byte, 32)}
	if rate, err := streamRate(stream); err != nil || rate != 44100 {
		t.Fatalf("captured realtime ALAC: rate=%d error=%v", rate, err)
	}
	for _, change := range []struct {
		name string
		edit func(*setupStreamRequest)
	}{
		{"codec", func(s *setupStreamRequest) { s.CompressionType = 4 }},
		{"format", func(s *setupStreamRequest) { s.AudioFormat = 1 << 22 }},
		{"rate", func(s *setupStreamRequest) { s.SampleRate = 48000 }},
		{"samples", func(s *setupStreamRequest) { s.Spf = 4096 }},
		{"key", func(s *setupStreamRequest) { s.SharedKey = make([]byte, 16) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			invalid := stream
			change.edit(&invalid)
			if _, err := streamRate(invalid); err == nil {
				t.Fatal("accepted unsupported realtime format")
			}
		})
	}
}

func TestSetupCapturedRealtimeRequestCreatesUDPSockets(t *testing.T) {
	r, _ := diagnosticHandler(t)
	r.player.BindIP = "127.0.0.1"
	r.peerIP = net.ParseIP("127.0.0.1")
	eventBody, _ := plist.Marshal(map[string]any{"timingProtocol": "PTP"}, plist.BinaryFormat)
	resp, err := r.OnSetupWeb(&rtsp.Request{Path: "music", Body: eventBody})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("event setup: %+v %v", resp, err)
	}
	body, _ := plist.Marshal(setupRtsp{Streams: []setupStreamRequest{{Type: 96, CompressionType: 2, AudioFormat: 1 << 18, SampleRate: 44100, Spf: 352, SharedKey: make([]byte, 32)}}}, plist.BinaryFormat)
	resp, err = r.OnSetupWeb(&rtsp.Request{Path: "music", Body: body})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("realtime setup: %+v %v", resp, err)
	}
	var result setupSteamsResponse
	if _, err = plist.Unmarshal(resp.Body, &result); err != nil || len(result.Streams) != 1 {
		t.Fatalf("stream response: %+v %v", result, err)
	}
	s := result.Streams[0]
	if s.TypeStream != 96 || s.AudioFormat != 1<<18 || s.DataPort == 0 || s.ControlPort == 0 || s.DataPort == s.ControlPort {
		t.Fatalf("realtime ports/format missing: %+v", s)
	}
	for _, port := range []uint16{s.DataPort, s.ControlPort} {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: r.peerIP, Port: int(port)})
		if err == nil {
			conn.Close()
			t.Fatalf("UDP port %d is not bound", port)
		}
	}
	resp, err = r.OnFlushWeb(&rtsp.Request{Path: "music", Header: rtsp.Header{"RTP-Info": {"seq=42;rtptime=12345"}}})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("realtime flush: %+v %v", resp, err)
	}
	resp, err = r.OnFlushWeb(&rtsp.Request{Path: "music", Header: rtsp.Header{"RTP-Info": {"seq=42;rtptime=no"}}})
	if err != nil || resp.StatusCode != 400 {
		t.Fatalf("invalid flush: %+v %v", resp, err)
	}
	if connected, _ := r.player.PlaybackState(); !connected {
		t.Fatal("flush released the sender")
	}
	for _, header := range []rtsp.Header{nil, {"RTP-Info": {"seq=42"}}} {
		resp, err = r.OnFlushWeb(&rtsp.Request{Path: "music", Header: header})
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("timestamp-less flush: %+v %v", resp, err)
		}
	}
}

func TestRealtimeFlushTimestampValidation(t *testing.T) {
	for _, tc := range []struct {
		header  string
		rtp     uint32
		valid   bool
		present bool
	}{
		{"seq=65535;rtptime=4294967295", 4294967295, true, true},
		{"rtptime=0;seq=0", 0, true, true},
		{"seq = 1; rtptime = 123", 123, true, true},
		{"seq=1", 0, true, false},
		{"rtptime=-1", 0, false, false},
		{"rtptime=4294967296", 0, false, false},
		{"rtptime=123;rtptime=124", 0, false, false},
		{"seq=65536;rtptime=123", 0, false, false},
		{"seq=no;rtptime=123", 0, false, false},
	} {
		t.Run(tc.header, func(t *testing.T) {
			rtp, present, err := realtimeFlushTimestamp(tc.header)
			if (err == nil) != tc.valid || (err == nil && (rtp != tc.rtp || present != tc.present)) {
				t.Fatalf("rtp=%d err=%v", rtp, err)
			}
		})
	}
}
