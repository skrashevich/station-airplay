// The shared player grants one session ownership; it never writes global ALSA controls.
package audio

import (
	"context"
	"fmt"
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

type Player struct {
	Context     context.Context
	Output      codec.OutputOptions
	Clock       *ptp.VirtualClock
	OutputDelay time.Duration
	BindIP      string
	mu          sync.Mutex
	owner       *Server
	gain        atomic.Uint64
	volume      atomic.Uint64
}

func NewPlayer(ctx context.Context, clock *ptp.VirtualClock, opts codec.OutputOptions) *Player {
	p := &Player{Context: ctx, Clock: clock, Output: opts, OutputDelay: 160 * time.Millisecond}
	p.SetVolume(-20)
	return p
}
func (p *Player) claim(s *Server) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owner != nil && p.owner != s {
		return fmt.Errorf("another sender owns audio")
	}
	p.owner = s
	return nil
}
func (p *Player) release(s *Server) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owner == s {
		p.owner = nil
	}
}
func (p *Player) SetVolume(db float64) {
	p.volume.Store(math.Float64bits(db))
	p.gain.Store(math.Float64bits(codec.LinearVolume(db)))
}
func (p *Player) Gain() float64 { return math.Float64frombits(p.gain.Load()) }

func (p *Player) Volume() float64 { return math.Float64frombits(p.volume.Load()) }
