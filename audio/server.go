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

type Server struct {
	player     *Player
	ctx        context.Context
	cancel     context.CancelFunc
	decoder    *codec.AacDecoder
	rate       int
	sharedKey  []byte
	PeerIP     net.IP
	mu         sync.Mutex
	listener   net.Listener
	conn       net.Conn
	output     *codec.Output
	playing    bool
	generation uint64
	anchorRTP  uint32
	anchorTime time.Time
	buffer     *Buffer
	wake       chan struct{}
	inputDone  chan struct{}
	done       chan struct{}
	once       sync.Once
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
	if s.listener != nil {
		return 0, fmt.Errorf("stream already set up")
	}
	if err := s.player.claim(s); err != nil {
		return 0, err
	}
	d := codec.NewAacDecoder()
	asc := []byte{0x12, 0x10}
	if rate == 48000 {
		asc = []byte{0x11, 0x90}
	}
	if err := d.InitRaw(asc); err != nil {
		s.player.release(s)
		return 0, err
	}
	l, err := net.Listen("tcp4", net.JoinHostPort(s.player.BindIP, "0"))
	if err != nil {
		s.player.release(s)
		return 0, err
	}
	s.decoder = d
	s.rate = rate
	s.sharedKey = append([]byte(nil), key...)
	s.listener = l
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
	c, err := l.Accept()
	if err != nil {
		if s.ctx.Err() == nil {
			log.Printf("audio accept: %v", err)
		}
		s.Close()
		return
	}
	l.Close()
	if s.PeerIP != nil && !c.RemoteAddr().(*net.TCPAddr).IP.Equal(s.PeerIP) {
		c.Close()
		s.Close()
		return
	}
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
		frame, err := NewFrame(s.decoder, s.sharedKey, raw)
		if err != nil {
			log.Printf("audio decode: %v", err)
			s.Close()
			return
		}
		if err = s.buffer.Push(s.ctx, frame); err != nil {
			return
		}
	}
}
func (s *Server) SetRateAnchorTime(rtp uint32, network time.Time) {
	now := time.Now()
	target := now.Add(network.Sub(s.player.Clock.Now()))
	s.mu.Lock()
	s.anchorRTP = rtp
	s.anchorTime = target
	s.playing = true
	s.mu.Unlock()
	s.signal()
}
func (s *Server) SetRate0() {
	s.mu.Lock()
	s.playing = false
	s.generation++
	s.mu.Unlock()
	s.stopOutput()
	s.signal()
}
func (s *Server) Flush(from, until uint64) {
	s.mu.Lock()
	s.generation++
	s.buffer.Flush(uint32(from), uint32(until))
	s.mu.Unlock()
	s.stopOutput()
	s.signal()
}
func (s *Server) Teardown() { s.Close() }
func (s *Server) stopOutput() {
	s.mu.Lock()
	o := s.output
	s.output = nil
	s.mu.Unlock()
	if o != nil {
		o.Close()
	}
}
func (s *Server) Close() {
	s.once.Do(func() {
		s.cancel()
		s.mu.Lock()
		l, c := s.listener, s.conn
		s.mu.Unlock()
		if l != nil {
			l.Close()
		}
		if c != nil {
			c.Close()
		}
		s.stopOutput()
		s.player.release(s)
		s.signal()
	})
}
func (s *Server) Done() <-chan struct{} { return s.done }
func (s *Server) play() {
	defer close(s.done)
	defer s.Close()
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
		f := s.buffer.Peek()
		if f == nil {
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
			case <-s.buffer.changed:
			case <-s.inputDone:
				if s.buffer.Len() == 0 {
					s.drainOutput()
					return
				}
			}
			continue
		}
		target := anchor.Add(time.Duration(int64(int32(f.Timestamp-anchorRTP)))*time.Second/time.Duration(s.rate) - s.player.OutputDelay)
		if wait := time.Until(target); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-s.ctx.Done():
				timer.Stop()
				return
			case <-s.wake:
				timer.Stop()
				continue
			}
			timer.Stop()
		}
		s.mu.Lock()
		if !s.playing || s.generation != gen || s.ctx.Err() != nil {
			s.mu.Unlock()
			continue
		}
		f = s.buffer.Pop()
		if f == nil {
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
		pcm := append([]byte(nil), f.Data()...)
		gain := s.player.Gain()
		for i := 0; i < len(pcm); i += 2 {
			v := int16(binary.LittleEndian.Uint16(pcm[i:]))
			binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(math.Round(float64(v)*gain))))
		}
		if _, err := output.Write(pcm); err != nil {
			s.mu.Lock()
			stale := s.generation != gen || !s.playing
			s.mu.Unlock()
			if stale {
				continue
			}
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
