package codec

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
)

func alacFixture(t *testing.T) (packet, reference []byte) {
	t.Helper()
	var err error
	packet, err = os.ReadFile("testdata/alac-stereo16-44100.packet")
	if err != nil {
		t.Fatal(err)
	}
	reference, err = os.ReadFile("testdata/alac-stereo16-44100.pcm")
	if err != nil {
		t.Fatal(err)
	}
	return packet, reference
}

func TestALACIndependentStereoFixture(t *testing.T) {
	packet, reference := alacFixture(t)
	d, err := NewAlacDecoder()
	if err != nil {
		t.Fatal(err)
	}
	if d.SampleRate() != 44100 {
		t.Fatalf("sample rate=%d", d.SampleRate())
	}
	pcm, err := d.Decode(packet)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcm) != 352*4 || !bytes.Equal(pcm, reference) {
		t.Fatalf("ALAC output differs from independent PCM: %d bytes", len(pcm))
	}
	for i := range 352 {
		left := int16(binary.LittleEndian.Uint16(pcm[i*4:]))
		right := int16(binary.LittleEndian.Uint16(pcm[i*4+2:]))
		if left != int16((i*97%32768)-16384) || right != int16(12000-i*53) {
			t.Fatalf("frame %d: L=%d R=%d", i, left, right)
		}
	}
	// Software gain may mutate a returned frame. Decoding another frame must
	// neither retain that mutation nor overwrite earlier returned samples.
	pcm[0] ^= 0xff
	next, err := d.Decode(packet)
	if err != nil || !bytes.Equal(next, reference) || pcm[0] != reference[0]^0xff {
		t.Fatalf("frame storage reused: err=%v", err)
	}
}

func TestALACRejectsMalformedPackets(t *testing.T) {
	packet, reference := alacFixture(t)
	d, err := NewAlacDecoder()
	if err != nil {
		t.Fatal(err)
	}
	// The explicit-count escape headers below declare zero and 353 samples.
	for _, data := range [][]byte{
		nil, {0xff}, {0x40}, packet[:3], packet[:len(packet)/2],
		{0x20, 0, 0x12, 0, 0, 0, 0},
		{0x20, 0, 0x12, 0, 0, 2, 0xc2},
	} {
		if pcm, err := d.Decode(data); err == nil || len(pcm) != 0 {
			t.Errorf("accepted malformed packet of %d bytes: pcm=%d err=%v", len(data), len(pcm), err)
		}
	}
	// A bad sender frame must not poison the decoder for the next valid frame.
	pcm, err := d.Decode(packet)
	if err != nil || !bytes.Equal(pcm, reference) {
		t.Fatalf("recovery after malformed packets: pcm=%d err=%v", len(pcm), err)
	}
}

func TestALACUnconfiguredDecoder(t *testing.T) {
	var d AlacDecoder
	if pcm, err := d.Decode([]byte{1, 2, 3}); err == nil || pcm != nil {
		t.Fatalf("unconfigured decoder returned pcm=%x err=%v", pcm, err)
	}
}
