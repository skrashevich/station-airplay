// AirPlay event connections are independent of audio/control sockets.
package event

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

func Serve(ctx context.Context, l net.Listener) {
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	slots := make(chan struct{}, 8)
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			c.Close()
			continue
		}
		wg.Go(func() {
			defer func() { <-slots }()
			defer c.Close()
			stop := context.AfterFunc(ctx, func() { c.Close() })
			defer stop()
			var b [4096]byte
			for {
				c.SetReadDeadline(time.Now().Add(2 * time.Minute))
				_, err := c.Read(b[:])
				if err != nil {
					if err == io.EOF {
						return
					}
					return
				}
			}
		})
	}
}
