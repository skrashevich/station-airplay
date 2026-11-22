//go:build (linux && (arm || arm64 || amd64)) || darwin

package pcm

import (
	"fmt"
	"os"

	"github.com/ebitengine/purego"
)

// OpenSLES.h and OpenSLES_Android.h define each interface as a pointer to
// a vtable pointer. Preserve the unused slots preceding the methods we call.
type slInterface[T any] struct{ methods *T }
type slObject = *slInterface[slObjectMethods]
type slEngine = *slInterface[slEngineMethods]
type slPlay = *slInterface[slPlayMethods]
type slQueue = *slInterface[slQueueMethods]
type slID *byte // opaque SLInterfaceID owned by libOpenSLES

type slObjectMethods struct {
	realize      uintptr
	_            [2]uintptr // Resume, GetState
	getInterface uintptr
	_            [2]uintptr // RegisterCallback, AbortAsyncOperation
	destroy      uintptr
}

type slEngineMethods struct {
	_                 [2]uintptr // CreateLEDDevice, CreateVibraDevice
	createAudioPlayer uintptr
	_                 [4]uintptr // CreateAudioRecorder, CreateMidiPlayer, CreateListener, Create3DGroup
	createOutputMix   uintptr
}

type slPlayMethods struct{ setPlayState uintptr }
type slQueueMethods struct{ enqueue, clear, getState uintptr }

type slBufferQueueLocator struct{ kind, buffers uint32 }
type slPCMFormat struct{ kind, channels, rateMilliHz, sampleBits, containerBits, channelMask, endianness uint32 }
type slOutputMixLocator struct {
	kind  uint32
	mixer slObject
}
type slDataSource struct {
	locator *slBufferQueueLocator
	format  *slPCMFormat
}
type slDataSink struct {
	locator *slOutputMixLocator
	format  *slPCMFormat
}
type slDescriptors struct {
	queue    slBufferQueueLocator
	pcm      slPCMFormat
	output   slOutputMixLocator
	source   slDataSource
	sink     slDataSink
	queueID  slID
	required uint32
}

func newSLDescriptors(rate int, mixer slObject, queueID slID) *slDescriptors {
	d := &slDescriptors{
		queue: slBufferQueueLocator{kind: 0x800007bd, buffers: slBuffers}, // ANDROIDSIMPLEBUFFERQUEUE
		pcm: slPCMFormat{kind: 2, channels: 2, rateMilliHz: uint32(rate) * 1000,
			sampleBits: 16, containerBits: 16, channelMask: 3, endianness: 2}, // stereo S16_LE
		output:   slOutputMixLocator{kind: 4, mixer: mixer}, // OUTPUTMIX
		queueID:  queueID,
		required: 1,
	}
	d.source = slDataSource{locator: &d.queue, format: &d.pcm}
	d.sink = slDataSink{locator: &d.output}
	return d
}

func slBind[F any](address uintptr) F {
	var fn F
	purego.RegisterFunc(&fn, address)
	return fn
}

func slRealize(object slObject) uint32 {
	realize := slBind[func(slObject, uint32) uint32](object.methods.realize)
	return realize(object, 0) // synchronous
}

func slDestroy(object slObject) {
	destroy := slBind[func(slObject)](object.methods.destroy)
	destroy(object)
}

func slGetInterface[T any](object slObject, id slID) (*slInterface[T], uint32) {
	var result *slInterface[T]
	get := slBind[func(slObject, slID, **slInterface[T]) uint32](object.methods.getInterface)
	code := get(object, id, &result)
	return result, code
}

type openSL struct {
	library                   uintptr
	create                    func(*slObject, uint32, uintptr, uint32, *slID, *uint32) uint32
	engineID, playID, queueID slID
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
	a, err := bindOpenSL(handle)
	if err != nil {
		purego.Dlclose(handle)
	}
	return a, err
}

func bindOpenSL(handle uintptr) (*openSL, error) {
	create, err := purego.Dlsym(handle, "slCreateEngine")
	if err != nil {
		return nil, fmt.Errorf("OpenSL ES symbol slCreateEngine: %w", err)
	}
	a := &openSL{library: handle}
	purego.RegisterFunc(&a.create, create)

	// SL_IID_* exports are data symbols containing a pointer. Bind dlsym
	// with the pointer-to-SLInterfaceID return type for these symbols.
	dlsym, err := purego.Dlsym(purego.RTLD_DEFAULT, "dlsym")
	if err != nil {
		return nil, fmt.Errorf("OpenSL ES resolve dlsym: %w", err)
	}
	lookupID := slBind[func(uintptr, string) *slID](dlsym)
	for _, symbol := range []struct {
		name string
		id   *slID
	}{
		{"SL_IID_ENGINE", &a.engineID},
		{"SL_IID_PLAY", &a.playID},
		{"SL_IID_ANDROIDSIMPLEBUFFERQUEUE", &a.queueID},
	} {
		value := lookupID(handle, symbol.name)
		if value == nil || *value == nil {
			return nil, fmt.Errorf("OpenSL ES missing interface ID %s", symbol.name)
		}
		*symbol.id = *value
	}
	return a, nil
}
