package aliceipc

import (
	"bytes"
	"testing"
)

func TestOrionNativeEvents(t *testing.T) {
	// Independently encoded AliceState and remoting STATE_CHANGED envelopes.
	for _, raw := range []string{
		"1a12 0803 120ad0bfd0b0d183d0b7d0b0 3a027231",
		"920d28 3a0f416c6963654361706162696c697479 920114 1212 0803 120ad0bfd0b0d183d0b7d0b0 3a027231",
	} {
		e, ok, err := decodeEvent(fromHex(t, raw))
		if err != nil || !ok || !e.nativeEnvelope || e.phrase != "пауза" || e.state != 3 || e.requestID != "r1" {
			t.Fatalf("event=%+v ok=%v err=%v", e, ok, err)
		}
	}
}

func TestOrionNativeCancel(t *testing.T) {
	for _, tc := range []struct {
		method int
		want   string
	}{
		{1, "18000000 920d15 3a0f416c6963654361706162696c697479 42020801"},
		{6, "18000000 920d15 3a0f416c6963654361706162696c697479 42020806"},
	} {
		got := nativeCancelFrame(tc.method)
		if !bytes.Equal(got, fromHex(t, tc.want)) {
			t.Errorf("method %d: got %x", tc.method, got)
		}
	}
}
