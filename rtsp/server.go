// Adapted from ibiscum/goplay2; connection state and lifecycle rewritten.
package rtsp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

type Handler interface {
	Handle(*Conn, *Request) (*Response, error)
	OnRequest(*Conn, *Request)
	OnResponse(*Conn, *Response)
	OnConnOpen(*Conn)
}
type Conn struct {
	c             net.Conn
	AfterResponse func()
}

func (c *Conn) NetConn() net.Conn     { return c.c }
func (c *Conn) Close() error          { return c.c.Close() }
func (c *Conn) SetNetConn(n net.Conn) { c.c = n }

// Serve creates isolated protocol handlers and buffers for every connection.
func Serve(ctx context.Context, l net.Listener, factory func() (Handler, error)) error {
	var wg sync.WaitGroup
	slots := make(chan struct{}, 32)
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	defer wg.Wait()
	for {
		raw, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			raw.Close()
			continue
		}
		wg.Go(func() {
			defer func() { <-slots }()
			defer raw.Close()
			stop := context.AfterFunc(ctx, func() { raw.Close() })
			defer stop()
			h, err := factory()
			if err != nil {
				log.Printf("RTSP handler: %v", err)
				return
			}
			conn := &Conn{c: raw}
			h.OnConnOpen(conn)
			defer conn.Close()
			if closer, ok := h.(interface{ OnConnClose(*Conn) }); ok {
				defer closer.OnConnClose(conn)
			}
			br, bw := bufio.NewReaderSize(conn.c, 4096), bufio.NewWriterSize(conn.c, 4096)
			for {
				conn.c.SetReadDeadline(time.Now().Add(5 * time.Minute))
				req := new(Request)
				if err := req.Read(br); err != nil {
					if !errors.Is(err, io.EOF) && ctx.Err() == nil {
						log.Printf("RTSP read: %v", err)
					}
					return
				}
				h.OnRequest(conn, req)
				resp, err := h.Handle(conn, req)
				if err != nil {
					log.Printf("RTSP handle: %v", err)
					if resp == nil {
						return
					}
				}
				if resp == nil {
					return
				}
				if resp.Header == nil {
					resp.Header = make(Header)
				}
				resp.Header["CSeq"] = req.Header["CSeq"]
				resp.Header["Server"] = HeaderValue{"AirTunes/366.0"}
				h.OnResponse(conn, resp)
				conn.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := resp.Write(bw); err != nil {
					return
				}
				if conn.AfterResponse != nil {
					f := conn.AfterResponse
					conn.AfterResponse = nil
					f()
				}
			}
		})
	}
}
