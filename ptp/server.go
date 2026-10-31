// Adapted from goplay2; bounded parsing and cancellation added.
package ptp

import (
	"context"
	"encoding"
	"fmt"
	wire "github.com/albanseurat/go-ptp"
	"net"
	"sync"
	"time"
)

type serverMsg struct {
	LocalTimeStamp time.Time
	Msg            encoding.BinaryUnmarshaler
	Src            *net.UDPAddr
}
type Server struct {
	genConn, eventConn *net.UDPConn
	clock              *VirtualClock
	identity           uint64
	measurements       *measures
}

func Listen(clock *VirtualClock, ip net.IP, identity uint64) (*Server, error) {
	g, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: 320})
	if err != nil {
		return nil, fmt.Errorf("PTP port 320: %w", err)
	}
	e, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: 319})
	if err != nil {
		g.Close()
		return nil, fmt.Errorf("PTP port 319: %w", err)
	}
	return &Server{genConn: g, eventConn: e, clock: clock, identity: identity, measurements: newMeasurements()}, nil
}
func (s *Server) Close() { s.genConn.Close(); s.eventConn.Close() }
func (s *Server) Serve(ctx context.Context) {
	defer s.Close()
	stop := context.AfterFunc(ctx, s.Close)
	defer stop()
	packets := make(chan serverMsg, 32)
	var wg sync.WaitGroup
	for _, conn := range []*net.UDPConn{s.genConn, s.eventConn} {
		wg.Go(func() {
			var b [2048]byte
			for {
				n, src, err := conn.ReadFromUDP(b[:])
				if err != nil {
					return
				}
				stamp := s.clock.Now()
				msg, err := decodePacket(b[:n])
				if err != nil || msg == nil {
					continue
				}
				select {
				case packets <- serverMsg{stamp, msg, src}:
				case <-ctx.Done():
					return
				}
			}
		})
	}
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-packets:
			switch x := p.Msg.(type) {
			case *wire.AnnounceMsg:
				s.measurements.currentUTCoffset = time.Duration(x.CurrentUtcOffset) * time.Second
			case *wire.SyncMsg:
				s.measurements.addSync(x.SequenceID, p.LocalTimeStamp)
			case *wire.FollowUpMsg:
				s.measurements.addFollowUp(x.SequenceID, x.PreciseOriginTimestamp)
				request := &wire.DelReqMsg{Header: wire.Header{Flags: wire.Flags{Unicast: true, TwoSteps: true, TimeScale: true}, MessageType: wire.DelayReqMsgType, MessageLength: wire.HeaderLen + wire.DelayReqPayloadLen, VersionPTP: wire.Version2, ClockIdentity: s.identity, PortNumber: 1, SequenceID: x.SequenceID, LogMessagePeriod: 0x7f}, OriginTimestamp: time.Unix(0, 0)}
				b, err := request.MarshalBinary()
				if err != nil {
					continue
				}
				b[0] |= 0x10
				s.measurements.addDelayReq(x.SequenceID, s.clock.Now())
				target := *p.Src
				target.Port = 319
				s.eventConn.WriteToUDP(b, &target)
			case *wire.DelRespMsg:
				s.measurements.addDelayResp(x.SequenceID, x.ReceiveTimestamp)
				if result, err := s.measurements.latest(); err == nil {
					s.clock.Offset(result.Offset)
				}
			}
			// Limit state even if a peer never completes an exchange.
			if len(s.measurements.serverToClient) > 64 || len(s.measurements.clientToServer) > 64 {
				s.measurements = newMeasurements()
			}
		}
	}
}
func decodePacket(b []byte) (encoding.BinaryUnmarshaler, error) {
	if len(b) < wire.HeaderLen {
		return nil, fmt.Errorf("truncated PTP header")
	}
	h := new(wire.Header)
	if err := h.UnmarshalBinary(b[:wire.HeaderLen]); err != nil {
		return nil, err
	}
	if int(h.MessageLength) > len(b) || int(h.MessageLength) < wire.HeaderLen {
		return nil, fmt.Errorf("invalid PTP length")
	}
	var msg encoding.BinaryUnmarshaler
	switch h.MessageType {
	case wire.AnnounceMsgType:
		msg = &wire.AnnounceMsg{}
	case wire.SyncMsgType:
		msg = &wire.SyncMsg{}
	case wire.FollowUpMsgType:
		msg = &wire.FollowUpMsg{}
	case wire.DelayRespMsgType:
		msg = &wire.DelRespMsg{}
	default:
		return nil, nil
	}
	if err := safeUnmarshal(msg, b[:h.MessageLength]); err != nil {
		return nil, err
	}
	return msg, nil
}
func safeUnmarshal(m encoding.BinaryUnmarshaler, b []byte) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("truncated PTP payload")
		}
	}()
	return m.UnmarshalBinary(b)
}
