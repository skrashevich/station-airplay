package event

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/skrashevich/station-airplay/rtsp"
)

const acknowledgment = "RTSP/1.0 200 OK\r\nContent-Length: 0\r\n\r\n"

func eventPhone(t *testing.T) (*Session, *encryptedConn) {
	t.Helper()
	_, session, socket, _, _ := connected(t)
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	crypt, err := newEventCipher(secret)
	if err != nil {
		t.Fatal(err)
	}
	crypt.send, crypt.receive = crypt.receive, crypt.send
	return session, &encryptedConn{Conn: socket, crypt: crypt}
}

func requireEventClosed(t *testing.T, session *Session) {
	t.Helper()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("event session remained available after peer closure or unsolicited response")
	}
	if session.Available() {
		t.Fatal("closed session still available")
	}
}

func TestIdlePeerEOFMakesSessionUnavailable(t *testing.T) {
	session, phone := eventPhone(t)
	if err := phone.Close(); err != nil {
		t.Fatal(err)
	}
	requireEventClosed(t, session)
}

func TestAcknowledgmentThenEOFClosesSession(t *testing.T) {
	session, phone := eventPhone(t)
	served := make(chan error, 1)
	go func() {
		request := new(rtsp.Request)
		if err := request.Read(bufio.NewReader(phone)); err != nil {
			served <- err
			return
		}
		_, err := io.WriteString(phone, acknowledgment)
		phone.Close()
		served <- err
	}()
	if err := session.Send(t.Context(), "pause"); err != nil {
		t.Fatalf("complete acknowledgment lost at EOF: %v", err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	requireEventClosed(t, session)
}

func TestUnsolicitedAcknowledgmentClosesSession(t *testing.T) {
	session, phone := eventPhone(t)
	if _, err := io.WriteString(phone, acknowledgment); err != nil {
		t.Fatal(err)
	}
	requireEventClosed(t, session)
	if err := session.Send(t.Context(), "resume"); err == nil {
		t.Fatal("unsolicited acknowledgment satisfied a new command")
	}
}

func TestDuplicateAcknowledgmentCannotSatisfyNextRequest(t *testing.T) {
	session, phone := eventPhone(t)
	served := make(chan error, 1)
	go func() {
		request := new(rtsp.Request)
		if err := request.Read(bufio.NewReader(phone)); err != nil {
			served <- err
			return
		}
		_, err := io.WriteString(phone, acknowledgment+acknowledgment)
		served <- err
	}()
	// A duplicate in the same record must invalidate the connection, whether
	// the first valid acknowledgment has already completed its command or not.
	_ = session.Send(t.Context(), "pause")
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := session.Send(ctx, "resume"); err == nil {
		t.Fatal("duplicate acknowledgment satisfied a new command")
	}
}

func TestConcurrentCommandsUseOneAcknowledgmentEach(t *testing.T) {
	session, phone := eventPhone(t)
	const count = 8
	served := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(phone)
		for range count {
			if err := new(rtsp.Request).Read(reader); err != nil {
				served <- err
				return
			}
			if _, err := io.WriteString(phone, acknowledgment); err != nil {
				served <- err
				return
			}
		}
		served <- nil
	}()
	var callers sync.WaitGroup
	for range count {
		callers.Go(func() {
			if err := session.Send(t.Context(), "pause"); err != nil {
				t.Error(err)
			}
		})
	}
	callers.Wait()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	if phone.crypt.sendCount != count || phone.crypt.receiveCount != count {
		t.Fatalf("concurrent commands corrupted nonce counters: send=%d receive=%d", phone.crypt.sendCount, phone.crypt.receiveCount)
	}
}

func TestCancellingQueuedCommandPreservesActiveCommand(t *testing.T) {
	session, phone := eventPhone(t)
	active := make(chan error, 1)
	go func() { active <- session.Send(t.Context(), "pause") }()
	if err := new(rtsp.Request).Read(bufio.NewReader(phone)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	queued := make(chan error, 1)
	go func() { queued <- session.Send(ctx, "resume") }()
	cancel()
	if err := <-queued; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued command: %v", err)
	}
	if !session.Available() {
		t.Fatal("queued cancellation closed active command's channel")
	}
	if _, err := io.WriteString(phone, acknowledgment); err != nil {
		t.Fatal(err)
	}
	if err := <-active; err != nil {
		t.Fatalf("active command: %v", err)
	}
}

func TestAcknowledgmentClearsDeadlineBeforeNextIdleRead(t *testing.T) {
	session, phone := eventPhone(t)
	served := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(phone)
		for range 2 {
			if err := new(rtsp.Request).Read(reader); err != nil {
				served <- err
				return
			}
			if _, err := io.WriteString(phone, acknowledgment); err != nil {
				served <- err
				return
			}
		}
		served <- nil
	}()
	deadline := time.Now().Add(100 * time.Millisecond)
	ctx, cancel := context.WithDeadline(t.Context(), deadline)
	if err := session.Send(ctx, "pause"); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	// Neither the expired command deadline nor cancellation after its complete
	// acknowledgment may terminate the following idle read.
	<-time.After(time.Until(deadline) + 10*time.Millisecond)
	if !session.Available() {
		t.Fatal("completed command's deadline closed the idle event channel")
	}
	if err := session.Send(t.Context(), "resume"); err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}
