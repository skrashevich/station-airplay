package aliceipc

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"
)

func fromHex(t *testing.T, value string) []byte {
	t.Helper()
	data, err := hex.DecodeString(strings.ReplaceAll(value, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCancelWire(t *testing.T) {
	for _, tc := range []struct {
		method int
		want   string
	}{
		{1, "1c000000 e20f15 3a0f416c6963654361706162696c697479 42020801 e80fd201"},
		{6, "1c000000 e20f15 3a0f416c6963654361706162696c697479 42020806 e80fd201"},
	} {
		if got := cancelFrame(tc.method); !bytes.Equal(got, fromHex(t, tc.want)) {
			t.Errorf("method %d: got %x", tc.method, got)
		}
	}
}

func TestDecodeCapturedShape(t *testing.T) {
	// Independently encoded legacy BUSY state with "пауза" and request id "r1".
	state := fromHex(t, "0803 120ad0bfd0b0d183d0b7d0b0 3a027231")
	legacy := append(fromHex(t, "e20f12"), state...)
	legacy = append(legacy, fromHex(t, "e80f03")...)
	e, ok, err := decodeEvent(legacy)
	if err != nil || !ok || e.state != 3 || e.phrase != "пауза" || e.requestID != "r1" || e.finalPresent {
		t.Fatalf("legacy: %+v %t %v", e, ok, err)
	}
	// Remoting AliceCapability.STATE_CHANGED wraps the same AliceState at field2.
	remote := fromHex(t, "3a0f416c6963654361706162696c697479 920114 1212")
	remote = append(remote, state...)
	wrapped := append(fromHex(t, "e20f28"), remote...)
	wrapped = append(wrapped, fromHex(t, "e80fd201")...)
	e, ok, err = decodeEvent(wrapped)
	if err != nil || !ok || e.phrase != "пауза" || e.requestID != "r1" {
		t.Fatalf("remoting: %+v %t %v", e, ok, err)
	}
	// RequestState.final=true supplies the recognized text when top-level text is absent.
	final := fromHex(t, "e20f12 0803 7a0e 0a0ad0bfd0b0d183d0b7d0b0 1001 e80f03")
	e, ok, err = decodeEvent(final)
	if err != nil || !ok || !e.final || !e.finalPresent || e.phrase != "пауза" {
		t.Fatalf("final: %+v %t %v", e, ok, err)
	}
}

func TestStrictCommands(t *testing.T) {
	for text, want := range map[string]string{
		"Алиса, поставь музыку на паузу, пожалуйста!": "pause",
		"Пауза": "pause", "продолжай": "resume", "возобнови музыку": "resume",
		"хватит": "pause", "стоп": "pause", "выключи музыку": "pause",
		"не ставь на паузу": "", "как поставить на паузу": "", "пауза и выключи свет": "",
		"включи музыку": "", "расскажи про паузу": "", "пауза в спальне": "",
	} {
		_, got := command(text)
		if got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
}

func TestTrackerRequiresCurrentFinalCycle(t *testing.T) {
	now := time.Now()
	s := tracker{state: -1}
	steps := []struct {
		e    event
		want string
	}{
		{event{statePresent: true, state: 3, phrase: "пауза", final: true}, ""},     // cached startup
		{event{statePresent: true, state: 1, requestID: "r1", phrase: "пауза"}, ""}, // partial
		{event{statePresent: true, state: 3, requestID: "r1", phrase: "пауза", finalPresent: true}, ""},
		{event{statePresent: true, state: 3, requestID: "r1", finalPresent: true, final: true}, "pause"},
		{event{statePresent: true, state: 3, requestID: "r1", phrase: "пауза"}, ""}, // duplicate
		{event{statePresent: true, state: 0}, ""},
		{event{statePresent: true, state: 1, requestID: "r1"}, ""},
		{event{statePresent: true, state: 3, requestID: "r1", phrase: "пауза"}, ""}, // repeat IPC cycle
		{event{statePresent: true, state: 1, requestID: "r2"}, ""},
		{event{statePresent: true, state: 3, requestID: "r2", phrase: "продолжай"}, "resume"},
	}
	for i, step := range steps {
		_, got := s.accept(step.e, now)
		if got != step.want {
			t.Fatalf("step %d got %q want %q", i, got, step.want)
		}
	}
}

func TestMalformedWire(t *testing.T) {
	for _, raw := range []string{"e20fffff", "00", "ffffffffffffffffffff02", "e20f04 0803", "e20f03 080380 e80f03", "e20f03 0a0103 e80f03"} {
		if _, _, err := decodeEvent(fromHex(t, raw)); err == nil {
			t.Errorf("accepted malformed %s", raw)
		}
	}
	for _, header := range [][]byte{{0, 0, 0, 0}, {1, 0, 16, 0}, {5, 0, 0, 0, 1}} {
		if _, err := readFrame(bytes.NewReader(header)); err == nil {
			t.Errorf("accepted bad frame %x", header)
		}
	}
}

func TestSparsePhraseDoesNotFinalizeListening(t *testing.T) {
	s := tracker{state: -1}
	now := time.Now()
	_, _ = s.accept(event{statePresent: true, state: 1, requestID: "sparse"}, now)
	// No state field: this is not IDLE and must not complete the utterance.
	e, ok, err := decodeEvent(fromHex(t, "e20f0c 120ad0bfd0b0d183d0b7d0b0 e80f03"))
	if err != nil || !ok || e.statePresent {
		t.Fatalf("sparse: %+v %t %v", e, ok, err)
	}
	if _, action := s.accept(e, now); action != "" || s.state != 1 {
		t.Fatalf("sparse partial became action=%q state=%d", action, s.state)
	}
	// An explicit final marker can complete a sparse notification.
	e.final, e.finalPresent = true, true
	if _, action := s.accept(e, now); action != "pause" {
		t.Fatalf("sparse final action=%q", action)
	}
}

func TestFinalMetadataCannotDowngradeToLegacyFallback(t *testing.T) {
	s := tracker{state: -1}
	now := time.Now()
	_, _ = s.accept(event{statePresent: true, state: 1, requestID: "metadata"}, now)
	if _, action := s.accept(event{statePresent: true, state: 3, phrase: "пауза", finalPresent: true}, now); action != "" {
		t.Fatal("explicit non-final phrase accepted")
	}
	if _, action := s.accept(event{statePresent: true, state: 3, phrase: "пауза"}, now); action != "" {
		t.Fatal("sparse metadata omission downgraded readiness")
	}
	if _, action := s.accept(event{finalPresent: true, final: true}, now); action != "pause" {
		t.Fatalf("explicit final did not complete: %q", action)
	}
}

func writeFixture(t *testing.T, conn net.Conn, raw string) {
	t.Helper()
	data := fromHex(t, raw)
	frame := binary.LittleEndian.AppendUint32(nil, uint32(len(data)))
	if _, err := conn.Write(append(frame, data...)); err != nil {
		t.Fatal(err)
	}
}

func TestRunSelectiveCancelAndShutdown(t *testing.T) {
	for _, tc := range []struct {
		method int
		native bool
	}{{1, false}, {6, false}, {1, true}, {6, true}} {
		method := tc.method
		t.Run(fmt.Sprintf("method=%d/native=%t", method, tc.native), func(t *testing.T) {
			write := func(conn net.Conn, raw string) {
				t.Helper()
				if !tc.native {
					writeFixture(t, conn, raw)
					return
				}
				outer, err := fields(fromHex(t, raw))
				if err != nil {
					t.Fatal(err)
				}
				data := bytesField(nil, outer[253].n, outer[252].data)
				frame := binary.LittleEndian.AppendUint32(nil, uint32(len(data)))
				if _, err := conn.Write(append(frame, data...)); err != nil {
					t.Fatal(err)
				}
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := make(chan string, 4)
			done := make(chan error, 1)
			go func() {
				done <- Run(ctx, Config{Address: listener.Addr().String(), CancelMethod: method, Logger: log.New(io.Discard, "", 0)}, func() bool { return true }, func(ctx context.Context, action string) error {
					calls <- action
					return nil
				})
			}()
			conn, err := listener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			// Startup phrase and unrelated complete command must not cancel Alice.
			write(conn, "e20f0e 0803 120ad0bfd0b0d183d0b7d0b0 e80f03")
			write(conn, "e20f02 0801 e80f03")
			write(conn, "e20f06 0803 12026869 e80f03")
			if err := conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if _, err := readFrame(conn); err == nil {
				t.Fatal("unrelated request cancelled Alice")
			}
			write(conn, "e20f02 0801 e80f03")
			write(conn, "e20f0e 0803 120ad0bfd0b0d183d0b7d0b0 e80f03")
			write(conn, "e20f0e 0803 120ad0bfd0b0d183d0b7d0b0 e80f03")
			if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			out, err := readFrame(conn)
			want := cancelFrame(method)
			if tc.native {
				want = nativeCancelFrame(method)
			}
			if err != nil || !bytes.Equal(out, want[4:]) {
				t.Fatalf("cancel=%x err=%v", out, err)
			}
			select {
			case action := <-calls:
				if action != "pause" {
					t.Fatal(action)
				}
			case <-time.After(time.Second):
				t.Fatal("media action missing")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("listener failed to stop")
			}
			if len(calls) != 0 {
				t.Fatal("duplicate media action")
			}
		})
	}
}

func TestDisconnectedSenderDoesNotCancel(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	jobs := make(chan job, 1)
	done := make(chan error, 1)
	go func() {
		done <- listen(ctx, server, 1, log.New(io.Discard, "", 0), func() bool { return false }, jobs)
	}()
	writeFixture(t, client, "e20f02 0801 e80f03")
	writeFixture(t, client, "e20f0e 0803 120ad0bfd0b0d183d0b7d0b0 e80f03")
	_ = client.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := readFrame(client); err == nil {
		t.Fatal("cancel with no sender")
	}
	_ = client.Close()
	_ = server.Close()
	<-done
	if len(jobs) != 0 {
		t.Fatal("queued media with no sender")
	}
}

func TestFinalFromAnotherRequestCannotControlCurrentSender(t *testing.T) {
	now := time.Now()
	s := tracker{state: -1}
	_, _ = s.accept(event{statePresent: true, state: 1, requestID: "current", phrase: "пауза", finalPresent: true}, now)
	// Delayed notifications from an older request must not finalize the current
	// partial phrase or overwrite its state/metadata.
	if _, action := s.accept(event{statePresent: true, state: 3, requestID: "older", finalPresent: true, final: true}, now); action != "" {
		t.Fatalf("unrelated final executed the current partial phrase: %q", action)
	}
	if s.state != 1 || s.requestID != "current" {
		t.Fatalf("unrelated request overwrote current cycle: %+v", s)
	}
	if _, action := s.accept(event{requestID: "current", phrase: "какая погода", finalPresent: true, final: true}, now); action != "" {
		t.Fatalf("unrelated final affected non-media current request: %q", action)
	}
}
