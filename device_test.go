package stationairplay

import (
	"testing"

	"github.com/skrashevich/station-airplay/globals"
)

func TestAdvertisedAudioMatchesBothImplementedTransports(t *testing.T) {
	features := airplayDevice().ToUint64()
	for _, bit := range []int{globals.SupportsAirPlayAudio, globals.AudioFormats_1, globals.AudioFormats_2, globals.SupportsBufferedAudio, globals.SupportsPTP} {
		if features&(uint64(1)<<bit) == 0 {
			t.Errorf("missing supported audio feature %d", bit)
		}
	}
	if features&(uint64(1)<<globals.SupportsRFC2198Redundancy) != 0 {
		t.Fatal("advertised unsupported RTP redundancy")
	}
}
