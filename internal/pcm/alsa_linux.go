//go:build linux && (arm || arm64 || amd64)

package pcm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Before glibc 2.34, pthread symbols used by purego live in libpthread
// rather than libc. Keep that library in the helper's DT_NEEDED entries.
//go:cgo_import_dynamic _ _ "libpthread.so.0"

type alsa struct {
	library    uintptr
	open       func(*uintptr, string, int32, int32) int32
	setParams  func(uintptr, int32, int32, uint32, uint32, int32, uint32) int32
	writei     func(uintptr, unsafe.Pointer, uintptr) int
	waitPCM    func(uintptr, int32) int32
	recoverPCM func(uintptr, int32, int32) int32
	drainPCM   func(uintptr) int32
	dropPCM    func(uintptr) int32
	closePCM   func(uintptr) int32
	strerror   func(int32) string
}

// load uses dlsym, whose lookup selects the default symbol version. This avoids
// the legacy ALSA compatibility symbols selected by some unversioned aplay ELFs.
func load() (*alsa, error) {
	paths := []string{os.Getenv("STATION_PCM_LIBRARY")}
	if paths[0] == "" {
		paths = []string{"libasound.so.2", "/system/vendor/quasar/libs/libasound.so.2"}
	}
	var failure error
	for _, path := range paths {
		handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
		if err != nil {
			failure = errors.Join(failure, fmt.Errorf("%s: %w", path, err))
			continue
		}
		a := &alsa{library: handle}
		bindings := []struct {
			name     string
			function any
		}{
			{"snd_pcm_open", &a.open}, {"snd_pcm_set_params", &a.setParams},
			{"snd_pcm_writei", &a.writei}, {"snd_pcm_wait", &a.waitPCM},
			{"snd_pcm_recover", &a.recoverPCM}, {"snd_pcm_drain", &a.drainPCM},
			{"snd_pcm_drop", &a.dropPCM}, {"snd_pcm_close", &a.closePCM},
			{"snd_strerror", &a.strerror},
		}
		for _, binding := range bindings {
			address, err := purego.Dlsym(handle, binding.name)
			if err != nil {
				purego.Dlclose(handle)
				return nil, fmt.Errorf("ALSA function %s: %w", binding.name, err)
			}
			purego.RegisterFunc(binding.function, address)
		}
		return a, nil
	}
	return nil, fmt.Errorf("load ALSA: %w", failure)
}

// Check loads ALSA and verifies required entry points without opening a PCM.
// Vendor library constructors may still run as part of dlopen.
func Check() error {
	a, err := load()
	if err != nil {
		return err
	}
	return purego.Dlclose(a.library)
}

func Play(ctx context.Context, input io.Reader, cfg Config) error {
	if cfg.Rate != 44100 && cfg.Rate != 48000 {
		return errors.New("PCM rate must be 44100 or 48000 Hz")
	}
	if cfg.Device == "" {
		cfg.Device = "default"
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a, err := load()
	if err != nil {
		return err
	}
	defer purego.Dlclose(a.library)
	d := &alsaDevice{api: a}
	// Playback=0, SND_PCM_NONBLOCK=1. Every wait is bounded so cancellation does
	// not have to call ALSA concurrently with a write or close.
	if code := a.open(&d.handle, cfg.Device, 0, 1); code < 0 {
		return fmt.Errorf("open PCM %q: %w", cfg.Device, d.err(int(code)))
	}
	defer a.closePCM(d.handle)
	// S16_LE=2, RW_INTERLEAVED=3, stereo. ALSA's configured plug/dmix chain
	// owns hardware format/rate conversion and mixing with the speaker service.
	if code := a.setParams(d.handle, 2, 3, 2, uint32(cfg.Rate), 1, 100000); code < 0 {
		d.drop()
		return fmt.Errorf("configure PCM %q: %w", cfg.Device, d.err(int(code)))
	}
	return transfer(ctx, input, d)
}

type alsaDevice struct {
	api    *alsa
	handle uintptr
}

func (d *alsaDevice) write(p []byte) int {
	n := d.api.writei(d.handle, unsafe.Pointer(unsafe.SliceData(p)), uintptr(len(p)/4))
	runtime.KeepAlive(p)
	return n
}
func (d *alsaDevice) wait() int { return int(d.api.waitPCM(d.handle, 100)) }
func (d *alsaDevice) recover(code int) int {
	// Native suspended-stream recovery may retry snd_pcm_resume forever. Fail
	// suspended/unrecognized errors promptly so the receiver can stop this process.
	if code != -int(syscall.EPIPE) && code != -int(syscall.EINTR) {
		return code
	}
	return int(d.api.recoverPCM(d.handle, int32(code), 1))
}
func (d *alsaDevice) drain() int { return int(d.api.drainPCM(d.handle)) }
func (d *alsaDevice) drop()      { d.api.dropPCM(d.handle) }
func (d *alsaDevice) err(code int) error {
	return fmt.Errorf("%s: %w", d.api.strerror(int32(code)), syscall.Errno(-code))
}
