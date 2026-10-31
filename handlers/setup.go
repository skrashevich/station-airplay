// Adapted from goplay2; explicit negotiation and no model-specific audio setup.
package handlers

import (
	"fmt"
	"github.com/skrashevich/station-airplay/audio"
	"github.com/skrashevich/station-airplay/globals"
	"github.com/skrashevich/station-airplay/homekit"
	"github.com/skrashevich/station-airplay/rtsp"
	"howett.net/plist"
	"net"
)

type setupStreamRequest struct {
	Type            uint8  `plist:"type"`
	SharedKey       []byte `plist:"shk"`
	Spf             uint32 `plist:"spf"`
	CompressionType uint32 `plist:"ct"`
	AudioFormat     uint64 `plist:"audioFormat"`
	SampleRate      uint32 `plist:"sr"`
}
type setupRtsp struct {
	Streams        []setupStreamRequest `plist:"streams"`
	TimingProtocol string               `plist:"timingProtocol"`
}
type peerInfos struct {
	Addresses                 []string `plist:"Addresses"`
	Id                        string   `plist:"ID"`
	ClockPortMatchingOverride bool     `plist:"SupportsClockPortMatchingOverride"`
}
type setupEventResponse struct {
	EventPort  uint16    `plist:"eventPort"`
	PeerInfo   peerInfos `plist:"timingPeerInfo"`
	TimingPort uint16    `plist:"timingPort"`
}
type setupStream struct {
	BufferSize  uint32 `plist:"audioBufferSize"`
	AudioFormat uint64 `plist:"audioFormat"`
	DataPort    uint16 `plist:"dataPort"`
	ControlPort uint16 `plist:"controlPort"`
	TypeStream  uint8  `plist:"type"`
}
type setupSteamsResponse struct {
	Streams []setupStream `plist:"streams"`
}

func plistResponse(value any) (*rtsp.Response, error) {
	body, err := plist.Marshal(value, plist.BinaryFormat)
	if err != nil {
		return nil, err
	}
	return &rtsp.Response{StatusCode: rtsp.StatusOK, Header: rtsp.Header{"Content-Type": {"application/x-apple-binary-plist"}}, Body: body}, nil
}
func streamRate(v setupStreamRequest) (int, error) {
	if v.Type != 103 || v.CompressionType != 4 || len(v.SharedKey) != 32 || (v.Spf != 0 && v.Spf != 1024) {
		return 0, fmt.Errorf("only buffered AAC-LC stereo is supported")
	}
	rate := 44100
	switch v.AudioFormat {
	case 1 << 22:
		rate = 44100
	case 1 << 24:
		rate = 48000
	default:
		return 0, fmt.Errorf("unsupported audioFormat %#x", v.AudioFormat)
	}
	if v.SampleRate != 0 && v.SampleRate != uint32(rate) {
		return 0, fmt.Errorf("inconsistent sample rate")
	}
	return rate, nil
}
func (r *Rstp) OnSetupWeb(req *rtsp.Request) (*rtsp.Response, error) {
	var v setupRtsp
	if _, err := plist.Unmarshal(req.Body, &v); err != nil {
		return &rtsp.Response{StatusCode: rtsp.StatusBadRequest}, nil
	}
	if v.Streams == nil {
		if v.TimingProtocol != "" && v.TimingProtocol != "PTP" {
			return &rtsp.Response{StatusCode: rtsp.StatusUnsupportedTransport}, nil
		}
		if old := r.streams[req.Path]; old != nil {
			old.Close()
		}
		if len(r.streams) > 0 && r.streams[req.Path] == nil {
			return &rtsp.Response{StatusCode: rtsp.StatusMethodNotValidInThisState}, nil
		}
		s := audio.NewServer(r.player)
		s.PeerIP = r.peerIP
		r.streams[req.Path] = s
		return plistResponse(setupEventResponse{EventPort: homekit.Server.EventPort, PeerInfo: peerInfos{Addresses: homekit.Server.Ips, Id: homekit.Device.Pi.String()}})
	}
	if len(v.Streams) != 1 {
		return &rtsp.Response{StatusCode: rtsp.StatusBadRequest}, nil
	}
	rate, err := streamRate(v.Streams[0])
	if err != nil {
		return &rtsp.Response{StatusCode: rtsp.StatusUnsupportedMediaType}, nil
	}
	s := r.streams[req.Path]
	if s == nil {
		return &rtsp.Response{StatusCode: rtsp.StatusSessionNotFound}, nil
	}
	port, err := s.Setup(v.Streams[0].SharedKey, rate)
	if err != nil {
		return &rtsp.Response{StatusCode: rtsp.StatusNotEnoughBandwidth}, nil
	}
	s.SetTimingPeers(r.peers, r.clockID)
	r.player.SetVolumeFor(s, r.volume)
	return plistResponse(setupSteamsResponse{Streams: []setupStream{{BufferSize: globals.BufferSize, AudioFormat: v.Streams[0].AudioFormat, DataPort: uint16(port), TypeStream: 103}}})
}
func (r *Rstp) OnSetPeerWeb(req *rtsp.Request) (*rtsp.Response, error) {
	var value any
	if _, err := plist.Unmarshal(req.Body, &value); err != nil {
		return &rtsp.Response{StatusCode: rtsp.StatusBadRequest}, nil
	}
	var peers []net.IP
	var walk func(any, int)
	walk = func(v any, depth int) {
		if depth > 8 || len(peers) >= 16 {
			return
		}
		switch x := v.(type) {
		case string:
			if ip := net.ParseIP(x); ip != nil {
				peers = append(peers, ip)
			}
		case []any:
			for _, e := range x {
				walk(e, depth+1)
			}
		case map[string]any:
			for key, e := range x {
				if key == "Addresses" || key == "addresses" || key == "peers" || key == "timingPeerInfo" {
					walk(e, depth+1)
				}
			}
		}
	}
	walk(value, 0)
	r.peers = peers
	for _, s := range r.streams {
		s.SetTimingPeers(peers, r.clockID)
	}
	return &rtsp.Response{StatusCode: rtsp.StatusOK}, nil
}
