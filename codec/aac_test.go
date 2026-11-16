package codec

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"testing"
)

func TestAACEmptyAccessUnitPreservesHistory(t *testing.T) {
	data, err := os.ReadFile("testdata/tone.aac")
	if err != nil {
		t.Fatal(err)
	}
	var frames [][]byte
	for len(data) > 0 {
		if len(data) < 7 {
			t.Fatal("fixture truncated")
		}
		n := int(data[3]&3)<<11 | int(data[4])<<3 | int(data[5]>>5)
		if n < 7 || n > len(data) {
			t.Fatal("invalid fixture frame length")
		}
		frames = append(frames, data[7:n])
		data = data[n:]
	}
	if len(frames) < 2 {
		t.Fatal("fixture needs multiple frames")
	}
	baseline := NewAacDecoder()
	if err := baseline.InitRaw([]byte{0x12, 0x10}); err != nil {
		t.Fatal(err)
	}
	want := make([][]byte, len(frames))
	for i, frame := range frames {
		want[i], err = baseline.Decode(frame)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Ensure this fixture detects a decoder reset that loses overlap history.
	fresh := NewAacDecoder()
	if err := fresh.InitRaw([]byte{0x12, 0x10}); err != nil {
		t.Fatal(err)
	}
	withoutHistory, err := fresh.Decode(frames[1])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(withoutHistory, want[1]) {
		t.Fatal("fixture does not exercise overlap history")
	}
	for _, empty := range []struct {
		name string
		data []byte
	}{{"nil", nil}, {"empty", []byte{}}} {
		t.Run(empty.name, func(t *testing.T) {
			d := NewAacDecoder()
			if err := d.InitRaw([]byte{0x12, 0x10}); err != nil {
				t.Fatal(err)
			}
			for i, frame := range frames {
				pcm, err := d.Decode(empty.data)
				if err != nil || len(pcm) != 0 {
					t.Fatalf("empty AU before frame %d: PCM bytes=%d, error=%v", i, len(pcm), err)
				}
				pcm, err = d.Decode(frame)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(pcm, want[i]) {
					t.Fatalf("empty AU changed PCM at frame %d", i)
				}
			}
		})
	}
}

func TestAACEmptyAccessUnitRequiresConfig(t *testing.T) {
	if _, err := NewAacDecoder().Decode(nil); err == nil {
		t.Fatal("unconfigured decoder accepted an empty AU")
	}
}

func TestAACRejectsNonemptyTruncatedAccessUnit(t *testing.T) {
	d := NewAacDecoder()
	if err := d.InitRaw([]byte{0x12, 0x10}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Decode([]byte{0}); err == nil {
		t.Fatal("accepted a nonempty truncated AU")
	}
}

// The fixture is generated audio, independently decoded by FFmpeg for comparison.
func TestAACRealStereoFixture(t *testing.T) {
	data, _ := os.ReadFile("testdata/tone.aac")
	reference, _ := os.ReadFile("testdata/tone.pcm")
	d := NewAacDecoder()
	if err := d.InitRaw([]byte{0x12, 0x10}); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for len(data) > 0 {
		if len(data) < 7 {
			t.Fatal("fixture truncated")
		}
		n := int(data[3]&3)<<11 | int(data[4])<<3 | int(data[5]>>5)
		pcm, err := d.Decode(data[7:n])
		if err != nil {
			t.Fatal(err)
		}
		if len(pcm) != 4096 {
			t.Fatalf("frame bytes=%d", len(pcm))
		}
		got = append(got, pcm...)
		data = data[n:]
	}
	if len(got) != len(reference) {
		t.Fatalf("length %d != reference %d", len(got), len(reference))
	}
	var xy, xx, yy float64
	for i := 0; i < len(got); i += 2 {
		x := float64(int16(binary.LittleEndian.Uint16(got[i:])))
		y := float64(int16(binary.LittleEndian.Uint16(reference[i:])))
		xy += x * y
		xx += x * x
		yy += y * y
	}
	correlation := xy / math.Sqrt(xx*yy)
	if correlation < 0.99 {
		t.Fatalf("correlation with independent decoder=%f", correlation)
	}
	ratio := math.Sqrt(xx / yy)
	if ratio < 0.95 || ratio > 1.05 {
		t.Fatalf("amplitude ratio=%f", ratio)
	}
}

func TestAACRejectsUnsupportedConfig(t *testing.T) {
	for _, asc := range [][]byte{nil, {0x12}, {0x0a, 0x10}, {0x12, 0x08}} {
		if err := NewAacDecoder().InitRaw(asc); err == nil {
			t.Errorf("accepted ASC %x", asc)
		}
	}
}

func TestSoftwareVolume(t *testing.T) {
	samples := []int16{10000, -10000, 32767, -32768}
	ApplyGain(samples, 0.5)
	want := []int16{5000, -5000, 16384, -16384}
	for i := range samples {
		if samples[i] != want[i] {
			t.Fatalf("sample %d: %d != %d", i, samples[i], want[i])
		}
	}
	for _, db := range []float64{-144, math.NaN(), math.Inf(1)} {
		if LinearVolume(db) != 0 {
			t.Fatalf("unsafe gain for %v", db)
		}
	}
	if math.Abs(LinearVolume(-20)-0.1) > 1e-9 {
		t.Fatal("incorrect dB mapping")
	}
	if LinearVolume(30) != 1 {
		t.Fatal("gain must be capped")
	}
}
