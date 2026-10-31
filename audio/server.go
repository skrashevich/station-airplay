// AirPlay 2 buffered TCP audio. Protocol framing derives from goplay2.
package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/skrashevich/station-airplay/codec"
	"io"
	"log"
	"math"
	"net"
	"sync"
	"time"
)

type sequenceRange struct{ from, until uint32 }
type Server struct {
	player                       *Player
	ctx                          context.Context
	cancel                       context.CancelFunc
	rate                         int
	sharedKey                    []byte
	PeerIP                       net.IP
	mu                           sync.Mutex
	listener                     net.Listener
	conn                         net.Conn
	output                       *codec.Output
	playing, started, haveAnchor bool
	generation                   uint64
	anchorRTP                    uint32
	anchorTime                   time.Time // network clock, converted afresh as PTP converges
	discarded                    []sequenceRange
	buffer                       *Buffer
	wake                         chan struct{}
	inputDone, done              chan struct{}
	once                         sync.Once
}

func NewServer(p *Player) *Server {
	ctx, cancel := context.WithCancel(p.Context)
	return &Server{player: p, ctx: ctx, cancel: cancel, rate: 44100, buffer: NewBuffer(128), wake: make(chan struct{}, 1), inputDone: make(chan struct{}), done: make(chan struct{})}
}
func (s *Server) Setup(key []byte, rate int) (int, error) {
	if len(key) != 32 || (rate != 44100 && rate != 48000) {
		return 0, fmt.Errorf("unsupported audio key/format")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if s.started {
		return 0, fmt.Errorf("stream already set up")
	}
	if err := s.player.claim(s); err != nil {
		return 0, err
	}
	l, err := net.Listen("tcp4", net.JoinHostPort(s.player.BindIP, "0"))
	if err != nil {
		s.player.release(s)
		return 0, err
	}
	s.rate = rate
	s.sharedKey = append([]byte(nil), key...)
	s.listener = l
	s.started = true
	go s.receive(l)
	go s.play()
	return l.Addr().(*net.TCPAddr).Port, nil
}
func (s *Server) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Server) receive(l net.Listener) {
	defer close(s.inputDone)
	if tl, ok := l.(*net.TCPListener); ok {
		tl.SetDeadline(time.Now().Add(30 * time.Second))
	}
	var c net.Conn
	for {
		var err error
		c, err = l.Accept()
		if err != nil {
			if s.ctx.Err() == nil {
				log.Printf("audio accept: %v", err)
			}
			s.Close()
			return
		}
		if s.PeerIP == nil || c.RemoteAddr().(*net.TCPAddr).IP.Equal(s.PeerIP) {
			break
		}
		c.Close()
	}
	l.Close()
	s.mu.Lock()
	if s.ctx.Err() != nil {
		s.mu.Unlock()
		c.Close()
		return
	}
	s.conn = c
	s.mu.Unlock()
	defer c.Close()
	for {
		c.SetReadDeadline(time.Now().Add(60 * time.Second))
		raw, err := ReadPacket(c)
		if err != nil {
			if err != io.EOF && s.ctx.Err() == nil {
				log.Printf("audio input: %v", err)
				s.Close()
			}
			return
		}
		// Authenticate before queueing; decode only at playout, so a flush cannot
		// leave predecoded samples or overlap history from the old timeline.
		f, err := ParseFrame(s.sharedKey, raw)
		if err != nil {
			log.Printf("audio input: %v", err)
			s.Close()
			return
		}
		if err = s.enqueue(f); err != nil {
			return
		}
	}
}
func (s *Server) enqueue(f *PCMFrame) error {
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		changed := s.buffer.Changed()
		s.mu.Lock()
		if s.haveAnchor && int32(f.Timestamp-s.anchorRTP) < 0 {
			s.mu.Unlock()
			return nil
		}
		skip := false
		for _, r := range s.discarded {
			if sequenceInRange(f.SequenceNumber, r.from, r.until) {
				skip = true
				break
			}
		}
		if skip {
			s.mu.Unlock()
			return nil
		}
		// TCP media sequence numbers advance monotonically modulo 24 bits.
		ranges := s.discarded[:0]
		for _, r := range s.discarded {
			if !sequenceAfter(f.SequenceNumber, r.until) {
				ranges = append(ranges, r)
			}
		}
		s.discarded = ranges
		ok := s.buffer.TryPush(f)
		s.mu.Unlock()
		if ok {
			return nil
		}
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-changed:
		}
	}
}
func (s *Server) SetRateAnchorTime(rtp uint32, network time.Time) {
	s.mu.Lock()
	before := s.buffer.Len()
	s.buffer.TrimBefore(rtp)
	reset := !s.playing || s.buffer.Len() != before
	var old *codec.Output
	if reset {
		s.generation++
		old = s.output
		s.output = nil
	}
	s.anchorRTP = rtp
	s.anchorTime = network
	s.haveAnchor = true
	s.playing = true
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	s.signal()
}
func (s *Server) SetTimingPeers(peers []net.IP, id uint64) {
	all := append([]net.IP{s.PeerIP}, peers...)
	s.player.SetTimingPeers(s, all, id)
}
func (s *Server) SetRate0() {
	s.mu.Lock()
	s.playing = false
	s.generation++
	old := s.output
	s.output = nil
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	s.signal()
}
func (s *Server) Flush(from, until uint64) {
	s.mu.Lock()
	s.generation++
	s.discarded = append(s.discarded, sequenceRange{uint32(from), uint32(until)})
	s.buffer.Flush(uint32(from), uint32(until))
	old := s.output
	s.output = nil
	overflow := len(s.discarded) > 64
	s.mu.Unlock()
	if old != nil {
		old.Close()
	}
	if overflow {
		s.Close()
	}
	s.signal()
}
func (s *Server) Teardown() { s.Close() }
func (s *Server) Close() {
	s.once.Do(func() {
		s.cancel()
		s.mu.Lock()
		l, c, old := s.listener, s.conn, s.output
		s.output = nil
		if !s.started {
			close(s.done)
		}
		s.mu.Unlock()
		if l != nil {
			l.Close()
		}
		if c != nil {
			c.Close()
		}
		if old != nil {
			old.Close()
		}
		s.player.release(s)
		s.signal()
	})
}
func (s *Server) Done() <-chan struct{} { return s.done }
func (s *Server) newDecoder() *codec.AacDecoder {
	d := codec.NewAacDecoder()
	asc := []byte{0x12, 0x10}
	if s.rate == 48000 {
		asc = []byte{0x11, 0x90}
	}
	d.InitRaw(asc)
	return d
}
func (s *Server) play() {
	defer close(s.done)
	defer s.Close()
	var decoder *codec.AacDecoder
	var epoch uint64 = math.MaxUint64
	var lastSeq uint32
	haveSeq := false
	nextTimingLog := time.Time{}
	for {
		if s.ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		playing, gen, anchor, anchorRTP := s.playing, s.generation, s.anchorTime, s.anchorRTP
		s.mu.Unlock()
		if !playing {
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
			}
			continue
		}
		changed := s.buffer.Changed()
		f := s.buffer.Peek()
		if f == nil {
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
			case <-changed:
			case <-s.inputDone:
				if s.buffer.Len() == 0 {
					s.drainOutput()
					return
				}
			}
			continue
		}
		until := anchor.Sub(s.player.Clock.Now()) + time.Duration(int64(int32(f.Timestamp-anchorRTP)))*time.Second/time.Duration(s.rate) - s.player.OutputDelay
		if until > 0 {
			if until > time.Second && time.Now().After(nextTimingLog) {
				log.Printf("audio timing wait=%v queued=%d frame=%d anchor=%d anchor_sec=%d clock_sec=%d", until.Truncate(time.Millisecond), s.buffer.Len(), f.Timestamp, anchorRTP, anchor.Unix(), s.player.Clock.Now().Unix())
				nextTimingLog = time.Now().Add(5 * time.Second)
			}
			timer := time.NewTimer(min(until, 100*time.Millisecond))
			select {
			case <-timer.C:
			case <-s.ctx.Done():
				timer.Stop()
				return
			case <-s.wake:
				timer.Stop()
			}
			continue
		}
		s.mu.Lock()
		valid := s.playing && s.generation == gen && s.buffer.Peek() == f
		s.mu.Unlock()
		if !valid {
			continue
		}
		if decoder == nil || epoch != gen || (haveSeq && (lastSeq+1)&0xffffff != f.SequenceNumber) {
			decoder = s.newDecoder()
			epoch = gen
			haveSeq = false
		}
		pcm, err := decoder.Decode(f.encodedData)
		if err != nil {
			log.Printf("AAC decode: %v", err)
			return
		}
		s.mu.Lock()
		if !s.playing || s.generation != gen || s.buffer.Peek() != f || s.ctx.Err() != nil {
			s.mu.Unlock()
			continue
		}
		if s.output == nil {
			opts := s.player.Output
			opts.Rate = s.rate
			o, err := codec.OpenOutput(s.ctx, opts)
			if err != nil {
				s.mu.Unlock()
				log.Printf("audio output: %v", err)
				return
			}
			s.output = o
		}
		output := s.output
		s.mu.Unlock()
		gain := s.player.Gain()
		for i := 0; i < len(pcm); i += 2 {
			v := int16(binary.LittleEndian.Uint16(pcm[i:]))
			binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(math.Round(float64(v)*gain))))
		}
		_, err = output.Write(pcm)
		s.mu.Lock()
		stale := s.generation != gen || !s.playing
		if err == nil && !stale && s.buffer.Peek() == f {
			s.buffer.Pop()
			lastSeq = f.SequenceNumber
			haveSeq = true
		}
		s.mu.Unlock()
		if err != nil && !stale {
			if s.ctx.Err() == nil {
				log.Printf("audio output: %v", err)
			}
			return
		}
	}
}
func (s *Server) drainOutput() {
	s.mu.Lock()
	o := s.output
	s.mu.Unlock()
	if o == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	if err := o.Drain(ctx); err != nil && s.ctx.Err() == nil {
		log.Printf("audio drain: %v", err)
	}
}
