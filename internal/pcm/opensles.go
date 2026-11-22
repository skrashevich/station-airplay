//go:build (linux && (arm || arm64 || amd64)) || darwin

package pcm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

const slBuffers = 3
const slBufferSize = 4096

type slQueueState struct{ Count, Index uint32 }

type openSLDevice struct {
	memory  []byte // Native mmap storage, retained until after the player is destroyed.
	next    int
	running bool
	failure error
	state   func(*slQueueState) uint32
	enqueue func([]byte) uint32
	play    func(uint32) uint32
	clear   func() uint32
}

func (d *openSLDevice) queueCount() (uint32, int) {
	var state slQueueState
	if code := d.state(&state); code != 0 {
		d.failure = slError("inspect queue", code)
		return 0, -int(syscall.EIO)
	}
	if state.Count > slBuffers {
		d.failure = errors.New("OpenSL ES returned an invalid queue count")
		return 0, -int(syscall.EIO)
	}
	return state.Count, 0
}

func (d *openSLDevice) write(data []byte) int {
	count, code := d.queueCount()
	if code < 0 {
		return code
	}
	if count == slBuffers {
		return -int(syscall.EAGAIN)
	}
	length := min(len(data), slBufferSize)
	buffer := d.memory[d.next*slBufferSize : d.next*slBufferSize+length]
	copy(buffer, data[:length])
	if code := d.enqueue(buffer); code != 0 {
		d.failure = slError("enqueue PCM", code)
		return -int(syscall.EIO)
	}
	d.next = (d.next + 1) % slBuffers
	if !d.running && count+1 >= 2 {
		if code := d.start(); code < 0 {
			return code
		}
	}
	return length / 4
}

func (d *openSLDevice) start() int {
	if code := d.play(3); code != 0 { // SL_PLAYSTATE_PLAYING
		d.failure = slError("start playback", code)
		return -int(syscall.EIO)
	}
	d.running = true
	return 0
}
func (d *openSLDevice) wait() int {
	// No native callback enters the Go runtime. OpenSL owns its mixer thread;
	// this isolated helper polls consumption and keeps every wait cancellable.
	time.Sleep(5 * time.Millisecond)
	_, code := d.queueCount()
	return code
}
func (d *openSLDevice) recover(code int) int { return code }
func (d *openSLDevice) drain() int {
	count, code := d.queueCount()
	if code < 0 || count == 0 {
		return code
	}
	if !d.running {
		if code := d.start(); code < 0 {
			return code
		}
	}
	return -int(syscall.EAGAIN)
}
func (d *openSLDevice) drop() {
	if d.play != nil {
		d.play(1) // SL_PLAYSTATE_STOPPED, before discarding queued buffers.
	}
	if d.clear != nil {
		d.clear()
	}
	d.running = false
}
func (d *openSLDevice) err(code int) error {
	if d.failure != nil {
		return d.failure
	}
	return syscall.Errno(-code)
}

func androidPCM() bool {
	switch os.Getenv("STATION_PCM_BACKEND") {
	case "opensl":
		return true
	case "alsa":
		return false
	}
	_, library := os.Stat("/system/lib64/libOpenSLES.so")
	_, linker := os.Stat("/system/bin/linker64")
	return library == nil && linker == nil
}

func checkOpenSL() error {
	a, err := loadOpenSL()
	if err != nil {
		return err
	}
	return purego.Dlclose(a.library)
}

func slError(step string, result uint32) error {
	return fmt.Errorf("OpenSL ES %s failed (result %d)", step, result)
}

func playOpenSL(ctx context.Context, input io.Reader, cfg Config) error {
	if cfg.Rate != 44100 && cfg.Rate != 48000 {
		return errors.New("PCM rate must be 44100 or 48000 Hz")
	}
	if cfg.Device != "" && cfg.Device != "default" {
		return errors.New("Android PCM supports only the default system mixer")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a, err := loadOpenSL()
	if err != nil {
		return err
	}
	defer purego.Dlclose(a.library)
	memory, err := unix.Mmap(-1, 0, slBuffers*slBufferSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
	if err != nil {
		return fmt.Errorf("allocate native PCM: %w", err)
	}
	defer unix.Munmap(memory)

	var engine, mixer, player slObject
	if code := a.create(&engine, 0, 0, 0, nil, nil); code != 0 {
		return slError("create engine", code)
	}
	defer slDestroy(engine)
	if code := slRealize(engine); code != 0 {
		return slError("realize engine", code)
	}
	engineAPI, code := slGetInterface[slEngineMethods](engine, a.engineID)
	if code != 0 {
		return slError("engine interface", code)
	}
	createMixer := slBind[func(slEngine, *slObject, uint32, *slID, *uint32) uint32](engineAPI.methods.createOutputMix)
	if code := createMixer(engineAPI, &mixer, 0, nil, nil); code != 0 {
		return slError("create mixer", code)
	}
	defer slDestroy(mixer)
	if code := slRealize(mixer); code != 0 {
		return slError("realize mixer", code)
	}

	// The source and sink point into this single allocation or into OpenSL.
	// Pin the whole descriptor graph until the player has been destroyed.
	desc := newSLDescriptors(cfg.Rate, mixer, a.queueID)
	var pinned runtime.Pinner
	pinned.Pin(desc)
	defer pinned.Unpin()
	createPlayer := slBind[func(slEngine, *slObject, *slDataSource, *slDataSink, uint32, *slID, *uint32) uint32](engineAPI.methods.createAudioPlayer)
	if code := createPlayer(engineAPI, &player, &desc.source, &desc.sink, 1, &desc.queueID, &desc.required); code != 0 {
		return slError("create player", code)
	}
	defer slDestroy(player)
	if code := slRealize(player); code != 0 {
		return slError("realize player", code)
	}
	play, code := slGetInterface[slPlayMethods](player, a.playID)
	if code != 0 {
		return slError("play interface", code)
	}
	queue, code := slGetInterface[slQueueMethods](player, a.queueID)
	if code != 0 {
		return slError("queue interface", code)
	}

	setState := slBind[func(slPlay, uint32) uint32](play.methods.setPlayState)
	getState := slBind[func(slQueue, *slQueueState) uint32](queue.methods.getState)
	enqueue := slBind[func(slQueue, []byte, uint32) uint32](queue.methods.enqueue)
	clearQueue := slBind[func(slQueue) uint32](queue.methods.clear)
	d := &openSLDevice{
		memory:  memory,
		play:    func(state uint32) uint32 { return setState(play, state) },
		state:   func(state *slQueueState) uint32 { return getState(queue, state) },
		enqueue: func(data []byte) uint32 { return enqueue(queue, data, uint32(len(data))) },
		clear:   func() uint32 { return clearQueue(queue) },
	}
	defer d.drop()
	return transfer(ctx, input, d)
}
