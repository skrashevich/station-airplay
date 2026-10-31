package event

import (
	"bufio"
	"context"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
	"howett.net/plist"
)

// The sender fixture uses the published event key labels and reversed event
// direction directly, independently of the receiver's connection implementation.
func phoneFrame(t *testing.T, c net.Conn, secret []byte, response string, inspect func([]byte)) {
	t.Helper()
	derive := func(label string) []byte {
		k := make([]byte, 32)
		if _, err := io.ReadFull(hkdf.New(sha512.New, secret, []byte("Events-Salt"), []byte(label)), k); err != nil {
			t.Error(err)
		}
		return k
	}
	dec, _ := chacha20poly1305.New(derive("Events-Write-Encryption-Key"))
	enc, _ := chacha20poly1305.New(derive("Events-Read-Encryption-Key"))
	var message []byte
	var count uint64
	for {
		var h [2]byte
		if _, err := io.ReadFull(c, h[:]); err != nil {
			t.Error(err)
			return
		}
		n := int(binary.LittleEndian.Uint16(h[:]))
		data := make([]byte, n+16)
		if _, err := io.ReadFull(c, data); err != nil {
			t.Error(err)
			return
		}
		var nonce [12]byte
		binary.LittleEndian.PutUint64(nonce[4:], count)
		count++
		plain, err := dec.Open(nil, nonce[:], data, h[:])
		if err != nil {
			t.Error(err)
			return
		}
		message = append(message, plain...)
		i := strings.Index(string(message), "\r\n\r\n")
		if i < 0 {
			continue
		}
		head := string(message[:i])
		pos := strings.Index(head, "Content-Length: ")
		if pos < 0 {
			t.Error("missing Content-Length")
			return
		}
		n, err = strconv.Atoi(strings.Split(head[pos+16:], "\r\n")[0])
		if err != nil {
			t.Error(err)
			return
		}
		if len(message) < i+4+n {
			continue
		}
		inspect(message)
		break
	}
	var h [2]byte
	binary.LittleEndian.PutUint16(h[:], uint16(len(response)))
	var nonce [12]byte
	out := append(h[:], enc.Seal(nil, nonce[:], []byte(response), h[:])...)
	if _, err := c.Write(out); err != nil {
		t.Error(err)
	}
}
func connected(t *testing.T) (*Hub, *Session, net.Conn, context.CancelFunc, <-chan error) {
	t.Helper()
	h := NewHub()
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	s, err := h.Register(net.ParseIP("127.0.0.1"), secret)
	if err != nil {
		t.Fatal(err)
	}
	s.SetGroupID("group-test")
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx, l) }()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); s.Close(); cancel() })
	readyDeadline := time.After(time.Second)
	for !s.Available() {
		select {
		case <-time.After(time.Millisecond):
		case <-readyDeadline:
			t.Fatal("not available")
		}
	}
	return h, s, c, cancel, done
}
func TestEncryptedPauseCommand(t *testing.T) {
	h, s, c, cancel, done := connected(t)
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	if replacement, err := h.Register(net.ParseIP("127.0.0.1"), secret); !errors.Is(err, ErrUnavailable) || replacement != nil {
		t.Fatalf("connected registration replaced: session=%v error=%v", replacement, err)
	}
	inspected := make(chan struct{})
	go phoneFrame(t, c, secret, "RTSP/1.0 200 OK\r\nContent-Length: 0\r\n\r\n", func(wire []byte) {
		defer close(inspected)
		parts := strings.SplitN(string(wire), "\r\n\r\n", 2)
		if !strings.HasPrefix(parts[0], "POST /command RTSP/1.0\r\n") {
			t.Errorf("request %s", parts[0])
		}
		var p map[string]any
		if _, err := plist.Unmarshal([]byte(parts[1]), &p); err != nil {
			t.Error(err)
			return
		}
		if p["type"] != "sendMediaRemoteCommand" || p["modernMediaRemoteCommand"] != "1" {
			t.Errorf("wrong command %#v", p)
		}
		params := p["params"].(map[string]any)
		if params["kMRMediaRemoteOptionSendOptionsNumber"] != uint64(0) || params["kMRMediaRemoteOptionIsRedirectingCommand"] != true {
			t.Errorf("params %#v", params)
		}
		var archive map[string]any
		if _, err := plist.Unmarshal(params["kMRMediaRemoteOptionDestinationDeviceUIDs"].([]byte), &archive); err != nil {
			t.Error(err)
			return
		}
		if archive["$archiver"] != "NSKeyedArchiver" || archive["$objects"].([]any)[2] != "group-test" {
			t.Errorf("archive %#v", archive)
		}
	})
	if err := s.Send(t.Context(), "pause"); err != nil {
		t.Fatal(err)
	}
	<-inspected
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
	if s.Available() {
		t.Fatal("available after shutdown")
	}
}
func TestRejectedReply(t *testing.T) {
	_, s, c, _, _ := connected(t)
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	go phoneFrame(t, c, secret, "RTSP/1.0 403 Forbidden\r\n\r\n", func([]byte) {})
	if err := s.Send(t.Context(), "stop"); err == nil {
		t.Fatal("accepted rejected command")
	}
}
func TestCancellationUnblocksRead(t *testing.T) {
	_, s, c, _, _ := connected(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- s.Send(ctx, "pause") }()
	r := bufio.NewReader(c)
	if _, err := r.Peek(1); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel blocked")
	}
	if s.Available() {
		t.Fatal("failed connection stayed available")
	}
}
func TestReplacementOwnership(t *testing.T) {
	h := NewHub()
	ip := net.ParseIP("127.0.0.1")
	secret := make([]byte, 32)
	old, err := h.Register(ip, secret)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	fresh, err := h.Register(ip, secret)
	if err != nil {
		t.Fatal(err)
	}
	old.Close()
	if h.sessions[ip.String()] != fresh {
		t.Fatal("stale close removed replacement")
	}
	fresh.Close()
}
func TestUnavailableAndUnsupported(t *testing.T) {
	h := NewHub()
	s, err := h.Register(net.ParseIP("127.0.0.1"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, action := range []string{"pause", "restart", "volume_up"} {
		if err := s.Send(t.Context(), action); err == nil {
			t.Fatal(fmt.Sprintf("unexpected success: %s", action))
		}
	}
}

func TestAcknowledgmentBounds(t *testing.T) {
	for _, reply := range []string{
		"HTTP/1.1 200 OK\r\n\r\n", "RTSP/1.0 abc Bad\r\n\r\n", "RTSP/1.0 200 OK\r\nContent-Length: -1\r\n\r\n",
		"RTSP/1.0 200 OK\r\nContent-Length: 65537\r\n\r\n", "RTSP/1.0 200 OK\r\nContent-Length: 0\r\nContent-Length: 0\r\n\r\n",
		"RTSP/1.0 200 OK\r\nX: " + strings.Repeat("x", 17000) + "\r\n\r\n", "RTSP/1.0 200 OK\r\nContent-Length: 3\r\n\r\nx",
	} {
		if err := readAcknowledgment(bufio.NewReader(strings.NewReader(reply))); err == nil {
			t.Errorf("accepted invalid reply %.80q", reply)
		}
	}
	if err := readAcknowledgment(bufio.NewReader(strings.NewReader("RTSP/1.1 204 OK\r\nContent-Length: 3\r\n\r\nabc"))); err != nil {
		t.Fatal(err)
	}
}
func TestUnknownPeerAndRegistrationBounds(t *testing.T) {
	h := NewHub()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Serve(ctx, l) }()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("unknown peer remained open: %v", err)
	}
	for i := range maxSessions {
		_, err := h.Register(net.IPv4(10, 0, 0, byte(i+1)), make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.Register(net.IPv4(10, 0, 0, 99), make([]byte, 32)); err == nil {
		t.Fatal("unbounded registrations")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Serve did not shut down")
	}
	if _, err := h.Register(net.IPv4(10, 0, 0, 1), make([]byte, 32)); err == nil {
		t.Fatal("registered after shutdown")
	}
}
func TestActionNumbers(t *testing.T) {
	for action, want := range map[string]string{"resume": "0", "pause": "1", "stop": "3", "next": "4", "previous": "5"} {
		body, err := commandBody(action, "test")
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		if _, err := plist.Unmarshal(body, &p); err != nil {
			t.Fatal(err)
		}
		if p["modernMediaRemoteCommand"] != want {
			t.Errorf("%s=%v want %s", action, p["modernMediaRemoteCommand"], want)
		}
	}
}
func TestInvalidEncryptedRecord(t *testing.T) {
	crypt, err := newEventCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	for _, wire := range [][]byte{{0, 0}, {1, 4}, append([]byte{1, 0}, make([]byte, 17)...)} {
		a, b := net.Pipe()
		done := make(chan struct{})
		go func() { defer close(done); defer b.Close(); b.Write(wire) }()
		c := &encryptedConn{Conn: a, crypt: crypt}
		if _, err := c.Read(make([]byte, 1)); err == nil {
			t.Error("accepted invalid encrypted record")
		}
		a.Close()
		<-done
	}
}

func TestMultipleCommandsKeepNonceCounters(t *testing.T) {
	_, s, c, _, _ := connected(t)
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	crypt, err := newEventCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	crypt.send, crypt.receive = crypt.receive, crypt.send
	phone := &encryptedConn{Conn: c, crypt: crypt}
	done := make(chan error, 1)
	go func() {
		r := bufio.NewReader(phone)
		for range 2 {
			line, err := r.ReadString('\n')
			if err != nil {
				done <- err
				return
			}
			if line != "POST /command RTSP/1.0\r\n" {
				done <- fmt.Errorf("bad request: %s", line)
				return
			}
			length := 0
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					done <- err
					return
				}
				if line == "\r\n" {
					break
				}
				if strings.HasPrefix(line, "Content-Length: ") {
					length, err = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length: ")))
					if err != nil {
						done <- err
						return
					}
				}
			}
			if _, err := io.CopyN(io.Discard, r, int64(length)); err != nil {
				done <- err
				return
			}
			if _, err := phone.Write([]byte("RTSP/1.0 200 OK\r\nContent-Length: 0\r\n\r\n")); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	// Exercise archive serialization at the bounded group ID length.
	s.SetGroupID(strings.Repeat("g", 256))
	for _, action := range []string{"pause", "resume"} {
		if err := s.Send(t.Context(), action); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if crypt.receiveCount != 2 || crypt.sendCount != 2 {
		t.Errorf("nonce counts send=%d receive=%d", crypt.sendCount, crypt.receiveCount)
	}
}
func TestVolumeNotification(t *testing.T) {
	_, s, c, _, _ := connected(t)
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	go phoneFrame(t, c, secret, "RTSP/1.0 200 OK\r\n\r\n", func(wire []byte) {
		parts := strings.SplitN(string(wire), "\r\n\r\n", 2)
		var p map[string]any
		if _, err := plist.Unmarshal([]byte(parts[1]), &p); err != nil {
			t.Error(err)
			return
		}
		if p["type"] != "sendMediaRemoteCommand" || p["value"] != "dvlc" || p["volume"] != 0.25 {
			t.Errorf("volume notification %#v", p)
		}
		if p["params"].(map[string]any)["volume"] != 0.25 {
			t.Error("missing params volume")
		}
	})
	if err := s.SetVolume(t.Context(), 0.25); err != nil {
		t.Fatal(err)
	}
}

func TestEncryptedFragmentation(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	secret := make([]byte, 32)
	send, err := newEventCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	receive, err := newEventCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	receive.send, receive.receive = receive.receive, receive.send
	writer := &encryptedConn{Conn: a, crypt: send}
	reader := &encryptedConn{Conn: b, crypt: receive}
	payload := strings.Repeat("fragment", 1025)
	done := make(chan error, 1)
	go func() { _, err := writer.Write([]byte(payload)); done <- err }()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatal("fragmentation corrupted plaintext")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if send.sendCount != 9 || receive.receiveCount != 9 {
		t.Errorf("record counters send=%d receive=%d", send.sendCount, receive.receiveCount)
	}
}

func TestPendingRegistrationCannotBeReplaced(t *testing.T) {
	h := NewHub()
	ip := net.ParseIP("127.0.0.1")
	secret := make([]byte, 32)
	original, err := h.Register(ip, secret)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	if replacement, err := h.Register(ip, secret); !errors.Is(err, ErrUnavailable) || replacement != nil {
		t.Fatalf("pending registration replaced: session=%v error=%v", replacement, err)
	}
	if h.sessions[ip.String()] != original {
		t.Fatal("original pending registration lost")
	}
	select {
	case <-original.done:
		t.Fatal("original pending registration closed")
	default:
	}
}
