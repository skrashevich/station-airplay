//go:build (linux && (arm || arm64 || amd64)) || darwin

package pcm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
	"unsafe"

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
	enqueue func(unsafe.Pointer, uint32) uint32
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
	if code := d.enqueue(unsafe.Pointer(unsafe.SliceData(buffer)), uint32(length)); code != 0 {
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

type openSL struct {
	library, create, engineID, playID, queueID uintptr
}

func loadOpenSL() (*openSL, error) {
	path := os.Getenv("STATION_PCM_LIBRARY")
	if path == "" {
		path = "libOpenSLES.so"
	}
	handle, err := purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("load OpenSL ES: %w", err)
	}
	a := &openSL{library: handle}
	for _, symbol := range []struct {
		name string
		out  *uintptr
		data bool
	}{{"slCreateEngine", &a.create, false}, {"SL_IID_ENGINE", &a.engineID, true}, {"SL_IID_PLAY", &a.playID, true}, {"SL_IID_ANDROIDSIMPLEBUFFERQUEUE", &a.queueID, true}} {
		address, err := purego.Dlsym(handle, symbol.name)
		if err != nil {
			purego.Dlclose(handle)
			return nil, fmt.Errorf("OpenSL ES symbol %s: %w", symbol.name, err)
		}
		if symbol.data {
			address = *(*uintptr)(nativePointer(address))
		}
		*symbol.out = address
	}
	return a, nil
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

// Native interface/data addresses refer to dlopen mappings or mmap storage,
// never to Go heap objects. Reinterpret the address without pointer arithmetic
// on Go allocations; the owning library/mapping outlives every native call.
func nativePointer(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}

// OpenSL interfaces are pointers to vtable pointers, not vtable pointers.
// Every OpenSL argument/result here is an integer or native pointer (no floats).
func slMethod(object uintptr, slot int, arguments ...uintptr) uint32 {
	table := *(*unsafe.Pointer)(nativePointer(object))
	function := *(*uintptr)(unsafe.Add(table, uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	result, _, _ := purego.SyscallN(function, append([]uintptr{object}, arguments...)...)
	return uint32(result)
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
	// Keep both retained PCM buffers and all ABI descriptors outside the Go
	// heap. OpenSL may read them asynchronously after a purego call returns.
	memory, err := unix.Mmap(-1, 0, slBuffers*slBufferSize+512, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
	if err != nil {
		return fmt.Errorf("allocate native PCM: %w", err)
	}
	defer unix.Munmap(memory)
	address := func(offset int) uintptr {
		return uintptr(unsafe.Pointer(unsafe.SliceData(memory))) + uintptr(slBuffers*slBufferSize+offset)
	}
	word := func(offset int) uintptr { return *(*uintptr)(nativePointer(address(offset))) }
	var engine, mixer, player uintptr
	d := &openSLDevice{memory: memory[:slBuffers*slBufferSize]}
	defer func() {
		d.drop()
		for _, object := range []uintptr{player, mixer, engine} {
			if object != 0 {
				slMethod(object, 6) // SLObjectItf.Destroy, before unmapping buffers.
			}
		}
	}()
	result, _, _ := purego.SyscallN(a.create, address(0), 0, 0, 0, 0, 0)
	if result != 0 {
		return slError("create engine", uint32(result))
	}
	engine = word(0)
	if code := slMethod(engine, 0, 0); code != 0 {
		return slError("realize engine", code)
	}
	if code := slMethod(engine, 3, a.engineID, address(0)); code != 0 {
		return slError("engine interface", code)
	}
	engineAPI := word(0)
	if code := slMethod(engineAPI, 7, address(0), 0, 0, 0); code != 0 {
		return slError("create mixer", code)
	}
	mixer = word(0)
	if code := slMethod(mixer, 0, 0); code != 0 {
		return slError("realize mixer", code)
	}
	*(*[2]uint32)(nativePointer(address(32))) = [2]uint32{0x800007bd, slBuffers}
	*(*[7]uint32)(nativePointer(address(48))) = [7]uint32{2, 2, uint32(cfg.Rate) * 1000, 16, 16, 3, 2}
	*(*struct {
		kind uint32
		mix  uintptr
	})(nativePointer(address(80))) = struct {
		kind uint32
		mix  uintptr
	}{4, mixer}
	*(*[2]uintptr)(nativePointer(address(96))) = [2]uintptr{address(32), address(48)}
	*(*[2]uintptr)(nativePointer(address(112))) = [2]uintptr{address(80), 0}
	*(*uintptr)(nativePointer(address(128))) = a.queueID
	*(*uint32)(nativePointer(address(144))) = 1
	if code := slMethod(engineAPI, 2, address(0), address(96), address(112), 1, address(128), address(144)); code != 0 {
		return slError("create player", code)
	}
	player = word(0)
	if code := slMethod(player, 0, 0); code != 0 {
		return slError("realize player", code)
	}
	if code := slMethod(player, 3, a.playID, address(0)); code != 0 {
		return slError("play interface", code)
	}
	play := word(0)
	d.play = func(state uint32) uint32 { return slMethod(play, 0, uintptr(state)) }
	if code := slMethod(player, 3, a.queueID, address(0)); code != 0 {
		return slError("queue interface", code)
	}
	queue := word(0)
	d.state = func(state *slQueueState) uint32 {
		code := slMethod(queue, 2, address(16))
		*state = *(*slQueueState)(nativePointer(address(16)))
		return code
	}
	d.enqueue = func(data unsafe.Pointer, length uint32) uint32 {
		return slMethod(queue, 0, uintptr(data), uintptr(length))
	}
	d.clear = func() uint32 { return slMethod(queue, 1) }
	return transfer(ctx, input, d)
}
