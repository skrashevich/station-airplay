package stationairplay

import (
	"context"
	"fmt"
	"github.com/skrashevich/station-airplay/audio"
	"math"
)

// Control sends a media command to the current sender, without disabling the
// receiver. Source commands require a working encrypted event channel.
func (r *Receiver) Control(ctx context.Context, action string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	current := r.current
	if current == nil || r.state != StateRunning {
		r.mu.Unlock()
		return ErrNotRunning
	}
	control, ok := current.service.(interface {
		Control(context.Context, string) error
	})
	r.mu.Unlock()
	if !ok {
		return ErrControlUnsupported
	}
	return control.Control(ctx, action)
}

// SetVolume changes only this receiver's PCM gain in dB (-144 mutes, 0 max).
func (r *Receiver) SetVolume(ctx context.Context, db float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validVolume(db) {
		return fmt.Errorf("volume must be between -144 and 0 dB")
	}
	r.mu.Lock()
	current := r.current
	if current == nil || r.state != StateRunning {
		r.mu.Unlock()
		return ErrNotRunning
	}
	control, ok := current.service.(interface {
		SetVolume(context.Context, float64) error
	})
	r.mu.Unlock()
	if !ok {
		return ErrControlUnsupported
	}
	return control.SetVolume(ctx, db)
}

func validVolume(db float64) bool {
	return db >= -144 && db <= 0 && !math.IsNaN(db) && !math.IsInf(db, 0)
}

func (s *protocolService) Control(ctx context.Context, action string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	owner := s.player.Current()
	if owner == nil {
		return ErrNoSender
	}
	switch action {
	case "volume_up", "volume_down":
		db := s.player.Volume()
		if action == "volume_up" {
			db = min(0, max(-60, db)+3)
		} else {
			db -= 3
			if db < -60 {
				db = -144
			}
		}
		return s.setOwnerVolume(ctx, owner, db)
	case "pause", "resume", "stop", "next", "previous", "restart":
		if owner.RemoteControl == nil {
			return ErrControlUnsupported
		}
		local, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(owner.Context(), cancel)
		defer stop()
		defer cancel()
		if owner.Context().Err() != nil {
			return ErrNoSender
		}
		return owner.RemoteControl(local, action)
	default:
		return ErrControlUnsupported
	}
}

func (s *protocolService) SetVolume(ctx context.Context, db float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validVolume(db) {
		return fmt.Errorf("invalid volume")
	}
	return s.setOwnerVolume(ctx, s.player.Current(), db)
}
func (s *protocolService) setOwnerVolume(ctx context.Context, owner *audio.Server, db float64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if owner == nil || !s.player.SetVolumeFor(owner, db) {
		return ErrNoSender
	}
	// Phone slider notification is advisory; PCM gain is already committed.
	if owner.NotifyVolume != nil {
		_ = owner.NotifyVolume(ctx, db)
	}
	return nil
}
