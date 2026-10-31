package rtsp

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
)

type echoHandler struct{}

func (*echoHandler) OnConnOpen(*Conn)            {}
func (*echoHandler) OnRequest(*Conn, *Request)   {}
func (*echoHandler) OnResponse(*Conn, *Response) {}
func (*echoHandler) Handle(_ *Conn, r *Request) (*Response, error) {
	return &Response{StatusCode: StatusOK, Body: r.Body}, nil
}
func TestConcurrentConnectionsKeepTheirOwnBuffers(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, l, func() (Handler, error) { return &echoHandler{}, nil }) }()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			c, err := net.Dial("tcp", l.Addr().String())
			if err != nil {
				t.Error(err)
				return
			}
			defer c.Close()
			br := bufio.NewReader(c)
			for j := range 10 {
				body := fmt.Sprintf("%d-%d", i, j)
				fmt.Fprintf(c, "SETUP /x RTSP/1.0\r\nCSeq: %d\r\nContent-Length: %d\r\n\r\n%s", j, len(body), body)
				var r Response
				if err := r.Read(br); err != nil {
					t.Error(err)
					return
				}
				if string(r.Body) != body {
					t.Errorf("body=%q want=%q", r.Body, body)
					return
				}
			}
		})
	}
	wg.Wait()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
