package audio

import (
	"github.com/skrashevich/station-airplay/codec"
	"github.com/skrashevich/station-airplay/ptp"
	"net"
	"sync"
	"testing"
	"time"
)

type deadlineConn struct {
	net.Conn
	deadline time.Time
	reading  chan struct{}
	once     sync.Once
}

func (c *deadlineConn) SetReadDeadline(d time.Time) error {
	c.deadline = d
	return c.Conn.SetReadDeadline(d)
}
func (c *deadlineConn) Read(b []byte) (int, error) {
	c.once.Do(func() { close(c.reading) })
	return c.Conn.Read(b)
}

type deadlineListener struct {
	net.Listener
	accepted chan *deadlineConn
}

func (l *deadlineListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	wrapped := &deadlineConn{Conn: c, reading: make(chan struct{})}
	l.accepted <- wrapped
	return wrapped, nil
}

func TestPausedMediaReadHasNoIdleExpiryAndCloseUnblocksIt(t *testing.T) {
	raw, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &deadlineListener{Listener: raw, accepted: make(chan *deadlineConn, 1)}
	s := NewServer(NewPlayer(t.Context(), ptp.NewVirtualClock(0), codec.OutputOptions{}))
	go s.receive(l)
	client, err := net.Dial("tcp4", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	c := <-l.accepted
	t.Cleanup(func() { s.Close(); raw.Close(); <-s.inputDone })
	<-c.reading
	if !c.deadline.IsZero() {
		t.Fatalf("paused media is disconnected at %v", c.deadline)
	}
}
