// Adapted from goplay2; bounded parsing and cancellation added.
package ptp

import (
	"context"
	"encoding"
	"encoding/binary"
	"fmt"
	wire "github.com/albanseurat/go-ptp"
	"log"
	"net"
	"sync"
	"time"
)

type serverMsg struct {
	LocalTimeStamp time.Time
	Msg            encoding.BinaryUnmarshaler
	Src            *net.UDPAddr
	ClockIdentity  uint64
}
type Server struct {
	Logger             *log.Logger
	genConn, eventConn *net.UDPConn
	clock              *VirtualClock
	identity           uint64
	measurements       *measures
	mu                 sync.Mutex
	owner              any
	allowed            map[string]bool
	master             uint64
	generation         uint64
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
func (s *Server) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	failures := make(chan error, 2)
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
					failures <- err
					return
				}
				stamp := time.Now()
				msg, err := decodePacket(b[:n])
				if err != nil || msg == nil {
					continue
				}
				select {
				case packets <- serverMsg{LocalTimeStamp: stamp, Msg: msg, Src: src, ClockIdentity: binary.BigEndian.Uint64(b[20:28])}:
				case <-ctx.Done():
					return
				}
			}
		})
	}
	defer func() { cancel(); s.Close(); wg.Wait() }()
	var generation uint64
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-failures:
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("PTP receive: %w", err)
		case p := <-packets:
			gen, ok := s.accept(p.Src.IP, p.ClockIdentity)
			if !ok {
				continue
			}
			if gen != generation {
				s.measurements = newMeasurements()
				generation = gen
			}
			switch x := p.Msg.(type) {
			case *wire.AnnounceMsg:
				s.measurements.currentUTCoffset = time.Duration(x.CurrentUtcOffset) * time.Second
			case *wire.SyncMsg:
				s.measurements.addSync(x.SequenceID, s.clock.At(p.LocalTimeStamp))
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
				if x.RequestingPortIdentity != s.identity || x.RequestingPortID != 1 {
					continue
				}
				s.measurements.addDelayResp(x.SequenceID, x.ReceiveTimestamp)
				if result, err := s.measurements.latest(); err == nil {
					if result.Offset > 100*time.Millisecond || result.Offset < -100*time.Millisecond {
						s.logf("PTP correction offset=%v delay=%v", result.Offset, result.Delay)
					}
					s.applyOffset(gen, result.Offset)
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
	if h.VersionPTP != wire.Version2 {
		return nil, fmt.Errorf("unsupported PTP version")
	}
	if int(h.MessageLength) > len(b) || int(h.MessageLength) < wire.HeaderLen {
		return nil, fmt.Errorf("invalid PTP length")
	}
	b = b[:h.MessageLength]
	var msg encoding.BinaryUnmarshaler
	switch h.MessageType {
	case wire.AnnounceMsgType:
		msg = &wire.AnnounceMsg{}
	case wire.SyncMsgType:
		msg = &wire.SyncMsg{}
	case wire.FollowUpMsgType:
		if len(b) < wire.HeaderLen+wire.FollowUpPayloadLen {
			return nil, fmt.Errorf("truncated PTP FollowUp")
		}
		// The dependency expects exactly the fixed timestamp payload; Apple adds profile TLVs.
		b = b[:wire.HeaderLen+wire.FollowUpPayloadLen]
		msg = &wire.FollowUpMsg{}
	case wire.DelayRespMsgType:
		msg = &wire.DelRespMsg{}
	default:
		return nil, nil
	}
	if err := safeUnmarshal(msg, b); err != nil {
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

// Selection changes are scoped to the claimed audio owner, so stale cleanup
// from the previous session cannot reset a new sender's timing state.
func (s *Server) Bind(owner any, peers []net.IP) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.owner = owner
	s.allowed = peerMap(peers)
	s.master = 0
	s.generation++
}
func (s *Server) Clear(owner any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner == owner {
		s.owner = nil
		s.allowed = nil
		s.master = 0
		s.generation++
	}
}
func (s *Server) SetPeers(owner any, peers []net.IP, id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner != owner {
		return
	}
	s.allowed = peerMap(peers)
	if id != 0 {
		if s.master != id {
			s.logf("PTP master selected clock=%016x", id)
		}
		s.master = id
	}
	s.generation++
}
func peerMap(peers []net.IP) map[string]bool {
	m := make(map[string]bool)
	for _, ip := range peers {
		if ip != nil {
			m[ip.String()] = true
		}
	}
	return m
}
func (s *Server) accept(ip net.IP, id uint64) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner == nil || !s.allowed[ip.String()] || id == 0 {
		return s.generation, false
	}
	if s.master == 0 {
		s.master = id
		s.generation++
	}
	return s.generation, s.master == id
}

func (s *Server) applyOffset(generation uint64, offset time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner == nil || s.generation != generation {
		return false
	}
	s.clock.Offset(offset)
	return true
}

func (s *Server) logf(format string, args ...any) {
	logger := s.Logger
	if logger == nil {
		logger = log.Default()
	}
	logger.Printf(format, args...)
}
